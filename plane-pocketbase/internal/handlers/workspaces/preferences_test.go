package workspaces

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

// TestSidebarPreferenceDetail covers PATCH …/sidebar-preferences/{key}/: the
// pin toggle flips the persisted row and returns the single item; an unknown
// key is a 404, not a 500.
func TestSidebarPreferenceDetail(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	seed := func(collection string, set func(r *core.Record)) *core.Record {
		t.Helper()
		coll, err := app.FindCollectionByNameOrId(collection)
		if err != nil {
			t.Fatalf("collection %s: %v", collection, err)
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		set(rec)
		if err := app.Save(rec); err != nil {
			t.Fatalf("save %s: %v", collection, err)
		}
		return rec
	}

	user := seed("users", func(r *core.Record) {
		r.Set("username", "me")
		r.Set("email", "me@example.com")
		r.Set("password", "1234567890")
		r.Set("display_name", "Me")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	workspace := seed("workspaces", func(r *core.Record) {
		r.Set("name", "WS")
		r.Set("slug", "ws")
		r.Set("owner", user.Id)
	})
	pref := seed("workspace_user_preferences", func(r *core.Record) {
		r.Set("workspace", workspace.Id)
		r.Set("user", user.Id)
		r.Set("key", "drafts")
		r.Set("is_pinned", false)
		r.Set("sort_order", float64(65535))
	})

	scope := &middleware.Scope{Workspace: workspace, WorkspaceRole: 15}
	req := httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"is_pinned": true}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("key", "drafts")
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: app}
	e.Request = req
	e.Response = rec
	e.Auth = user
	e.Set("plane.scope", scope)

	h := New(app)
	if err := h.SidebarPreferenceDetail(e); err != nil {
		t.Fatalf("patch preference: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("patch preference status %d body %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if out["key"] != "drafts" || out["is_pinned"] != true {
		t.Fatalf("patched item: %s", rec.Body.String())
	}

	fresh, _ := app.FindRecordById("workspace_user_preferences", pref.Id)
	if !fresh.GetBool("is_pinned") {
		t.Fatalf("is_pinned must persist")
	}

	// Unknown key → 404.
	req = httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(`{"is_pinned": true}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("key", "nope")
	rec = httptest.NewRecorder()
	e = &core.RequestEvent{App: app}
	e.Request = req
	e.Response = rec
	e.Auth = user
	e.Set("plane.scope", scope)
	if err := h.SidebarPreferenceDetail(e); err != nil {
		t.Fatalf("patch unknown key: %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown key status %d", rec.Code)
	}
}

// TestWorkspaceMemberDetail covers GET/PATCH/DELETE
// /api/workspaces/{slug}/members/{pk}/: the admin shape carries the email,
// role changes persist, self-removal is blocked, and removal deactivates.
func TestWorkspaceMemberDetail(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	seed := func(collection string, set func(r *core.Record)) *core.Record {
		t.Helper()
		coll, err := app.FindCollectionByNameOrId(collection)
		if err != nil {
			t.Fatalf("collection %s: %v", collection, err)
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		set(rec)
		if err := app.Save(rec); err != nil {
			t.Fatalf("save %s: %v", collection, err)
		}
		return rec
	}

	admin := seed("users", func(r *core.Record) {
		r.Set("username", "admin")
		r.Set("email", "admin@example.com")
		r.Set("password", "1234567890")
		r.Set("display_name", "Admin")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	peer := seed("users", func(r *core.Record) {
		r.Set("username", "peer")
		r.Set("email", "peer@example.com")
		r.Set("password", "1234567890")
		r.Set("display_name", "Peer")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	workspace := seed("workspaces", func(r *core.Record) {
		r.Set("name", "WS")
		r.Set("slug", "ws")
		r.Set("owner", admin.Id)
	})
	adminMember := seed("workspace_members", func(r *core.Record) {
		r.Set("workspace", workspace.Id)
		r.Set("member", admin.Id)
		r.Set("is_active", true)
		r.Set("role", 20)
	})
	peerMember := seed("workspace_members", func(r *core.Record) {
		r.Set("workspace", workspace.Id)
		r.Set("member", peer.Id)
		r.Set("is_active", true)
		r.Set("role", 15)
	})

	h := New(app)
	scope := &middleware.Scope{Workspace: workspace, WorkspaceRole: 20}
	call := func(method, body, pk string) (*httptest.ResponseRecorder, *core.RequestEvent) {
		t.Helper()
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, "/", reader)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.SetPathValue("pk", pk)
		rec := httptest.NewRecorder()
		e := &core.RequestEvent{App: app}
		e.Request = req
		e.Response = rec
		e.Auth = admin
		e.Set("plane.scope", scope)
		return rec, e
	}

	rec, e := call(http.MethodGet, "", peerMember.GetString("uuid"))
	if err := h.MemberDetail(e); err != nil {
		t.Fatalf("member GET: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("member GET status %d body %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	memberObj, _ := got["member"].(map[string]any)
	if memberObj["email"] != "peer@example.com" {
		t.Fatalf("member GET must carry the email (admin view): %s", rec.Body.String())
	}

	rec, e = call(http.MethodPatch, `{"role": 5}`, peerMember.GetString("uuid"))
	if err := h.MemberDetail(e); err != nil {
		t.Fatalf("member PATCH: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("member PATCH status %d body %s", rec.Code, rec.Body.String())
	}
	if got = decodeMap(t, rec); got["role"].(float64) != 5 {
		t.Fatalf("member PATCH role: %s", rec.Body.String())
	}

	// Self-role change and self-removal are 400s (use leave instead).
	rec, e = call(http.MethodPatch, `{"role": 5}`, adminMember.GetString("uuid"))
	if err := h.MemberDetail(e); err != nil {
		t.Fatalf("self PATCH: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("self role change must 400: %d", rec.Code)
	}
	rec, e = call(http.MethodDelete, "", adminMember.GetString("uuid"))
	if err := h.MemberDetail(e); err != nil {
		t.Fatalf("self DELETE: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("self removal must 400: %d", rec.Code)
	}

	rec, e = call(http.MethodDelete, "", peerMember.GetString("uuid"))
	if err := h.MemberDetail(e); err != nil {
		t.Fatalf("member DELETE: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("member DELETE status %d", rec.Code)
	}
	fresh, _ := app.FindRecordById("workspace_members", peerMember.Id)
	if fresh.GetBool("is_active") {
		t.Fatalf("removed member must be deactivated")
	}
	rec, e = call(http.MethodGet, "", peerMember.GetString("uuid"))
	if err := h.MemberDetail(e); err != nil {
		t.Fatalf("member GET after delete: %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("member GET after delete status %d", rec.Code)
	}
}

func decodeMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out
}
