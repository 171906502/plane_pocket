package live

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

const (
	// compactThreshold is when a document's log is worth rewriting. An edit
	// costs 100–200 bytes, so this is tens of thousands of edits — normal pages
	// never reach it.
	compactThreshold = 1 << 20
	// compactMinRecords keeps compaction away from short logs, where the saving
	// would not justify touching the file.
	compactMinRecords = 64
	// compactTimeout drops a snapshot request whose answer never arrived (a peer
	// that does not speak the sync protocol, or one that vanished).
	compactTimeout = 15 * time.Second
	// compactMinAge is how long a peer must have been in the room before it is
	// asked for a snapshot. The handshake's own SyncStep2 arrives within
	// milliseconds, so waiting rules it out and the answer we consume is
	// guaranteed to be the reply to our request.
	compactMinAge = 10 * time.Second
	// idleTimeout releases a room nobody is connected to. Reloading from disk on
	// the next join is one linear scan, and the process stops holding a file
	// handle for every page ever opened.
	idleTimeout = 5 * time.Minute
)

// Hub owns every open document room and the on-disk log behind it.
//
// A room is kept while peers are connected (plus a grace period) because its
// in-memory update slice *is* the document; recreating it on every connect
// would mean re-reading the log each time.
type Hub struct {
	app core.App
	dir string

	// Compaction thresholds. Defaults are the constants above; the environment
	// overrides exist so the path can be exercised on a small document instead
	// of a 1 MiB one (and so an operator can turn compaction off with 0).
	compactBytes   int64
	compactRecords int

	mu    sync.Mutex
	rooms map[string]*room
}

// NewHub creates a hub that persists document logs under dir.
func NewHub(app core.App, dir string) *Hub {
	return &Hub{
		app:            app,
		dir:            dir,
		rooms:          map[string]*room{},
		compactBytes:   envInt64("PLANE_LIVE_COMPACT_BYTES", compactThreshold),
		compactRecords: int(envInt64("PLANE_LIVE_COMPACT_RECORDS", compactMinRecords)),
	}
}

// envInt64 reads a non-negative integer override, falling back on anything it
// cannot parse so a typo cannot silently disable a safety net.
func envInt64(key string, fallback int64) int64 {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		log.Printf("[live] ignoring %s=%q (want a non-negative integer)", key, raw)
		return fallback
	}
	return value
}

// room returns the room for a document, opening its log on first use.
func (h *Hub) room(documentName string) (*room, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.rooms[documentName]; ok {
		return r, nil
	}
	docFile, updates, err := openDocLog(h.dir, documentName)
	if err != nil {
		return nil, err
	}
	r := &room{
		name:      documentName,
		hub:       h,
		log:       docFile,
		updates:   updates,
		conns:     map[*conn]struct{}{},
		awareness: map[uint32]awarenessEntry{},
	}
	h.rooms[documentName] = r
	if len(updates) > 0 {
		log.Printf("[live] %s: replayed %d stored update(s)", documentName, len(updates))
	}
	return r, nil
}

// evict releases a room that has been empty for idleTimeout.
//
// Lock order is room → hub, matching join/leave (which take room.mu and never
// reach for hub.mu). Taking them the other way round would deadlock against
// room(), which holds hub.mu while opening the log.
func (h *Hub) evict(r *room) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.conns) > 0 || r.compacting {
		return
	}
	h.mu.Lock()
	if h.rooms[r.name] == r {
		delete(h.rooms, r.name)
	}
	h.mu.Unlock()
	if err := r.log.close(); err != nil {
		log.Printf("[live] %s: close failed: %v", r.name, err)
	}
	r.updates = nil
	log.Printf("[live] %s: idle, released", r.name)
}

// awarenessEntry is the last presence record seen for one clientID.
type awarenessEntry struct {
	clock uint64
	state []byte
	owner *conn
}

// purgeDocument evicts the room for documentName — when one is open — and
// removes its log and sidecars from disk. Called by Handler.PurgeDocument
// (the page-restore path): the relay must not replay the pre-restore updates
// over the restored content.
//
// Lock order is hub → room, taken in two separate critical sections (the map
// entry is removed under hub.mu, the room is closed under its own mu
// afterwards). Taking them nested would deadlock against evict(), which runs
// from the idle timer holding room.mu and reaching for hub.mu.
func (h *Hub) purgeDocument(documentName string) {
	h.mu.Lock()
	r, open := h.rooms[documentName]
	if open {
		delete(h.rooms, documentName)
	}
	h.mu.Unlock()

	if open {
		r.mu.Lock()
		if r.log != nil {
			if err := r.log.close(); err != nil {
				log.Printf("[live] %s: purge close failed: %v", documentName, err)
			}
		}
		r.updates = nil
		r.mu.Unlock()
	}

	// Same suffix set as removeLogFiles: the log itself plus the `.name`
	// sidecar and the `.bak` compaction backup.
	removeLogFiles(filepath.Join(h.dir, safeFileName(documentName)+".log"))
	log.Printf("[live] %s: purged (restore)", documentName)
}

// room is one document: its connections, its history, and who is present.
type room struct {
	name string
	hub  *Hub

	mu        sync.Mutex
	conns     map[*conn]struct{}
	updates   [][]byte
	awareness map[uint32]awarenessEntry

	log *docLog

	// compaction state, guarded by mu
	compacting  bool
	compactConn *conn
	compactIdx  int

	idleTimer *time.Timer
}

func (r *room) join(c *conn) {
	r.mu.Lock()
	r.conns[c] = struct{}{}
	if r.idleTimer != nil {
		r.idleTimer.Stop()
		r.idleTimer = nil
	}
	conns := len(r.conns)
	updates := len(r.updates)
	r.mu.Unlock()
	log.Printf("[live] %s: join by user %s (conns=%d, stored=%d)", r.name, c.user, conns, updates)
}

// leave removes a connection and tells the remaining peers that its presence
// states are gone, otherwise every disconnect leaves a ghost cursor behind.
func (r *room) leave(c *conn) {
	r.mu.Lock()
	delete(r.conns, c)

	var removals [][]byte
	for id, entry := range r.awareness {
		if entry.owner != c {
			continue
		}
		delete(r.awareness, id)
		// y-protocols removes a client by sending the same clientID with a
		// higher clock and a `null` state.
		removals = append(removals, buildAwarenessMessage(r.name,
			buildAwarenessState(id, entry.clock+1, []byte("null"))))
	}
	peers := r.peerList()
	if len(r.conns) == 0 && r.idleTimer == nil {
		r.idleTimer = time.AfterFunc(idleTimeout, func() { r.hub.evict(r) })
	}
	r.mu.Unlock()

	for _, msg := range removals {
		for _, p := range peers {
			p.trySend(msg)
		}
	}
}

// record persists an update from one peer and forwards it to the others.
//
// Persist-then-broadcast: if the write fails the room keeps working in memory
// (the update is still relayed), it just will not survive a restart. Losing
// durability is better than dropping a user's keystrokes.
//
// The room lock is held across the write *and* the broadcast so a concurrent
// compaction (which rewrites the whole file) cannot interleave with an append,
// and so every peer sees updates in the same order they were recorded. That
// order is what makes a snapshot trustworthy: when a peer is asked for one, it
// has provably already received every update up to compactIdx.
//
// trySend never blocks (it is a buffered queue that drops the peer when full),
// so holding the lock across it is safe.
func (r *room) record(from *conn, update []byte) {
	if isNoopUpdate(update) {
		return
	}

	r.mu.Lock()
	if err := r.log.append(update); err != nil {
		log.Printf("[live] %s: persist failed: %v", r.name, err)
	}
	r.updates = append(r.updates, update)
	msg := buildSyncMessage(r.name, syncUpdate, update)
	for c := range r.conns {
		if c != from {
			c.trySend(msg)
		}
	}

	var target *conn
	if r.shouldCompactLocked() {
		for c := range r.conns {
			if c.snapshotReady() {
				target = c
				break
			}
		}
		if target != nil {
			r.compacting = true
			r.compactConn = target
			r.compactIdx = len(r.updates)
			// Queued after this update, so the peer's document already includes
			// it when it answers.
			target.trySend(buildSyncMessage(r.name, syncStep1, emptyStateVector))
		}
	}
	size := r.log.currentSize()
	stored := len(r.updates)
	r.mu.Unlock()

	if target != nil {
		log.Printf("[live] %s: log is %d bytes over %d update(s), asking %s for a snapshot",
			r.name, size, stored, target.user)
		time.AfterFunc(compactTimeout, func() { r.abandonSnapshot(target) })
	}
}

// shouldCompactLocked reports whether this is a good moment to ask for a
// snapshot. Callers hold r.mu.
func (r *room) shouldCompactLocked() bool {
	if r.compacting || r.hub.compactBytes <= 0 || r.hub.compactRecords <= 0 {
		return false
	}
	return len(r.updates) >= r.hub.compactRecords && r.log.currentSize() >= r.hub.compactBytes
}

// abandonSnapshot gives up on a request whose answer never arrived, so a later
// edit can try again.
func (r *room) abandonSnapshot(target *conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.compacting && r.compactConn == target {
		r.compacting = false
		r.compactConn = nil
		log.Printf("[live] %s: snapshot request timed out, log left as is", r.name)
	}
}

// hasContent reports whether the room already holds updates, so callers can
// skip the database lookup that seeding would need.
func (r *room) hasContent() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.updates) > 0
}

// seed installs a stored snapshot as the room's first update.
//
// Used when the log is gone (fresh data directory, restored database, log file
// deleted by hand) but the page still has a `description_binary`: without it the
// editor would open the page blank and the next autosave would overwrite the
// stored HTML with nothing.
func (r *room) seed(snapshot []byte) {
	if isNoopUpdate(snapshot) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.updates) > 0 {
		return
	}
	if err := r.log.append(snapshot); err != nil {
		log.Printf("[live] %s: seed persist failed: %v", r.name, err)
	}
	r.updates = append(r.updates, snapshot)
	log.Printf("[live] %s: seeded from the stored description_binary (%d bytes)", r.name, len(snapshot))
}

// replay pushes the stored history to a single connection, one update per
// record. Applying them in order reconstructs the document on the client.
func (r *room) replay(c *conn) {
	r.mu.Lock()
	history := make([][]byte, len(r.updates))
	copy(history, r.updates)
	r.mu.Unlock()

	for _, update := range history {
		c.trySend(buildSyncMessage(r.name, syncUpdate, update))
	}
}

// applySnapshot replaces the log with a peer's full state plus whatever was
// recorded after the request. It reports whether the update was consumed as a
// snapshot (false means the caller should treat it as a normal edit).
func (r *room) applySnapshot(from *conn, snapshot []byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.compacting || r.compactConn != from {
		return false
	}
	r.compacting = false
	r.compactConn = nil

	if isNoopUpdate(snapshot) {
		// A peer with an empty document cannot be the source of truth for a log
		// that has content; keep what is on disk.
		log.Printf("[live] %s: peer returned an empty snapshot, keeping the log", r.name)
		return true
	}

	idx := r.compactIdx
	if idx > len(r.updates) {
		idx = len(r.updates)
	}
	tail := append([][]byte(nil), r.updates[idx:]...)
	records := append([][]byte{snapshot}, tail...)

	before := r.log.currentSize()
	size, err := r.log.replace(records)
	if err != nil {
		log.Printf("[live] %s: compaction failed, log left as is: %v", r.name, err)
		return true
	}
	r.updates = records
	log.Printf("[live] %s: compacted %d update(s)/%d bytes into 1 snapshot + %d tail update(s) = %d bytes",
		r.name, idx, before, len(tail), size)
	return true
}

// applyAwareness records presence for a connection and relays it.
func (r *room) applyAwareness(from *conn, payload []byte) {
	r.mu.Lock()
	if states, ok := parseAwarenessUpdate(payload); ok {
		for _, s := range states {
			r.awareness[s.clientID] = awarenessEntry{clock: s.clock, state: s.json, owner: from}
			from.ids[s.clientID] = struct{}{}
		}
	}
	peers := r.peerList()
	r.mu.Unlock()

	// Relay even when parsing failed: the bytes are still valid for peers, and
	// bookkeeping is the only thing that needs to understand them.
	msg := buildAwarenessMessage(r.name, payload)
	for _, p := range peers {
		if p != from {
			p.trySend(msg)
		}
	}
}

// awarenessSnapshot returns one awareness message per known client, ready to be
// sent to a connection that just joined.
func (r *room) awarenessSnapshot() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]byte, 0, len(r.awareness))
	for id, entry := range r.awareness {
		out = append(out, buildAwarenessMessage(r.name, buildAwarenessState(id, entry.clock, entry.state)))
	}
	return out
}

// peerList must be called with r.mu held.
func (r *room) peerList() []*conn {
	peers := make([]*conn, 0, len(r.conns))
	for c := range r.conns {
		peers = append(peers, c)
	}
	return peers
}
