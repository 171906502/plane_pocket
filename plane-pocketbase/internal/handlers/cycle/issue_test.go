package cycle

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

// fixture is the minimal graph for the cycle-issues filter tests: one caller
// (project admin), one workspace, one project, two states and one cycle.
// Rows are seeded before any assertion — a 200 with an empty array proves
// nothing (docs/07 §4 conclusion 4).
type fixture struct {
	app       *tests.TestApp
	user      *core.Record
	workspace *core.Record
	project   *core.Record
	states    map[string]*core.Record
	cycle     *core.Record
	scope     *middleware.Scope
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	f := &fixture{app: app, states: map[string]*core.Record{}}
	f.user = mustSave(t, app, "users", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-0000000000a1")
		r.Set("username", "me")
		r.Set("email", "me@example.com")
		r.Set("password", "1234567890")
		r.Set("display_name", "Me")
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
	for name, group := range map[string]string{
		"Todo":    "unstarted",
		"Done":    "completed",
		"Blocked": "started",
	} {
		f.states[name] = mustSave(t, app, "states", func(r *core.Record) {
			r.Set("name", name)
			r.Set("color", "#fff")
			r.Set("group", group)
			r.Set("project", f.project.Id)
			r.Set("workspace", f.workspace.Id)
		})
	}
	f.cycle = mustSave(t, app, "cycles", func(r *core.Record) {
		r.Set("name", "Sprint 1")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
		r.Set("owned_by", f.user.Id)
		r.Set("created_by", f.user.Id)
		r.Set("sort_order", 65535)
		r.Set("version", 1)
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

func (f *fixture) event(t *testing.T, method, target string, pathValues map[string]string) (*core.RequestEvent, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
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

// seedIssue links a new issue into the fixture's cycle via the cycle_issues
// bridge and returns it.
func (f *fixture) seedIssue(t *testing.T, name, priority, stateName string) *core.Record {
	t.Helper()
	issue := mustSave(t, f.app, "issues", func(r *core.Record) {
		r.Set("name", name)
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
		r.Set("state", f.states[stateName].Id)
		r.Set("created_by", f.user.Id)
		r.Set("sequence_id", 1)
		r.Set("priority", priority)
	})
	mustSave(t, f.app, "cycle_issues", func(r *core.Record) {
		r.Set("cycle", f.cycle.Id)
		r.Set("issue", issue.Id)
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
	})
	return issue
}

func listUUIDs(t *testing.T, body []byte) []string {
	t.Helper()
	out := map[string]any{}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	results, _ := out["results"].([]any)
	uuids := make([]string, 0, len(results))
	for _, item := range results {
		m, _ := item.(map[string]any)
		uuids = append(uuids, m["id"].(string))
	}
	return uuids
}

// TestIssuesListMultiValueFilters reproduces the wire calls that exposed the
// MultipleChoiceFilter gap: the active-cycle board requests
// `?priority=urgent,high` and boards pass several state UUIDs at once. A
// single-value exact match returns an EMPTY list for both — the regression
// guard asserts the union is returned instead.
func TestIssuesListMultiValueFilters(t *testing.T) {
	f := newFixture(t)
	h := New(f.app)

	urgent := f.seedIssue(t, "Urgent", "urgent", "Todo")
	high := f.seedIssue(t, "High", "high", "Blocked")
	low := f.seedIssue(t, "Low", "low", "Done")

	// Comma-separated priorities (frontend: priority: `urgent,high`).
	e, rec := f.event(t, http.MethodGet, "/?priority=urgent,high", map[string]string{"cycle_id": f.cycle.GetString("uuid")})
	if err := h.IssuesList(e); err != nil {
		t.Fatalf("priority filter: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("priority filter status %d body %s", rec.Code, rec.Body.String())
	}
	got := listUUIDs(t, rec.Body.Bytes())
	if len(got) != 2 {
		t.Fatalf("priority=urgent,high: want 2 issues, got %d (%s)", len(got), rec.Body.String())
	}
	want := map[string]bool{urgent.GetString("uuid"): true, high.GetString("uuid"): true}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("priority=urgent,high returned unexpected issue %s (%s)", id, rec.Body.String())
		}
	}

	// Single priority keeps working.
	e, rec = f.event(t, http.MethodGet, "/?priority=low", map[string]string{"cycle_id": f.cycle.GetString("uuid")})
	if err := h.IssuesList(e); err != nil {
		t.Fatalf("single priority: %v", err)
	}
	got = listUUIDs(t, rec.Body.Bytes())
	if len(got) != 1 || got[0] != low.GetString("uuid") {
		t.Fatalf("priority=low: want only %s, got %s", low.GetString("uuid"), rec.Body.String())
	}

	// Comma-separated state UUIDs resolve to the union of both states.
	e, rec = f.event(t, http.MethodGet, "/?state_id="+f.states["Todo"].GetString("uuid")+","+f.states["Blocked"].GetString("uuid"),
		map[string]string{"cycle_id": f.cycle.GetString("uuid")})
	if err := h.IssuesList(e); err != nil {
		t.Fatalf("state filter: %v", err)
	}
	got = listUUIDs(t, rec.Body.Bytes())
	if len(got) != 2 {
		t.Fatalf("state_id=x,y: want 2 issues, got %d (%s)", len(got), rec.Body.String())
	}

	// Combined filters intersect.
	e, rec = f.event(t, http.MethodGet, "/?priority=urgent,high&state_id="+f.states["Blocked"].GetString("uuid"),
		map[string]string{"cycle_id": f.cycle.GetString("uuid")})
	if err := h.IssuesList(e); err != nil {
		t.Fatalf("combined filters: %v", err)
	}
	got = listUUIDs(t, rec.Body.Bytes())
	if len(got) != 1 || got[0] != high.GetString("uuid") {
		t.Fatalf("combined: want only %s, got %s", high.GetString("uuid"), rec.Body.String())
	}
}
