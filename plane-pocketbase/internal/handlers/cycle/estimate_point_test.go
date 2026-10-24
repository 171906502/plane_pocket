package cycle

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

// cycleEstimateFixture seeds a project with a points-type estimate, a cycle
// spanning yesterday..+3 days, and three issues carrying points — enough to
// pin the progress aggregates, the type=points analytics distributions and the
// points burndown.
type cycleEstimateFixture struct {
	app      *tests.TestApp
	member   *core.Record
	ws       *core.Record
	project  *core.Record
	cycle    *core.Record
	states   map[string]*core.Record
	estimate *core.Record
	// points keyed by value
	points map[int]*core.Record
	today  time.Time
}

func newCycleEstimateFixture(t *testing.T) *cycleEstimateFixture {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	f := &cycleEstimateFixture{app: app, states: map[string]*core.Record{}, points: map[int]*core.Record{}}
	f.today = time.Now().UTC().Truncate(24 * time.Hour)

	save := func(collection string, set func(*core.Record)) *core.Record {
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

	f.member = save("users", func(r *core.Record) {
		r.Set("username", "member")
		r.Set("email", "member@example.com")
		r.Set("password", "1234567890")
		r.Set("display_name", "Member")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	f.ws = save("workspaces", func(r *core.Record) {
		r.Set("name", "WS")
		r.Set("slug", "ws")
		r.Set("owner", f.member.Id)
	})
	f.project = save("projects", func(r *core.Record) {
		r.Set("name", "P")
		r.Set("identifier", "P")
		r.Set("workspace", f.ws.Id)
	})
	save("project_members", func(r *core.Record) {
		r.Set("project", f.project.Id)
		r.Set("member", f.member.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("is_active", true)
		r.Set("role", 20)
	})

	for _, group := range []string{"backlog", "unstarted", "started", "cancelled", "completed"} {
		g := group
		f.states[g] = save("states", func(r *core.Record) {
			r.Set("name", g)
			r.Set("color", "#fff")
			r.Set("group", g)
			r.Set("project", f.project.Id)
			r.Set("workspace", f.ws.Id)
		})
	}

	f.estimate = save("estimates", func(r *core.Record) {
		r.Set("name", "points")
		r.Set("type", "points")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
	})
	for _, v := range []int{1, 2, 3, 4, 5} {
		value := v
		f.points[value] = save("estimate_points", func(r *core.Record) {
			r.Set("estimate", f.estimate.Id)
			r.Set("project", f.project.Id)
			r.Set("workspace", f.ws.Id)
			r.Set("key", value)
			r.Set("value", itoa(value))
		})
	}

	f.cycle = save("cycles", func(r *core.Record) {
		r.Set("name", "Sprint")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("owned_by", f.member.Id)
		r.Set("start_date", f.today.AddDate(0, 0, -1).Format("2006-01-02 15:04:05.000Z"))
		r.Set("end_date", f.today.AddDate(0, 0, 3).Format("2006-01-02 15:04:05.000Z"))
	})
	return f
}

// addIssue creates an issue in the cycle with a point; assignee/label/completed
// are optional.
func (f *cycleEstimateFixture) addIssue(t *testing.T, group string, value int, assignee, label *core.Record, completedAt string) *core.Record {
	t.Helper()
	c, err := f.app.FindCollectionByNameOrId("issues")
	if err != nil {
		t.Fatalf("collection issues: %v", err)
	}
	issue := core.NewRecord(c)
	issue.Set("uuid", serializers.NewUUID())
	issue.Set("name", "issue-"+group+"-"+itoa(value))
	issue.Set("state", f.states[group].Id)
	issue.Set("project", f.project.Id)
	issue.Set("workspace", f.ws.Id)
	issue.Set("sequence_id", 1)
	issue.Set("sort_order", 65535)
	issue.Set("estimate_point", f.points[value].Id)
	if completedAt != "" {
		issue.Set("completed_at", completedAt)
	}
	if err := f.app.Save(issue); err != nil {
		t.Fatalf("save issue: %v", err)
	}
	if assignee != nil {
		f.bridge(t, "issue_assignees", issue, map[string]any{"assignee": assignee.Id})
	}
	if label != nil {
		f.bridge(t, "issue_labels", issue, map[string]any{"label": label.Id})
	}
	f.bridge(t, "cycle_issues", issue, map[string]any{"cycle": f.cycle.Id})
	return issue
}

func (f *cycleEstimateFixture) bridge(t *testing.T, table string, issue *core.Record, extra map[string]any) {
	t.Helper()
	c, err := f.app.FindCollectionByNameOrId(table)
	if err != nil {
		t.Fatalf("collection %s: %v", table, err)
	}
	row := core.NewRecord(c)
	row.Set("uuid", serializers.NewUUID())
	row.Set("issue", issue.Id)
	row.Set("project", f.project.Id)
	row.Set("workspace", f.ws.Id)
	for k, v := range extra {
		row.Set(k, v)
	}
	if err := f.app.Save(row); err != nil {
		t.Fatalf("save %s: %v", table, err)
	}
}

func (f *cycleEstimateFixture) get(t *testing.T, target string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.SetPathValue("cycle_id", serializers.UUID(f.cycle))
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: f.app}
	e.Request = req
	e.Response = rec
	e.Auth = f.member
	e.Set("plane.scope", &middleware.Scope{
		Workspace:     f.ws,
		Project:       f.project,
		WorkspaceRole: 20,
		ProjectRole:   20,
	})
	var handler func(*core.RequestEvent) error
	switch {
	case strings.HasSuffix(target, "/progress/"):
		handler = New(f.app).Progress
	case strings.Contains(target, "/analytics/"):
		handler = New(f.app).Analytics
	default:
		t.Fatalf("no handler for %s", target)
	}
	if err := handler(e); err != nil {
		t.Fatalf("%s: %v", target, err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status = %d, want 200 (body %s)", target, rec.Code, rec.Body.String())
	}
	out := map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s: decode %s: %v", target, rec.Body.String(), err)
	}
	return out
}

func itoa(v int) string {
	return strconv.Itoa(v)
}

// TestProgressReportsEstimatePoints pins the cycle progress endpoint's six
// `*_estimate_points` fields to the real per-state-group sums (they were
// hardcoded 0 before L-2).
func TestProgressReportsEstimatePoints(t *testing.T) {
	f := newCycleEstimateFixture(t)
	want := map[string]float64{
		"backlog_estimate_points":   1,
		"unstarted_estimate_points": 2,
		"started_estimate_points":   3,
		"cancelled_estimate_points": 4,
		"completed_estimate_points": 5,
		"total_estimate_points":     15,
	}
	for group, value := range map[string]int{
		"backlog": 1, "unstarted": 2, "started": 3, "cancelled": 4, "completed": 5,
	} {
		f.addIssue(t, group, value, nil, nil, "")
	}

	out := f.get(t, "/cycles/"+serializers.UUID(f.cycle)+"/progress/")
	for field, want := range want {
		got, ok := out[field].(float64)
		if !ok {
			t.Fatalf("progress.%s = %#v, want float64", field, out[field])
		}
		if got != want {
			t.Fatalf("progress.%s = %v, want %v", field, got, want)
		}
	}
}

// TestAnalyticsPointsDistribution pins the type=points branch: per-assignee and
// per-label estimate sums plus the points burndown chart.
func TestAnalyticsPointsDistribution(t *testing.T) {
	f := newCycleEstimateFixture(t)

	u1 := f.newUser(t, "Aa", "One")
	u2 := f.newUser(t, "Bb", "Two")
	label := f.newLabel(t, "L1", "#f00")

	// U1: 2 points completed yesterday, 5 points pending, one shared label.
	f.addIssue(t, "started", 2, u1, label, f.today.AddDate(0, 0, -1).Format("2006-01-02 15:04:05.000Z"))
	f.addIssue(t, "backlog", 5, u1, label, "")
	// U2: 3 points completed today, no label.
	f.addIssue(t, "completed", 3, u2, nil, f.today.Format("2006-01-02 15:04:05.000Z"))

	out := f.get(t, "/cycles/"+serializers.UUID(f.cycle)+"/analytics/?type=points")

	assignees, ok := out["assignees"].([]any)
	if !ok || len(assignees) != 2 {
		t.Fatalf("assignees = %#v, want 2 rows", out["assignees"])
	}
	wantAssignee := []struct {
		id                  string
		total, done, pendng float64
	}{
		{serializers.UUID(u1), 7, 2, 5},
		{serializers.UUID(u2), 3, 3, 0},
	}
	for i, want := range wantAssignee {
		row := assignees[i].(map[string]any)
		if row["assignee_id"] != want.id {
			t.Fatalf("assignees[%d].assignee_id = %v, want %s", i, row["assignee_id"], want.id)
		}
		if row["total_estimates"] != want.total || row["completed_estimates"] != want.done || row["pending_estimates"] != want.pendng {
			t.Fatalf("assignees[%d] = %#v, want total=%v completed=%v pending=%v",
				i, row, want.total, want.done, want.pendng)
		}
	}

	labels, ok := out["labels"].([]any)
	if !ok || len(labels) != 1 {
		t.Fatalf("labels = %#v, want 1 row", out["labels"])
	}
	lrow := labels[0].(map[string]any)
	if lrow["label_id"] != serializers.UUID(label) || lrow["total_estimates"] != float64(7) ||
		lrow["completed_estimates"] != float64(2) || lrow["pending_estimates"] != float64(5) {
		t.Fatalf("labels[0] = %#v, want the shared label with 7/2/5", lrow)
	}

	chart, ok := out["completion_chart"].(map[string]any)
	if !ok {
		t.Fatalf("completion_chart = %#v, want an object", out["completion_chart"])
	}
	// 10 points total; 2 completed yesterday, 3 completed today; future days null.
	wantChart := map[string]any{
		f.today.AddDate(0, 0, -1).Format("2006-01-02"): float64(8),
		f.today.Format("2006-01-02"):                   float64(5),
		f.today.AddDate(0, 0, 1).Format("2006-01-02"):  nil,
		f.today.AddDate(0, 0, 2).Format("2006-01-02"):  nil,
		f.today.AddDate(0, 0, 3).Format("2006-01-02"):  nil,
	}
	if len(chart) != len(wantChart) {
		t.Fatalf("completion_chart = %#v, want %d days", chart, len(wantChart))
	}
	for day, want := range wantChart {
		got, present := chart[day]
		if !present {
			t.Fatalf("completion_chart missing %s (got %#v)", day, chart)
		}
		if got != want {
			t.Fatalf("completion_chart[%s] = %#v, want %#v", day, got, want)
		}
	}
}

// TestAnalyticsPointsRequiresPointsEstimate: without a points-type estimate the
// type=points variant returns empty distributions (Django's estimate_type
// guard), never the issue-count fallback the L-2 card recorded as the bug.
func TestAnalyticsPointsRequiresPointsEstimate(t *testing.T) {
	f := newCycleEstimateFixture(t)
	// Downgrade the project's only estimate to "categories".
	f.estimate.Set("type", "categories")
	if err := f.app.Save(f.estimate); err != nil {
		t.Fatalf("save estimate: %v", err)
	}
	f.addIssue(t, "backlog", 1, nil, nil, "")

	out := f.get(t, "/cycles/"+serializers.UUID(f.cycle)+"/analytics/?type=points")
	if assignees, ok := out["assignees"].([]any); !ok || len(assignees) != 0 {
		t.Fatalf("assignees = %#v, want empty without a points estimate", out["assignees"])
	}
	if labels, ok := out["labels"].([]any); !ok || len(labels) != 0 {
		t.Fatalf("labels = %#v, want empty without a points estimate", out["labels"])
	}
	if chart, ok := out["completion_chart"].(map[string]any); !ok || len(chart) != 0 {
		t.Fatalf("completion_chart = %#v, want empty without a points estimate", out["completion_chart"])
	}
}

// TestAnalyticsIssuesDefaultUnchanged: without `type` (or with type=issues) the
// endpoint keeps returning the issue-count distributions.
func TestAnalyticsIssuesDefaultUnchanged(t *testing.T) {
	f := newCycleEstimateFixture(t)
	u1 := f.newUser(t, "Aa", "One")
	f.addIssue(t, "backlog", 1, u1, nil, "")

	for _, target := range []string{
		"/cycles/" + serializers.UUID(f.cycle) + "/analytics/",
		"/cycles/" + serializers.UUID(f.cycle) + "/analytics/?type=issues",
	} {
		out := f.get(t, target)
		assignees, ok := out["assignees"].([]any)
		if !ok || len(assignees) != 1 {
			t.Fatalf("%s: assignees = %#v, want 1 row", target, out["assignees"])
		}
		row := assignees[0].(map[string]any)
		if row["total_issues"] != float64(1) || row["completed_issues"] != float64(0) || row["pending_issues"] != float64(1) {
			t.Fatalf("%s: assignees[0] = %#v, want the issue-count shape", target, row)
		}
		if _, present := row["total_estimates"]; present {
			t.Fatalf("%s: assignees[0] carries total_estimates in the issues variant: %#v", target, row)
		}
	}
}

func (f *cycleEstimateFixture) newUser(t *testing.T, first, last string) *core.Record {
	t.Helper()
	c, err := f.app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("collection users: %v", err)
	}
	u := core.NewRecord(c)
	u.Set("uuid", serializers.NewUUID())
	u.Set("username", first)
	u.Set("email", first+"@example.com")
	u.Set("password", "1234567890")
	u.Set("first_name", first)
	u.Set("last_name", last)
	u.Set("display_name", first+" "+last)
	u.Set("last_active", "2024-01-01 00:00:00.000Z")
	if err := f.app.Save(u); err != nil {
		t.Fatalf("save user: %v", err)
	}
	return u
}

func (f *cycleEstimateFixture) newLabel(t *testing.T, name, color string) *core.Record {
	t.Helper()
	c, err := f.app.FindCollectionByNameOrId("labels")
	if err != nil {
		t.Fatalf("collection labels: %v", err)
	}
	l := core.NewRecord(c)
	l.Set("uuid", serializers.NewUUID())
	l.Set("name", name)
	l.Set("color", color)
	l.Set("project", f.project.Id)
	l.Set("workspace", f.ws.Id)
	if err := f.app.Save(l); err != nil {
		t.Fatalf("save label: %v", err)
	}
	return l
}
