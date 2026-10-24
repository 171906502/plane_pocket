package projects

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

// fixture is the minimal graph the member/state method-gap tests need: two
// users (the caller is a project admin, the peer an active workspace member),
// one workspace, one project. Rows are seeded before any assertion — a 200
// with an empty array proves nothing (docs/07 §4 conclusion 4).
type fixture struct {
	app       *tests.TestApp
	user      *core.Record
	peer      *core.Record
	workspace *core.Record
	project   *core.Record
	scope     *middleware.Scope
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	f := &fixture{app: app}
	f.user = mustSave(t, app, "users", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-0000000000a1")
		r.Set("username", "me")
		r.Set("email", "me@example.com")
		r.Set("password", "1234567890")
		r.Set("display_name", "Me")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	f.peer = mustSave(t, app, "users", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-0000000000a2")
		r.Set("username", "peer")
		r.Set("email", "peer@example.com")
		r.Set("password", "1234567890")
		r.Set("display_name", "Peer")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	f.workspace = mustSave(t, app, "workspaces", func(r *core.Record) {
		r.Set("name", "WS")
		r.Set("slug", "ws")
		r.Set("owner", f.user.Id)
	})
	f.project = mustSave(t, app, "projects", func(r *core.Record) {
		r.Set("name", "P")
		r.Set("identifier", "P")
		r.Set("workspace", f.workspace.Id)
	})
	member := mustSave(t, app, "project_members", func(r *core.Record) {
		r.Set("project", f.project.Id)
		r.Set("member", f.user.Id)
		r.Set("workspace", f.workspace.Id)
		r.Set("is_active", true)
		r.Set("role", 20)
	})
	// The peer is a workspace member but NOT yet a project member — that is
	// exactly the state MembersCreate is supposed to change.
	mustSave(t, app, "workspace_members", func(r *core.Record) {
		r.Set("workspace", f.workspace.Id)
		r.Set("member", f.peer.Id)
		r.Set("is_active", true)
		r.Set("role", 15)
	})

	f.scope = &middleware.Scope{
		Workspace:       f.workspace,
		Project:         f.project,
		WorkspaceMember: member,
		ProjectMember:   member,
		WorkspaceRole:   20,
		ProjectRole:     20,
	}
	return f
}

func (f *fixture) event(t *testing.T, method, target, body string, pathValues map[string]string) (*core.RequestEvent, *httptest.ResponseRecorder) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range pathValues {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: f.app}
	e.Request = req
	e.Response = rec
	e.Auth = f.user
	e.Set("plane.scope", f.scope)
	return e, rec
}

func mustSave(t *testing.T, app core.App, collection string, seed func(r *core.Record)) *core.Record {
	t.Helper()
	coll, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		t.Fatalf("collection %s: %v", collection, err)
	}
	rec := core.NewRecord(coll)
	rec.Set("uuid", serializers.NewUUID())
	seed(rec)
	if err := app.Save(rec); err != nil {
		t.Fatalf("save %s: %v", collection, err)
	}
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) []any {
	t.Helper()
	var out []any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out
}

func decodeOne(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out
}

// TestProjectMembersCRUD covers bulk-add, retrieve, role patch and removal on
// …/projects/{project_id}/members/.
func TestProjectMembersCRUD(t *testing.T) {
	f := newFixture(t)
	h := New(f.app)

	// Bulk add: the peer joins as a member. 201 + the membership row.
	body := `{"members": [{"member_id": "` + f.peer.GetString("uuid") + `", "role": 15}]}`
	e, rec := f.event(t, http.MethodPost, "/", body, nil)
	if err := h.MembersCreate(e); err != nil {
		t.Fatalf("members POST: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("members POST status %d body %s", rec.Code, rec.Body.String())
	}
	rows := decode(t, rec)
	if len(rows) != 1 {
		t.Fatalf("members POST: want 1 row, got %s", rec.Body.String())
	}
	first := rows[0].(map[string]any)
	if first["role"].(float64) != 15 {
		t.Fatalf("members POST role: %s", rec.Body.String())
	}
	memberUUID, _ := first["id"].(string)

	// Re-adding the same person must not create a second row
	// (Django's bulk_create ignore_conflicts).
	e, rec = f.event(t, http.MethodPost, "/", body, nil)
	if err := h.MembersCreate(e); err != nil {
		t.Fatalf("members POST again: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("members POST again status %d", rec.Code)
	}
	existing, _ := f.app.FindRecordsByFilter("project_members",
		"project = {:p} && deleted_at = ''", "", 0, 0, map[string]any{"p": f.project.Id})
	if len(existing) != 2 {
		t.Fatalf("duplicate membership created: %d rows", len(existing))
	}

	// A user who is not an active workspace member is skipped, not 500'd.
	stranger := mustSave(t, f.app, "users", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-0000000000a3")
		r.Set("username", "stranger")
		r.Set("email", "stranger@example.com")
		r.Set("password", "1234567890")
		r.Set("display_name", "Stranger")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	e, rec = f.event(t, http.MethodPost, "/",
		`{"members": [{"member_id": "`+stranger.GetString("uuid")+`", "role": 15}]}`, nil)
	if err := h.MembersCreate(e); err != nil {
		t.Fatalf("members POST stranger: %v", err)
	}
	if rec.Code != http.StatusCreated || len(decode(t, rec)) != 0 {
		t.Fatalf("stranger must be skipped: %d %s", rec.Code, rec.Body.String())
	}

	// GET detail (admin shape carries the email), PATCH role, DELETE.
	e, rec = f.event(t, http.MethodGet, "/", "", map[string]string{"pk": memberUUID})
	if err := h.MemberDetail(e); err != nil {
		t.Fatalf("member GET: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("member GET status %d body %s", rec.Code, rec.Body.String())
	}
	got := decodeOne(t, rec)
	memberObj, _ := got["member"].(map[string]any)
	if memberObj["email"] != "peer@example.com" {
		t.Fatalf("member GET must use the admin-lite shape: %s", rec.Body.String())
	}

	e, rec = f.event(t, http.MethodPatch, "/", `{"role": 5}`, map[string]string{"pk": memberUUID})
	if err := h.MemberDetail(e); err != nil {
		t.Fatalf("member PATCH: %v", err)
	}
	if rec.Code != http.StatusOK || decodeOne(t, rec)["role"].(float64) != 5 {
		t.Fatalf("member PATCH: %d %s", rec.Code, rec.Body.String())
	}

	e, rec = f.event(t, http.MethodDelete, "/", "", map[string]string{"pk": memberUUID})
	if err := h.MemberDetail(e); err != nil {
		t.Fatalf("member DELETE: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("member DELETE status %d", rec.Code)
	}
	e, rec = f.event(t, http.MethodGet, "/", "", map[string]string{"pk": memberUUID})
	if err := h.MemberDetail(e); err != nil {
		t.Fatalf("member GET after delete: %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("member GET after delete status %d", rec.Code)
	}
}

// TestStatesCRUD covers create, duplicate rejection, retrieve, patch and
// soft-delete on …/projects/{project_id}/states/.
func TestStatesCRUD(t *testing.T) {
	f := newFixture(t)
	h := New(f.app)

	// Baseline state for the project so order/sequence math has content.
	mustSave(t, f.app, "states", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-0000000000c1")
		r.Set("name", "Todo")
		r.Set("color", "#fff")
		r.Set("group", "unstarted")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
		r.Set("sequence", 1)
	})

	e, rec := f.event(t, http.MethodPost, "/",
		`{"name": "In Review", "color": "#abc", "group": "started"}`, nil)
	if err := h.StatesCreate(e); err != nil {
		t.Fatalf("states POST: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("states POST status %d body %s", rec.Code, rec.Body.String())
	}
	created := decodeOne(t, rec)
	if created["name"] != "In Review" || created["group"] != "started" {
		t.Fatalf("states POST body: %s", rec.Body.String())
	}
	if created["sequence"].(float64) != 2 {
		t.Fatalf("states POST sequence must be MAX+1: %s", rec.Body.String())
	}
	if created["order"].(float64) != 1 {
		t.Fatalf("states POST order must be 1/1 within group: %s", rec.Body.String())
	}
	stateUUID, _ := created["id"].(string)

	// Same-name state is rejected (unique per project).
	e, rec = f.event(t, http.MethodPost, "/", `{"name": "In Review", "group": "started"}`, nil)
	if err := h.StatesCreate(e); err != nil {
		t.Fatalf("states POST dup: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate state: %d %s", rec.Code, rec.Body.String())
	}

	// Invalid group is a validation error.
	e, rec = f.event(t, http.MethodPost, "/", `{"name": "X", "group": "wat"}`, nil)
	if err := h.StatesCreate(e); err != nil {
		t.Fatalf("states POST bad group: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid group: %d %s", rec.Code, rec.Body.String())
	}

	e, rec = f.event(t, http.MethodGet, "/", "", map[string]string{"state_id": stateUUID})
	if err := h.StateDetail(e); err != nil {
		t.Fatalf("state GET: %v", err)
	}
	if rec.Code != http.StatusOK || decodeOne(t, rec)["name"] != "In Review" {
		t.Fatalf("state GET: %d %s", rec.Code, rec.Body.String())
	}

	e, rec = f.event(t, http.MethodPatch, "/", `{"name": "Reviewing"}`, map[string]string{"state_id": stateUUID})
	if err := h.StateDetail(e); err != nil {
		t.Fatalf("state PATCH: %v", err)
	}
	if rec.Code != http.StatusOK || decodeOne(t, rec)["name"] != "Reviewing" {
		t.Fatalf("state PATCH: %d %s", rec.Code, rec.Body.String())
	}

	// Making it the default clears the flag on the others.
	e, rec = f.event(t, http.MethodPatch, "/", `{"default": true}`, map[string]string{"state_id": stateUUID})
	if err := h.StateDetail(e); err != nil {
		t.Fatalf("state PATCH default: %v", err)
	}
	others, _ := f.app.FindRecordsByFilter("states",
		"project = {:p} && default = true && deleted_at = ''", "", 0, 0, map[string]any{"p": f.project.Id})
	if len(others) != 1 || others[0].GetString("uuid") != stateUUID {
		t.Fatalf("default flag must be exclusive: %d rows", len(others))
	}

	e, rec = f.event(t, http.MethodDelete, "/", "", map[string]string{"state_id": stateUUID})
	if err := h.StateDetail(e); err != nil {
		t.Fatalf("state DELETE: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("state DELETE status %d", rec.Code)
	}
	e, rec = f.event(t, http.MethodGet, "/", "", map[string]string{"state_id": stateUUID})
	if err := h.StateDetail(e); err != nil {
		t.Fatalf("state GET after delete: %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("state GET after delete status %d", rec.Code)
	}
}
