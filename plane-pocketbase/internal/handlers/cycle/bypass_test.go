package cycle

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

// bypassFixture seeds the caller a CycleViewSet row has to admit:
//
//	member       project Member, workspace Member
//	guest        project Guest,  workspace Guest
//	wsAdminGuest project Guest,  workspace Admin
//
// CycleViewSet is decorated with @allow_permission, whose PROJECT branch has a
// second clause — an active project member whose *workspace* role is ADMIN
// passes regardless of their project role. A handler that only compares
// scope.ProjectRole refuses `wsAdminGuest`, which is the drift this file pins.
type bypassFixture struct {
	app          *tests.TestApp
	member       *core.Record
	guest        *core.Record
	wsAdminGuest *core.Record
	ws           *core.Record
	project      *core.Record
}

func newBypassFixture(t *testing.T) *bypassFixture {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	f := &bypassFixture{app: app}
	newUser := func(uuid, name string) *core.Record {
		return mustSave(t, app, "users", func(r *core.Record) {
			r.Set("uuid", uuid)
			r.Set("username", name)
			r.Set("email", name+"@example.com")
			r.Set("password", "1234567890")
			r.Set("display_name", name)
			r.Set("last_active", "2024-01-01 00:00:00.000Z")
		})
	}
	f.member = newUser("00000000-0000-0000-0000-0000000000e1", "member")
	f.guest = newUser("00000000-0000-0000-0000-0000000000e2", "guest")
	f.wsAdminGuest = newUser("00000000-0000-0000-0000-0000000000e3", "wsadmin")

	f.ws = mustSave(t, app, "workspaces", func(r *core.Record) {
		r.Set("name", "WS")
		r.Set("slug", "ws")
		r.Set("owner", f.member.Id)
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
	membership(f.member, middleware.RoleMember, middleware.RoleMember)
	membership(f.guest, middleware.RoleGuest, middleware.RoleGuest)
	membership(f.wsAdminGuest, middleware.RoleAdmin, middleware.RoleGuest)
	return f
}

func (f *bypassFixture) scope(user *core.Record) *middleware.Scope {
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
	return &middleware.Scope{
		Workspace:       f.ws,
		Project:         f.project,
		WorkspaceMember: wsMember,
		ProjectMember:   pjMember,
		WorkspaceRole:   wsRole,
		ProjectRole:     pjRole,
	}
}

func (f *bypassFixture) event(t *testing.T, user *core.Record, method, target, body string) (*core.RequestEvent, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: f.app}
	e.Request = req
	e.Response = rec
	e.Auth = user
	e.Set("plane.scope", f.scope(user))
	return e, rec
}

// TestCreateWorkspaceAdminBypass pins the allow_permission PROJECT branch on a
// representative inline guard: a Guest is refused, a Member is admitted, and a
// workspace Admin holding a Guest project role is admitted too.
func TestCreateWorkspaceAdminBypass(t *testing.T) {
	const payload = `{"name":"Sprint bypass"}`

	create := func(t *testing.T, f *bypassFixture, user *core.Record) int {
		t.Helper()
		e, rec := f.event(t, user, http.MethodPost, "/cycles/", payload)
		if err := New(f.app).Create(e); err != nil {
			t.Fatalf("Create: %v", err)
		}
		return rec.Code
	}

	f := newBypassFixture(t)
	if got := create(t, f, f.guest); got != http.StatusForbidden {
		t.Fatalf("guest create: status = %d, want 403", got)
	}
	rows, _ := f.app.FindRecordsByFilter("cycles", "name = 'Sprint bypass'", "", 0, 0, nil)
	if len(rows) != 0 {
		t.Fatalf("guest created %d cycles, want 0", len(rows))
	}

	if got := create(t, f, f.wsAdminGuest); got != http.StatusCreated {
		t.Fatalf("ws-admin/guest-project create: status = %d, want 201 (body %s)", got, "")
	}
	rows, _ = f.app.FindRecordsByFilter("cycles", "name = 'Sprint bypass'", "", 0, 0, nil)
	if len(rows) != 1 {
		t.Fatalf("ws-admin created %d cycles, want 1", len(rows))
	}
	if rows[0].GetString("created_by") != f.wsAdminGuest.Id {
		t.Fatalf("cycle created_by = %s, want the workspace Admin", rows[0].GetString("created_by"))
	}

	if got := create(t, f, f.member); got != http.StatusCreated {
		t.Fatalf("member create: status = %d, want 201", got)
	}
}
