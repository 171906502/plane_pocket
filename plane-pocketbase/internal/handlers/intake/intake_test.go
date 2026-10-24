package intake

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

// fixture is the minimal inbox graph: one admin caller, one project, its
// default intake, two states, and intake issues seeded per test.
type fixture struct {
	app       *tests.TestApp
	user      *core.Record
	peer      *core.Record
	workspace *core.Record
	project   *core.Record
	states    map[string]*core.Record
	intake    *core.Record
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
	for name, group := range map[string]string{
		"Triage": "triage",
		"Todo":   "unstarted",
	} {
		f.states[name] = mustSave(t, app, "states", func(r *core.Record) {
			r.Set("name", name)
			r.Set("color", "#fff")
			r.Set("group", group)
			r.Set("project", f.project.Id)
			r.Set("workspace", f.workspace.Id)
		})
	}
	f.intake = mustSave(t, app, "intakes", func(r *core.Record) {
		r.Set("name", "Inbox")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
		r.Set("created_by", f.user.Id)
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

func (f *fixture) event(t *testing.T, method, target string) (*core.RequestEvent, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
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

// seedIntakeIssue creates an issue in the given state/priority plus its pending
// intake_issues row.
func (f *fixture) seedIntakeIssue(t *testing.T, name, priority, stateName string) *core.Record {
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
	row := mustSave(t, f.app, "intake_issues", func(r *core.Record) {
		r.Set("intake", f.intake.Id)
		r.Set("issue", issue.Id)
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
		r.Set("status", -2)
		r.Set("source", "in-app")
	})
	_ = row
	return issue
}

// intakeIssueIDs maps the paginated envelope results to issue UUIDs.
func intakeIssueIDs(t *testing.T, body []byte) []string {
	t.Helper()
	out := map[string]any{}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	results, _ := out["results"].([]any)
	ids := make([]string, 0, len(results))
	for _, item := range results {
		m, _ := item.(map[string]any)
		issue, _ := m["issue"].(map[string]any)
		ids = append(ids, issue["id"].(string))
	}
	return ids
}

// TestIssuesListFilters covers the inbox filter bar wire contract: comma-
// separated priority/state/created_by on the related issue, assignees through
// the bridge table, and the `issue__created_at` sorting keys.
func TestIssuesListFilters(t *testing.T) {
	f := newFixture(t)
	h := New(f.app)

	urgent := f.seedIntakeIssue(t, "Urgent", "urgent", "Triage")
	high := f.seedIntakeIssue(t, "High", "high", "Todo")
	medium := f.seedIntakeIssue(t, "Medium", "medium", "Triage")

	// Assign `urgent` to the peer through the bridge table.
	mustSave(t, f.app, "issue_assignees", func(r *core.Record) {
		r.Set("issue", urgent.Id)
		r.Set("assignee", f.peer.Id)
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
	})

	fetch := func(t *testing.T, rawQuery string) []string {
		t.Helper()
		e, rec := f.event(t, http.MethodGet, "/?"+rawQuery)
		if err := h.IssuesList(e); err != nil {
			t.Fatalf("list %s: %v", rawQuery, err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("list %s status %d body %s", rawQuery, rec.Code, rec.Body.String())
		}
		return intakeIssueIDs(t, rec.Body.Bytes())
	}
	idOf := func(rec *core.Record) string { return rec.GetString("uuid") }

	// Default: only pending (-2).
	got := fetch(t, "")
	if len(got) != 3 {
		t.Fatalf("default status: want 3, got %v", got)
	}

	// Comma-separated priorities.
	got = fetch(t, "priority=urgent,high")
	if len(got) != 2 || contains(got, idOf(medium)) {
		t.Fatalf("priority=urgent,high: want 2 without Medium, got %v", got)
	}

	// State filter resolves UUIDs against the related issue.
	got = fetch(t, "state="+f.states["Todo"].GetString("uuid"))
	if len(got) != 1 || got[0] != idOf(high) {
		t.Fatalf("state=Todo: want only High, got %v", got)
	}

	// Assignees resolve through the bridge table.
	got = fetch(t, "assignees="+f.peer.GetString("uuid"))
	if len(got) != 1 || got[0] != idOf(urgent) {
		t.Fatalf("assignees=peer: want only Urgent, got %v", got)
	}

	// Sorting wire values map onto the related issue's fields.
	got = fetch(t, "order_by=-issue__sequence_id")
	if len(got) != 3 {
		t.Fatalf("order_by issue__sequence_id: want 3, got %v", got)
	}

	// Date lower bound on the submission.
	got = fetch(t, "created_at=2099-01-01")
	if len(got) != 0 {
		t.Fatalf("created_at=2099-01-01: want 0, got %v", got)
	}
	got = fetch(t, "created_at=2000-01-01")
	if len(got) != 3 {
		t.Fatalf("created_at=2000-01-01: want 3, got %v", got)
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
