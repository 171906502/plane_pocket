package issues

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

// relationFixture seeds the ladder IssueRelationViewSet's guard distinguishes.
// The viewset is guarded by ProjectEntityPermission, whose non-safe branch is a
// plain `ProjectMember.role__in=[ADMIN, MEMBER]` lookup — so unlike the
// @allow_permission rows there is **no** workspace-Admin bypass, and
// `wsAdminGuest` must be refused.
type relationFixture struct {
	app          *tests.TestApp
	member       *core.Record
	guest        *core.Record
	wsAdminGuest *core.Record
	ws           *core.Record
	project      *core.Record
	issue        *core.Record
	other        *core.Record
}

func newRelationFixture(t *testing.T) *relationFixture {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	f := &relationFixture{app: app}
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
	f.member = newUser("00000000-0000-0000-0000-0000000000c1", "member")
	f.guest = newUser("00000000-0000-0000-0000-0000000000c2", "guest")
	f.wsAdminGuest = newUser("00000000-0000-0000-0000-0000000000c3", "wsadmin")

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

	state := mustSave(t, app, "states", func(r *core.Record) {
		r.Set("name", "Todo")
		r.Set("color", "#fff")
		r.Set("group", "unstarted")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
	})
	newIssue := func(uuid, name string, seq int) *core.Record {
		return mustSave(t, app, "issues", func(r *core.Record) {
			r.Set("uuid", uuid)
			r.Set("name", name)
			r.Set("project", f.project.Id)
			r.Set("workspace", f.ws.Id)
			r.Set("state", state.Id)
			r.Set("created_by", f.member.Id)
			r.Set("sequence_id", seq)
		})
	}
	f.issue = newIssue("00000000-0000-0000-0000-0000000000d1", "source", 1)
	f.other = newIssue("00000000-0000-0000-0000-0000000000d2", "target", 2)
	return f
}

func (f *relationFixture) scope(user *core.Record) *middleware.Scope {
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

func (f *relationFixture) event(t *testing.T, user *core.Record, method, target, body string) (*core.RequestEvent, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetPathValue("issue_id", f.issue.GetString("uuid"))
	req.SetPathValue("related_id", f.other.GetString("uuid"))
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: f.app}
	e.Request = req
	e.Response = rec
	e.Auth = user
	e.Set("plane.scope", f.scope(user))
	return e, rec
}

// TestRelationMutationsRequireMember pins IssueRelationViewSet's guard: the
// viewset uses ProjectEntityPermission, whose non-safe branch is
// `ProjectMember.role__in=[ADMIN, MEMBER]`. Before the fix all three mutating
// paths were unguarded, so any Guest could link or unlink work items.
func TestRelationMutationsRequireMember(t *testing.T) {
	const body = `{"relation_type":"blocking","issues":["00000000-0000-0000-0000-0000000000d2"]}`

	create := func(t *testing.T, f *relationFixture, user *core.Record) int {
		t.Helper()
		e, rec := f.event(t, user, http.MethodPost, "/issues/x/issue-relation/", body)
		if err := New(f.app).CreateRelations(e); err != nil {
			t.Fatalf("CreateRelations: %v", err)
		}
		return rec.Code
	}

	// A Guest is refused and, crucially, nothing is written.
	f := newRelationFixture(t)
	if got := create(t, f, f.guest); got != http.StatusForbidden {
		t.Fatalf("guest create: status = %d, want 403 (body %s)", got, "")
	}
	rows, _ := f.app.FindRecordsByFilter("issue_relations", "issue != ''", "", 0, 0, nil)
	if len(rows) != 0 {
		t.Fatalf("guest created %d relation rows, want 0", len(rows))
	}

	// A workspace Admin whose project role is Guest is *also* refused here:
	// ProjectEntityPermission has no bypass.
	if got := create(t, f, f.wsAdminGuest); got != http.StatusForbidden {
		t.Fatalf("ws-admin/guest-project create: status = %d, want 403", got)
	}

	// A plain Member succeeds and the row exists.
	if got := create(t, f, f.member); got != http.StatusCreated {
		t.Fatalf("member create: status = %d, want 201", got)
	}
	rows, _ = f.app.FindRecordsByFilter("issue_relations", "issue != ''", "", 0, 0, nil)
	if len(rows) != 1 {
		t.Fatalf("member created %d relation rows, want 1", len(rows))
	}

	// remove_relation (POST) and the client-only DELETE take the same floor.
	remove := func(t *testing.T, f *relationFixture, user *core.Record) int {
		t.Helper()
		e, rec := f.event(t, user, http.MethodPost, "/issues/x/remove-relation/",
			`{"related_issue":"00000000-0000-0000-0000-0000000000d2"}`)
		if err := New(f.app).RemoveRelation(e); err != nil {
			t.Fatalf("RemoveRelation: %v", err)
		}
		return rec.Code
	}
	if got := remove(t, f, f.guest); got != http.StatusForbidden {
		t.Fatalf("guest remove_relation: status = %d, want 403", got)
	}
	if got := remove(t, f, f.member); got != http.StatusNoContent {
		t.Fatalf("member remove_relation: status = %d, want 204", got)
	}

	deleteFn := func(t *testing.T, f *relationFixture, user *core.Record) int {
		t.Helper()
		e, rec := f.event(t, user, http.MethodDelete, "/issues/x/issue-relation/y/", "")
		if err := New(f.app).DeleteRelation(e); err != nil {
			t.Fatalf("DeleteRelation: %v", err)
		}
		return rec.Code
	}
	if got := deleteFn(t, f, f.guest); got != http.StatusForbidden {
		t.Fatalf("guest delete relation: status = %d, want 403", got)
	}
}
