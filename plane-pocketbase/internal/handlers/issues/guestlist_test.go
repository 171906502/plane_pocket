package issues

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

// guestFixture seeds a project whose guest member must only see issues they
// created, unless the project sets guest_view_all_features.
type guestFixture struct {
	app     *tests.TestApp
	guest   *core.Record
	other   *core.Record
	ws      *core.Record
	project *core.Record
	mine    *core.Record
	theirs  *core.Record
}

func newGuestFixture(t *testing.T, viewAll bool) *guestFixture {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	f := &guestFixture{app: app}
	f.guest = mustSave(t, app, "users", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-0000000000c1")
		r.Set("username", "guest")
		r.Set("email", "guest@example.com")
		r.Set("password", "1234567890")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	f.other = mustSave(t, app, "users", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-0000000000c2")
		r.Set("username", "member")
		r.Set("email", "member@example.com")
		r.Set("password", "1234567890")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	f.ws = mustSave(t, app, "workspaces", func(r *core.Record) {
		r.Set("name", "WS")
		r.Set("slug", "ws")
		r.Set("owner", f.other.Id)
	})
	f.project = mustSave(t, app, "projects", func(r *core.Record) {
		r.Set("name", "P")
		r.Set("identifier", "P")
		r.Set("workspace", f.ws.Id)
		r.Set("guest_view_all_features", viewAll)
	})
	// Both users are active project members; the guest holds role 5.
	mustSave(t, app, "project_members", func(r *core.Record) {
		r.Set("project", f.project.Id)
		r.Set("member", f.guest.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("is_active", true)
		r.Set("role", middleware.RoleGuest)
	})
	mustSave(t, app, "project_members", func(r *core.Record) {
		r.Set("project", f.project.Id)
		r.Set("member", f.other.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("is_active", true)
		r.Set("role", middleware.RoleMember)
	})
	state := mustSave(t, app, "states", func(r *core.Record) {
		r.Set("name", "Todo")
		r.Set("color", "#fff")
		r.Set("group", "unstarted")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
	})
	newIssue := func(uuid, name string, by *core.Record) *core.Record {
		return mustSave(t, app, "issues", func(r *core.Record) {
			r.Set("uuid", uuid)
			r.Set("name", name)
			r.Set("project", f.project.Id)
			r.Set("workspace", f.ws.Id)
			r.Set("state", state.Id)
			r.Set("created_by", by.Id)
			r.Set("sequence_id", 1)
		})
	}
	f.mine = newIssue("00000000-0000-0000-0000-0000000000d1", "mine", f.guest)
	f.theirs = newIssue("00000000-0000-0000-0000-0000000000d2", "theirs", f.other)
	return f
}

func (f *guestFixture) scope(role int) *middleware.Scope {
	return &middleware.Scope{
		Workspace:     f.ws,
		Project:       f.project,
		WorkspaceRole: role,
		ProjectRole:   role,
	}
}

// plainListEvent builds a GET /issues/list/?issues=<ids> request for the guest.
func (f *guestFixture) plainListEvent(t *testing.T, ids string) (*core.RequestEvent, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/issues/list/?issues="+ids, nil)
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: f.app}
	e.Request = req
	e.Response = rec
	e.Auth = f.guest
	e.Set("plane.scope", f.scope(middleware.RoleGuest))
	return e, rec
}

// TestPlainListGuestScope pins permissions.md §5.1 for the fetch-by-id variant:
// a guest without guest_view_all_features must not see another user's issue
// through `?issues=<id,id>` (GHSA-32c7-84jc-4w67 bypass).
func TestPlainListGuestScope(t *testing.T) {
	f := newGuestFixture(t, false)
	h := New(f.app)

	// Ask for both issues: only the guest's own must come back.
	e, rec := f.plainListEvent(t, f.mine.GetString("uuid")+","+f.theirs.GetString("uuid"))
	if err := h.PlainList(e); err != nil {
		t.Fatalf("PlainList: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	rows := decodeArray(t, rec)
	if len(rows) != 1 {
		t.Fatalf("guest saw %d issues, want 1 (body %s)", len(rows), rec.Body.String())
	}
	if got := rows[0].(map[string]any)["name"]; got != "mine" {
		t.Fatalf("guest saw %v, want \"mine\"", got)
	}

	// A foreign id alone must yield an empty array, not the record.
	e, rec = f.plainListEvent(t, f.theirs.GetString("uuid"))
	if err := h.PlainList(e); err != nil {
		t.Fatalf("PlainList: %v", err)
	}
	if rows := decodeArray(t, rec); len(rows) != 0 {
		t.Fatalf("guest saw %d foreign issues, want 0", len(rows))
	}
}

// TestPlainListGuestViewAllFeatures proves the flag widens the guest's view:
// with guest_view_all_features the same request returns both issues.
func TestPlainListGuestViewAllFeatures(t *testing.T) {
	f := newGuestFixture(t, true)
	h := New(f.app)

	e, rec := f.plainListEvent(t, f.mine.GetString("uuid")+","+f.theirs.GetString("uuid"))
	if err := h.PlainList(e); err != nil {
		t.Fatalf("PlainList: %v", err)
	}
	if rows := decodeArray(t, rec); len(rows) != 2 {
		t.Fatalf("guest saw %d issues, want 2 (body %s)", len(rows), rec.Body.String())
	}
}

// TestPlainListMemberSeesAll is the control: the restriction must not leak onto
// non-guest roles.
func TestPlainListMemberSeesAll(t *testing.T) {
	f := newGuestFixture(t, false)
	h := New(f.app)

	req := httptest.NewRequest(http.MethodGet,
		"/issues/list/?issues="+f.mine.GetString("uuid")+","+f.theirs.GetString("uuid"), nil)
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: f.app}
	e.Request = req
	e.Response = rec
	e.Auth = f.other
	e.Set("plane.scope", f.scope(middleware.RoleMember))

	if err := h.PlainList(e); err != nil {
		t.Fatalf("PlainList: %v", err)
	}
	if rows := decodeArray(t, rec); len(rows) != 2 {
		t.Fatalf("member saw %d issues, want 2 (body %s)", len(rows), rec.Body.String())
	}
}
