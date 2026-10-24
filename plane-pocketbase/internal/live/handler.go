package live

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
)

// Handler serves `/live/collaboration`, the socket the page editor opens.
type Handler struct {
	app      core.App
	hub      *Hub
	upgrader websocket.Upgrader
}

// hubs memoises the handler per app. live.New runs once per serve, and REST
// code (the page-restore path, see Handler.PurgeDocument) reaches the same hub
// through New — a second hub would evict nothing and could not even remove the
// log file out from under the socket's open handle on Windows.
var hubs sync.Map // core.App -> *Handler

// New builds the collaboration handler. Document logs live in
// `<data dir>/live/`, next to the SQLite database, so a backup of the data
// directory captures page content too. It is idempotent per app: the first
// call builds the hub, later calls return the same handler.
func New(app core.App) *Handler {
	if existing, ok := hubs.Load(app); ok {
		return existing.(*Handler)
	}
	h := &Handler{
		app: app,
		hub: NewHub(app, filepath.Join(app.DataDir(), "live")),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			// The socket is same-deployment but not necessarily same-origin
			// (the dev stack runs the web app on :3000 and the API on :8000).
			// The credential is the session cookie checked below — the Origin
			// header would only add a false sense of safety, since a browser
			// never lets a page read another origin's socket frames anyway.
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
	hubs.Store(app, h)
	return h
}

// CollectOrphans removes collaboration logs whose page is gone (see
// Hub.CollectOrphans). Called once at startup.
func (h *Handler) CollectOrphans() int { return h.hub.CollectOrphans() }

// PurgeDocument drops one document's collaboration log: the in-memory room is
// evicted (its file handle closed) and the log plus its sidecars are removed
// from disk. The page-restore path calls it with the page's uuid, because the
// Yjs relay would otherwise replay the pre-restore CRDT updates on top of the
// restored content and the restore would silently appear to do nothing. The
// next join re-creates an empty log, which seedFromStoredBinary fills from the
// page's just-restored description_binary.
func (h *Handler) PurgeDocument(documentName string) {
	h.hub.purgeDocument(documentName)
}

// Serve upgrades the request and runs the connection until it closes.
func (h *Handler) Serve(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return e.JSON(http.StatusUnauthorized, map[string]any{
			"detail": "Authentication credentials were not provided.",
		})
	}

	ws, err := h.upgrader.Upgrade(e.Response, e.Request, nil)
	if err != nil {
		// Upgrade already answered with an HTTP error.
		return nil
	}

	c := newConn(ws, user.Id, user.GetString("uuid"))
	ws.SetReadLimit(maxMessageSize)
	ws.SetPongHandler(func(string) error {
		return ws.SetReadDeadline(time.Now().Add(pongWait))
	})
	go c.writeLoop()

	// The document name only exists inside the frames, so the socket must be
	// accepted before the page can be authorized. The deadline stops a client
	// that opens a socket and says nothing from parking a goroutine forever.
	_ = ws.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, first, err := ws.ReadMessage()
	if err != nil {
		c.close()
		return nil
	}

	documentName, msgType, payload, ok := splitFrame(first)
	if !ok || documentName == "" {
		c.close()
		return nil
	}

	if err := h.authorize(user, documentName, e.Request.URL.Query().Get("projectId")); err != nil {
		log.Printf("[live] denied %q for user %s: %v", documentName, user.Id, err)
		_ = ws.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(4403, "forbidden"),
			time.Now().Add(time.Second),
		)
		c.close()
		return nil
	}

	r, err := h.hub.room(documentName)
	if err != nil {
		log.Printf("[live] open room %q: %v", documentName, err)
		c.close()
		return nil
	}
	// A log that is gone (fresh data dir, restored database) must not make the
	// page open blank: the stored `description_binary` is a full yjs snapshot and
	// is the only other copy of the content.
	h.seedFromStoredBinary(r, documentName)
	c.room = r
	r.join(c)
	defer func() {
		r.leave(c)
		c.close()
	}()

	// Ask the peer for its full state. A client that reconnects with an
	// IndexedDB cache answers with everything it knows, which is how edits made
	// while the socket was down reach the server.
	c.trySend(buildSyncMessage(documentName, syncStep1, emptyStateVector))

	h.handleFrame(c, msgType, payload)

	for {
		_ = ws.SetReadDeadline(time.Now().Add(pongWait))
		_, data, err := ws.ReadMessage()
		if err != nil {
			return nil
		}
		// The document name is fixed by the handshake; later frames repeat it,
		// and a client cannot switch documents on an open socket.
		_, frameType, body, ok := splitFrame(data)
		if !ok {
			continue
		}
		h.handleFrame(c, frameType, body)
	}
}

// handleFrame dispatches one decoded frame.
func (h *Handler) handleFrame(c *conn, msgType uint64, payload []byte) {
	if c.room == nil {
		return
	}

	switch msgType {
	case msgAuth:
		h.handleAuth(c, payload)

	case msgSync:
		syncType, n, ok := readVarUint(payload)
		if !ok {
			return
		}
		data, _, ok := readVarBytes(payload[n:])
		if !ok {
			return
		}
		switch syncType {
		case syncStep1:
			// History first, then the empty diff: the client marks itself
			// synced the moment it receives a SyncStep2, and it should already
			// have the content by then (no empty flash in the editor).
			c.room.replay(c)
			for _, msg := range c.room.awarenessSnapshot() {
				c.trySend(msg)
			}
			c.trySend(buildSyncMessage(c.room.name, syncStep2, emptyUpdate))
			// From here on the peer holds the whole document, so it is a valid
			// source for a compaction snapshot.
			c.synced.Store(true)
		case syncStep2:
			// Two different things arrive as SyncStep2: the peer's answer to our
			// handshake SyncStep1 (its own diff) and, when a log has grown too
			// large, the peer's answer to an explicit snapshot request (its whole
			// document). The room knows which one it is waiting for.
			if c.room.applySnapshot(c, data) {
				return
			}
			c.room.record(c, data)
		case syncUpdate:
			// An incremental change. The relay treats it like any other update.
			c.room.record(c, data)
		}

	case msgAwareness:
		data, _, ok := readVarBytes(payload)
		if !ok {
			return
		}
		c.room.applyAwareness(c, data)

	case msgQueryAwareness:
		for _, msg := range c.room.awarenessSnapshot() {
			c.trySend(msg)
		}

	case msgClose:
		c.close()

	default:
		// Stateless/broadcast/sync-status frames come from extensions this
		// deployment does not run. Ignoring them keeps the socket alive for
		// clients that send them anyway.
	}
}

// handleAuth answers the editor's Auth frame. The token is not a secret — the
// editor fills it with `JSON.stringify({id, name, color})` — so it is only
// checked for agreement with the session that opened the socket, exactly like
// apps/live/src/lib/auth.ts does.
func (h *Handler) handleAuth(c *conn, payload []byte) {
	kind, n, ok := readVarUint(payload)
	if !ok || kind != authToken {
		return
	}
	token, _, ok := readVarString(payload[n:])
	if !ok {
		return
	}

	var claims struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(token), &claims); err != nil {
		log.Printf("[live] auth token is not JSON on %q: %v", c.room.name, err)
		c.trySend(buildAuthMessage(c.room.name, authDenied, "forbidden"))
		return
	}
	// The editor fills this with the user object it has from the API, whose id
	// is the `uuid` column — not the record id. Accept either so the check does
	// not depend on which surface the editor read the user from.
	if claims.ID != c.user && (c.userAPIID == "" || claims.ID != c.userAPIID) {
		log.Printf("[live] auth mismatch on %q: token id %q vs session %q/%q",
			c.room.name, claims.ID, c.user, c.userAPIID)
		c.trySend(buildAuthMessage(c.room.name, authDenied, "forbidden"))
		return
	}
	c.trySend(buildAuthMessage(c.room.name, authOK, scopeReadWrite))
}

// authorize checks that the caller may edit this page: the page exists, the
// user belongs to its workspace, and (when the client names a project) the page
// is actually in that project. Mirrors the REST API's page permissions rather
// than trusting the client's query string.
//
// Both identifiers arrive as the API-visible id, which for Plane entities is the
// `uuid` column, not PocketBase's own record id (see serializers.UUID). Looking
// them up by record id would work only for the handful of rows where the two
// happen to agree.
func (h *Handler) authorize(user *core.Record, documentName, projectID string) error {
	page, err := findEntityByAPIID(h.app, "pages", documentName)
	if err != nil {
		return fmt.Errorf("page lookup: %w", err)
	}
	workspaceID := page.GetString("workspace")
	if workspaceID == "" {
		return errors.New("page has no workspace")
	}
	if _, err := h.app.FindFirstRecordByFilter(
		"workspace_members",
		"workspace = {:workspace} && member = {:member}",
		dbx.Params{"workspace": workspaceID, "member": user.Id},
	); err != nil {
		return fmt.Errorf("user is not a member of workspace %s", workspaceID)
	}
	if projectID != "" && !page.GetBool("is_global") {
		project, err := findEntityByAPIID(h.app, "projects", projectID)
		if err != nil {
			return fmt.Errorf("project lookup: %w", err)
		}
		if _, err := h.app.FindFirstRecordByFilter(
			"project_pages",
			"project = {:project} && page = {:page}",
			dbx.Params{"project": project.Id, "page": page.Id},
		); err != nil {
			return fmt.Errorf("page is not part of project %s", projectID)
		}
	}
	return nil
}

// findEntityByAPIID resolves an id the API hands out (`uuid`) to a record,
// falling back to the record id for callers that pass the raw PocketBase id.
func findEntityByAPIID(app core.App, collection, apiID string) (*core.Record, error) {
	record, err := app.FindFirstRecordByFilter(collection, "uuid = {:id}", dbx.Params{"id": apiID})
	if err == nil {
		return record, nil
	}
	return app.FindRecordById(collection, apiID)
}

// splitFrame decodes `[varstring documentName][varuint type][payload]`.
func splitFrame(data []byte) (documentName string, msgType uint64, payload []byte, ok bool) {
	documentName, n, ok := readVarString(data)
	if !ok {
		return "", 0, nil, false
	}
	msgType, k, ok := readVarUint(data[n:])
	if !ok {
		return "", 0, nil, false
	}
	return documentName, msgType, data[n+k:], true
}
