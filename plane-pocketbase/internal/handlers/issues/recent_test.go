package issues

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/view"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// recentVisitFixture reuses the wave1-style seeding: one workspace Admin
// member, one project, a state and three issues to visit.
type recentVisitFixture struct {
	app     *tests.TestApp
	member  *core.Record
	ws      *core.Record
	project *core.Record
	issues  []*core.Record
}

func newRecentVisitFixture(t *testing.T, names ...string) *recentVisitFixture {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	f := &recentVisitFixture{app: app}
	f.member = mustSave(t, app, "users", func(r *core.Record) {
		r.Set("username", "member")
		r.Set("email", "member@example.com")
		r.Set("password", "1234567890")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
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
	mustSave(t, app, "workspace_members", func(r *core.Record) {
		r.Set("workspace", f.ws.Id)
		r.Set("member", f.member.Id)
		r.Set("is_active", true)
		r.Set("role", 20)
	})
	mustSave(t, app, "project_members", func(r *core.Record) {
		r.Set("project", f.project.Id)
		r.Set("member", f.member.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("is_active", true)
		r.Set("role", 20)
	})
	state := mustSave(t, app, "states", func(r *core.Record) {
		r.Set("name", "Todo")
		r.Set("color", "#fff")
		r.Set("group", "unstarted")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
	})
	for i, name := range names {
		f.issues = append(f.issues, mustSave(t, app, "issues", func(r *core.Record) {
			r.Set("name", name)
			r.Set("state", state.Id)
			r.Set("project", f.project.Id)
			r.Set("workspace", f.ws.Id)
			r.Set("sequence_id", i+1)
			r.Set("sort_order", 65535)
			r.Set("created_by", f.member.Id)
		}))
	}
	return f
}

// getDetail runs the issue detail GET for the given issue and asserts the
// wanted status.
func (f *recentVisitFixture) getDetail(t *testing.T, issueUUID string, want int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/issues/"+issueUUID+"/", strings.NewReader(""))
	req.SetPathValue("pk", issueUUID)
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: f.app}
	e.Request = req
	e.Response = rec
	e.Auth = f.member
	e.Set("plane.scope", f.scope())
	if err := New(f.app).Detail(e); err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if rec.Code != want {
		t.Fatalf("detail %s: status = %d, want %d (body %s)", issueUUID, rec.Code, want, rec.Body.String())
	}
	return rec
}

func (f *recentVisitFixture) scope() *middleware.Scope {
	wsMember, _ := f.app.FindFirstRecordByFilter(
		"workspace_members",
		"workspace = {:w} && member = {:m} && is_active = true",
		map[string]any{"w": f.ws.Id, "m": f.member.Id},
	)
	pjMember, _ := f.app.FindFirstRecordByFilter(
		"project_members",
		"project = {:p} && member = {:m} && is_active = true",
		map[string]any{"p": f.project.Id, "m": f.member.Id},
	)
	return &middleware.Scope{
		Workspace:       f.ws,
		Project:         f.project,
		WorkspaceMember: wsMember,
		ProjectMember:   pjMember,
		WorkspaceRole:   20,
		ProjectRole:     20,
	}
}

// visitRows returns the user_recent_visits rows for the fixture member.
func (f *recentVisitFixture) visitRows(t *testing.T) []*core.Record {
	t.Helper()
	rows, err := f.app.FindRecordsByFilter(
		"user_recent_visits",
		"user = {:user}",
		"-visited_at", 0, 0,
		dbx.Params{"user": f.member.Id},
	)
	if err != nil {
		t.Fatalf("visit rows: %v", err)
	}
	return rows
}

// recentVisitsJSON calls the read endpoint (view.RecentVisitsList) and decodes
// its plain-array body.
func (f *recentVisitFixture) recentVisitsJSON(t *testing.T) []map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/recent-visits/", strings.NewReader(""))
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: f.app}
	e.Request = req
	e.Response = rec
	e.Auth = f.member
	e.Set("plane.scope", &middleware.Scope{Workspace: f.ws, WorkspaceRole: 20})
	if err := view.New(f.app).RecentVisitsList(e); err != nil {
		t.Fatalf("RecentVisitsList: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("RecentVisitsList: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode recent-visits body: %v (%s)", err, rec.Body.String())
	}
	return out
}

// TestDetailRecordsRecentVisitsOrdered: three different issue detail GETs
// produce exactly three user_recent_visits rows, and GET …/recent-visits/
// returns them newest-first. (tasklist L-7 acceptance 1+2)
func TestDetailRecordsRecentVisitsOrdered(t *testing.T) {
	f := newRecentVisitFixture(t, "one", "two", "three")

	// Visit in a fixed order with real clock gaps so visited_at strictly
	// increases (the autodate column has millisecond precision).
	for _, issue := range f.issues {
		f.getDetail(t, serializers.UUID(issue), http.StatusOK)
		time.Sleep(10 * time.Millisecond)
	}

	rows := f.visitRows(t)
	if len(rows) != 3 {
		t.Fatalf("user_recent_visits rows = %d, want 3", len(rows))
	}
	wantOrder := []string{"three", "two", "one"} // last visited first
	for i, want := range wantOrder {
		if got := rows[i].GetString("entity_identifier"); got != serializers.UUID(f.issues[2-i]) {
			t.Fatalf("row %d entity_identifier = %s, want issue %q (%s)", i, got, want, serializers.UUID(f.issues[2-i]))
		}
		if got := rows[i].GetString("entity_name"); got != "issue" {
			t.Fatalf("row %d entity_name = %q, want issue", i, got)
		}
		if got := rows[i].GetString("project"); got != f.project.Id {
			t.Fatalf("row %d project = %q, want %q", i, got, f.project.Id)
		}
		if got := rows[i].GetString("workspace"); got != f.ws.Id {
			t.Fatalf("row %d workspace = %q, want %q", i, got, f.ws.Id)
		}
	}
	// And the wire contract: ordered by visited_at descending.
	recents := f.recentVisitsJSON(t)
	if len(recents) != 3 {
		t.Fatalf("recent-visits entries = %d, want 3", len(recents))
	}
	for i, want := range wantOrder {
		if got, _ := recents[i]["entity_name"].(string); got != "issue" {
			t.Fatalf("recent[%d].entity_name = %v, want issue", i, recents[i]["entity_name"])
		}
		id, _ := recents[i]["entity_identifier"].(string)
		if id != serializers.UUID(f.issues[2-i]) {
			t.Fatalf("recent[%d].entity_identifier = %s, want issue %q", i, id, want)
		}
		if recents[i]["entity_data"] == nil {
			t.Fatalf("recent[%d].entity_data missing — the web client filters null entity_data out", i)
		}
	}
}

// TestDetailRevisitThrottledThenRefreshed: revisiting an issue inside the 60 s
// throttle window neither adds a row nor moves visited_at; after the row is
// made stale (>60 s old) the next visit refreshes visited_at in place.
// (tasklist L-7 acceptance 3)
func TestDetailRevisitThrottledThenRefreshed(t *testing.T) {
	f := newRecentVisitFixture(t, "a", "b", "c")
	for _, issue := range f.issues {
		f.getDetail(t, serializers.UUID(issue), http.StatusOK)
		time.Sleep(10 * time.Millisecond)
	}
	if rows := f.visitRows(t); len(rows) != 3 {
		t.Fatalf("rows after first visits = %d, want 3", len(rows))
	}

	target := f.issues[0]
	targetUUID := serializers.UUID(target)
	visitedAtOf := func() string {
		rows := f.visitRows(t)
		for _, r := range rows {
			if r.GetString("entity_identifier") == targetUUID {
				return r.GetDateTime("visited_at").String()
			}
		}
		t.Fatalf("no visit row for issue %s", targetUUID)
		return ""
	}
	before := visitedAtOf()

	// Two revisits inside the throttle window: no new row, no timestamp bump.
	f.getDetail(t, targetUUID, http.StatusOK)
	f.getDetail(t, targetUUID, http.StatusOK)
	if rows := f.visitRows(t); len(rows) != 3 {
		t.Fatalf("rows after revisits = %d, want 3", len(rows))
	}
	if after := visitedAtOf(); after != before {
		t.Fatalf("revisit inside throttle moved visited_at: %s -> %s", before, after)
	}

	// Simulate >60 s staleness by backdating the row at the SQL level —
	// record-level saves cannot backdate an OnUpdate autodate field.
	stale := serializers.Now().Add(-2 * time.Minute).UTC().Format("2006-01-02 15:04:05.000Z")
	if _, err := f.app.DB().NewQuery("UPDATE user_recent_visits SET visited_at = {:at} WHERE entity_identifier = {:ident}").
		Bind(dbx.Params{"at": stale, "ident": targetUUID}).Execute(); err != nil {
		t.Fatalf("backdate visit row: %v", err)
	}

	f.getDetail(t, targetUUID, http.StatusOK)
	if rows := f.visitRows(t); len(rows) != 3 {
		t.Fatalf("rows after stale revisit = %d, want 3 (upsert, never insert)", len(rows))
	}
	if after := visitedAtOf(); after == stale {
		t.Fatalf("stale revisit did not refresh visited_at (still %s)", after)
	}
}

// TestDetailNotFoundRecordsNothing: a 404 issue fetch must not write a visit.
// (tasklist L-7 acceptance 4)
func TestDetailNotFoundRecordsNothing(t *testing.T) {
	f := newRecentVisitFixture(t, "real")
	f.getDetail(t, "00000000-0000-0000-0000-0000000000404", http.StatusNotFound)
	if rows := f.visitRows(t); len(rows) != 0 {
		t.Fatalf("404 fetch recorded %d visit rows, want 0", len(rows))
	}
}
