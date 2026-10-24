package view

import (
	"encoding/json"
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

// viewFixture seeds the membership ladder the saved-view guards distinguish:
// a project Admin, a plain project Member, and a workspace Admin whose project
// role is only Member (the promotion ResolveProject applies, which Django's
// destroy body re-checks against the raw role).
type viewFixture struct {
	app      *tests.TestApp
	owner    *core.Record // project Admin, owns the view
	member   *core.Record // project Member
	wsAdmin  *core.Record // workspace Admin, project Member
	ws       *core.Record
	project  *core.Record
	view     *core.Record
	stateRec *core.Record
}

func newViewFixture(t *testing.T) *viewFixture {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	f := &viewFixture{app: app}
	newUser := func(uuid, name string) *core.Record {
		return mustSave(t, app, "users", func(r *core.Record) {
			r.Set("uuid", uuid)
			r.Set("username", name)
			r.Set("email", name+"@example.com")
			r.Set("password", "1234567890")
			r.Set("last_active", "2024-01-01 00:00:00.000Z")
		})
	}
	f.owner = newUser("00000000-0000-0000-0000-0000000000e1", "owner")
	f.member = newUser("00000000-0000-0000-0000-0000000000e2", "member")
	f.wsAdmin = newUser("00000000-0000-0000-0000-0000000000e3", "wsadmin")

	f.ws = mustSave(t, app, "workspaces", func(r *core.Record) {
		r.Set("name", "WS")
		r.Set("slug", "ws")
		r.Set("owner", f.owner.Id)
	})
	f.project = mustSave(t, app, "projects", func(r *core.Record) {
		r.Set("name", "P")
		r.Set("identifier", "P")
		r.Set("workspace", f.ws.Id)
	})
	membership := func(user *core.Record, wsRole, pjRole int) {
		mustSave(t, app, "workspace_members", func(r *core.Record) {
			r.Set("workspace", f.ws.Id)
			r.Set("member", user.Id)
			r.Set("is_active", true)
			r.Set("role", wsRole)
		})
		mustSave(t, app, "project_members", func(r *core.Record) {
			r.Set("project", f.project.Id)
			r.Set("member", user.Id)
			r.Set("workspace", f.ws.Id)
			r.Set("is_active", true)
			r.Set("role", pjRole)
		})
	}
	membership(f.owner, middleware.RoleAdmin, middleware.RoleAdmin)
	membership(f.member, middleware.RoleMember, middleware.RoleMember)
	membership(f.wsAdmin, middleware.RoleAdmin, middleware.RoleMember)

	f.view = mustSave(t, app, "issue_views", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-0000000000f1")
		r.Set("name", "Owned by owner")
		r.Set("workspace", f.ws.Id)
		r.Set("project", f.project.Id)
		r.Set("owned_by", f.owner.Id)
		r.Set("created_by", f.owner.Id)
		r.Set("access", 0) // private: only the owner may see it
		r.Set("filters", map[string]any{})
		r.Set("sort_order", 65535)
	})
	return f
}

// scope mirrors what ResolveWorkspace+ResolveProject produce for the caller:
// both membership records are loaded, and ProjectRole is promoted to Admin for
// a workspace Admin who is a project member, while ProjectMember keeps the raw
// role.
func (f *viewFixture) scope(user *core.Record) *middleware.Scope {
	wsMember, _ := f.app.FindFirstRecordByFilter(
		"workspace_members",
		"workspace = {:w} && member = {:m} && is_active = true",
		map[string]any{"w": f.ws.Id, "m": user.Id},
	)
	pjMember, _ := f.app.FindFirstRecordByFilter(
		"project_members",
		"project = {:p} && member = {:m} && is_active = true",
		map[string]any{"p": f.project.Id, "m": user.Id},
	)
	wsRole, pjRole := 0, 0
	if wsMember != nil {
		wsRole = wsMember.GetInt("role")
	}
	if pjMember != nil {
		pjRole = pjMember.GetInt("role")
	}
	effective := pjRole
	if effective == 0 && wsRole >= middleware.RoleAdmin && pjMember != nil {
		effective = middleware.RoleAdmin
	}
	return &middleware.Scope{
		Workspace:       f.ws,
		Project:         f.project,
		WorkspaceMember: wsMember,
		ProjectMember:   pjMember,
		WorkspaceRole:   wsRole,
		ProjectRole:     effective,
	}
}

func (f *viewFixture) event(t *testing.T, user *core.Record, method, target, body string) (*core.RequestEvent, *httptest.ResponseRecorder) {
	t.Helper()
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetPathValue("pk", f.view.GetString("uuid"))
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: f.app}
	e.Request = req
	e.Response = rec
	e.Auth = user
	e.Set("plane.scope", f.scope(user))
	return e, rec
}

func mustSave(t *testing.T, app core.App, collection string, set func(*core.Record)) *core.Record {
	t.Helper()
	c, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		t.Fatalf("collection %s: %v", collection, err)
	}
	r := core.NewRecord(c)
	r.Set("uuid", serializers.NewUUID())
	set(r)
	if err := app.Save(r); err != nil {
		t.Fatalf("save %s: %v", collection, err)
	}
	return r
}

func errorOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		return rec.Body.String()
	}
	s, _ := out["error"].(string)
	return s
}

// TestViewUpdatePermissionLadder pins the `@allow_permission(allowed_roles=[],
// creator=True)` semantics for IssueViewViewSet.partial_update: the owner
// passes, a plain project Member is refused with Django's 403, and a workspace
// Admin who is not the owner is admitted by the decorator but rejected by the
// owner-only body check with a 400.
func TestViewUpdatePermissionLadder(t *testing.T) {
	const payload = `{"name":"renamed"}`

	// 1. Owner: 200 and the name actually changed.
	f := newViewFixture(t)
	h := New(f.app)
	e, rec := f.event(t, f.owner, http.MethodPatch, "/views/"+f.view.GetString("uuid")+"/", payload)
	if err := h.ProjectUpdate(e); err != nil {
		t.Fatalf("ProjectUpdate: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("owner: status = %d, body %s", rec.Code, rec.Body.String())
	}
	reloaded, _ := f.app.FindRecordById("issue_views", f.view.Id)
	if reloaded.GetString("name") != "renamed" {
		t.Fatalf("owner: name = %q, want \"renamed\"", reloaded.GetString("name"))
	}

	// 2. Plain project Member, not the owner: 403.
	e, rec = f.event(t, f.member, http.MethodPatch, "/views/"+f.view.GetString("uuid")+"/", payload)
	if err := h.ProjectUpdate(e); err != nil {
		t.Fatalf("ProjectUpdate: %v", err)
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member: status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	if got := errorOf(t, rec); got != "You don't have the required permissions." {
		t.Fatalf("member: error = %q", got)
	}
	reloaded, _ = f.app.FindRecordById("issue_views", f.view.Id)
	if reloaded.GetString("name") == "renamed-by-member" {
		t.Fatal("member: view was mutated")
	}

	// 3. Workspace Admin (project role Member), not the owner: 400 owner-only.
	e, rec = f.event(t, f.wsAdmin, http.MethodPatch, "/views/"+f.view.GetString("uuid")+"/", payload)
	if err := h.ProjectUpdate(e); err != nil {
		t.Fatalf("ProjectUpdate: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ws-admin: status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if got := errorOf(t, rec); got != "Only the owner of the view can update the view" {
		t.Fatalf("ws-admin: error = %q", got)
	}
}

// TestViewDestroyPermissionLadder pins
// `@allow_permission(allowed_roles=[ROLE.ADMIN], creator=True)` plus the body's
// **raw** project-role re-check: the owner and a project Admin may delete, a
// plain Member gets 403, and a workspace Admin whose project role is Member is
// admitted by the decorator yet refused by the body with a 400.
func TestViewDestroyPermissionLadder(t *testing.T) {
	f := newViewFixture(t)
	h := New(f.app)

	// 1. Plain project Member: 403, row untouched.
	e, rec := f.event(t, f.member, http.MethodDelete, "/views/"+f.view.GetString("uuid")+"/", "")
	if err := h.ProjectDestroy(e); err != nil {
		t.Fatalf("ProjectDestroy: %v", err)
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member: status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	if row, _ := f.app.FindRecordById("issue_views", f.view.Id); row.GetString("deleted_at") != "" {
		t.Fatal("member: view was deleted")
	}

	// 2. Workspace Admin, project role Member, not the owner: 400, row untouched.
	e, rec = f.event(t, f.wsAdmin, http.MethodDelete, "/views/"+f.view.GetString("uuid")+"/", "")
	if err := h.ProjectDestroy(e); err != nil {
		t.Fatalf("ProjectDestroy: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ws-admin: status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if got := errorOf(t, rec); got != "Only admin or owner can delete the view" {
		t.Fatalf("ws-admin: error = %q", got)
	}
	if row, _ := f.app.FindRecordById("issue_views", f.view.Id); row.GetString("deleted_at") != "" {
		t.Fatal("ws-admin: view was deleted")
	}

	// 3. Project Admin who is not the owner: 204 and the row is soft-deleted.
	e, rec = f.event(t, f.owner, http.MethodDelete, "/views/"+f.view.GetString("uuid")+"/", "")
	if err := h.ProjectDestroy(e); err != nil {
		t.Fatalf("ProjectDestroy: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin: status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
	if row, _ := f.app.FindRecordById("issue_views", f.view.Id); row.GetString("deleted_at") == "" {
		t.Fatal("admin: view was not soft-deleted")
	}
}

// TestViewDetailGuestDenied proves the guest branch of IssueViewViewSet.retrieve:
// a guest without guest_view_all_features asking for another user's private view
// gets the explicit 403 (not the generic 404), while the owner still gets 200.
func TestViewDetailGuestDenied(t *testing.T) {
	f := newViewFixture(t)
	h := New(f.app)

	// The owner sees the private view.
	e, rec := f.event(t, f.owner, http.MethodGet, "/views/"+f.view.GetString("uuid")+"/", "")
	if err := h.ProjectDetail(e); err != nil {
		t.Fatalf("ProjectDetail: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("owner: status = %d, body %s", rec.Code, rec.Body.String())
	}

	// A guest who is not the owner: 403 with Django's message.
	guest := mustSave(t, f.app, "users", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-0000000000e4")
		r.Set("username", "guest")
		r.Set("email", "guest@example.com")
		r.Set("password", "1234567890")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	mustSave(t, f.app, "workspace_members", func(r *core.Record) {
		r.Set("workspace", f.ws.Id)
		r.Set("member", guest.Id)
		r.Set("is_active", true)
		r.Set("role", middleware.RoleGuest)
	})
	mustSave(t, f.app, "project_members", func(r *core.Record) {
		r.Set("project", f.project.Id)
		r.Set("member", guest.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("is_active", true)
		r.Set("role", middleware.RoleGuest)
	})

	e, rec = f.event(t, guest, http.MethodGet, "/views/"+f.view.GetString("uuid")+"/", "")
	if err := h.ProjectDetail(e); err != nil {
		t.Fatalf("ProjectDetail: %v", err)
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest: status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	if got := errorOf(t, rec); got != "You are not allowed to view this issue" {
		t.Fatalf("guest: error = %q", got)
	}
}
