package hooks

import (
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// activitiesFor returns the issue_activities rows anchored to one issue,
// newest first. Shared by the activity_*_test.go files in this package.
func activitiesFor(t *testing.T, app core.App, issueID string) []*core.Record {
	t.Helper()
	rows, err := app.FindRecordsByFilter(
		"issue_activities",
		"issue = {:i} && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"i": issueID},
	)
	if err != nil {
		t.Fatalf("find activities: %v", err)
	}
	return rows
}

// findActivity returns the single row with the given field, or nil.
func findActivity(rows []*core.Record, field string) *core.Record {
	for _, r := range rows {
		if r.GetString("field") == field {
			return r
		}
	}
	return nil
}

// seedState creates an extra state in the probe graph's project.
func seedState(t *testing.T, g *probeGraph, name string) *core.Record {
	t.Helper()
	c, err := g.app.FindCollectionByNameOrId("states")
	if err != nil {
		t.Fatalf("collection states: %v", err)
	}
	r := core.NewRecord(c)
	r.Set("uuid", serializers.NewUUID())
	r.Set("name", name)
	r.Set("color", "#0f0")
	r.Set("group", "started")
	r.Set("project", g.project.Id)
	r.Set("workspace", g.ws.Id)
	if err := g.app.Save(r); err != nil {
		t.Fatalf("save state: %v", err)
	}
	return r
}

// TestActivityIssueCreated: creating an issue writes one verb="created" row
// with an empty field (the frontend renders a null field as "created the work
// item"), mirroring upstream create_issue_activity.
func TestActivityIssueCreated(t *testing.T) {
	g := newProbeGraph(t)
	Register(g.app)

	c, _ := g.app.FindCollectionByNameOrId("issues")
	issue := core.NewRecord(c)
	issue.Set("uuid", serializers.NewUUID())
	issue.Set("name", "fresh")
	issue.Set("project", g.project.Id)
	issue.Set("workspace", g.ws.Id)
	issue.Set("state", g.state.Id)
	issue.Set("created_by", g.user.Id)
	issue.Set("sequence_id", 2)
	if err := g.app.Save(issue); err != nil {
		t.Fatalf("save: %v", err)
	}

	rows := activitiesFor(t, g.app, issue.Id)
	if len(rows) != 1 {
		t.Fatalf("got %d activities, want 1", len(rows))
	}
	got := rows[0]
	if got.GetString("verb") != "created" {
		t.Errorf("verb = %q, want created", got.GetString("verb"))
	}
	if got.GetString("field") != "" {
		t.Errorf("field = %q, want empty (frontend default renderer)", got.GetString("field"))
	}
	if got.GetString("comment") != "created the issue" {
		t.Errorf("comment = %q, want %q", got.GetString("comment"), "created the issue")
	}
	if got.GetString("actor") != g.user.Id {
		t.Errorf("actor = %q, want %q (created_by)", got.GetString("actor"), g.user.Id)
	}
	if got.GetString("issue") != issue.Id || got.GetString("project") != g.project.Id {
		t.Errorf("relation columns wrong: issue=%q project=%q", got.GetString("issue"), got.GetString("project"))
	}
}

// TestActivityIssueStateChange: moving an issue between states writes one row
// with the state NAMES in old_value/new_value (what the renderer displays) and
// the state UUIDs in the identifier columns (what it links to).
func TestActivityIssueStateChange(t *testing.T) {
	g := newProbeGraph(t)
	Register(g.app)
	inProgress := seedState(t, g, "In Progress")

	issue, err := g.app.FindRecordById("issues", g.issue.Id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	issue.Set("state", inProgress.Id)
	issue.Set("updated_by", g.user.Id)
	if err := g.app.Save(issue); err != nil {
		t.Fatalf("save: %v", err)
	}

	rows := activitiesFor(t, g.app, issue.Id)
	row := findActivity(rows, "state")
	if row == nil {
		t.Fatalf("no state activity among %d rows", len(rows))
	}
	if row.GetString("old_value") != "Todo" {
		t.Errorf("old_value = %q, want Todo", row.GetString("old_value"))
	}
	if row.GetString("new_value") != "In Progress" {
		t.Errorf("new_value = %q, want In Progress", row.GetString("new_value"))
	}
	if row.GetString("old_identifier") != g.state.GetString("uuid") {
		t.Errorf("old_identifier = %q, want %q", row.GetString("old_identifier"), g.state.GetString("uuid"))
	}
	if row.GetString("new_identifier") != inProgress.GetString("uuid") {
		t.Errorf("new_identifier = %q, want %q", row.GetString("new_identifier"), inProgress.GetString("uuid"))
	}
	if row.GetString("verb") != "updated" {
		t.Errorf("verb = %q, want updated", row.GetString("verb"))
	}
}

// TestActivityIssueScalarAndDateChanges: priority/name write plain old/new
// strings; dates render as DRF DATE_FORMAT (what the rest of the API exposes).
func TestActivityIssueScalarAndDateChanges(t *testing.T) {
	g := newProbeGraph(t)
	Register(g.app)

	issue, err := g.app.FindRecordById("issues", g.issue.Id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	issue.Set("priority", "urgent")
	issue.Set("name", "renamed")
	issue.Set("start_date", "2026-10-24")
	issue.Set("updated_by", g.user.Id)
	if err := g.app.Save(issue); err != nil {
		t.Fatalf("save: %v", err)
	}

	rows := activitiesFor(t, g.app, issue.Id)
	if len(rows) != 3 {
		t.Fatalf("got %d activities, want 3 (priority/name/start_date)", len(rows))
	}
	pri := findActivity(rows, "priority")
	if pri == nil || pri.GetString("new_value") != "urgent" {
		t.Errorf("priority activity wrong: %+v", pri)
	}
	name := findActivity(rows, "name")
	if name == nil || name.GetString("old_value") != "original" || name.GetString("new_value") != "renamed" {
		t.Errorf("name activity wrong: %+v", name)
	}
	start := findActivity(rows, "start_date")
	if start == nil || start.GetString("new_value") != "2026-10-24" {
		t.Errorf("start_date activity wrong: %+v", start)
	}
}

// TestActivityIssueNoChangeNoActivity: a save that touches no whitelisted
// field (sort_order) must not write any activity row.
func TestActivityIssueNoChangeNoActivity(t *testing.T) {
	g := newProbeGraph(t)
	Register(g.app)

	issue, err := g.app.FindRecordById("issues", g.issue.Id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	issue.Set("sort_order", 42)
	issue.Set("updated_by", g.user.Id)
	if err := g.app.Save(issue); err != nil {
		t.Fatalf("save: %v", err)
	}
	if rows := activitiesFor(t, g.app, issue.Id); len(rows) != 0 {
		t.Fatalf("got %d activities, want 0 (sort_order is not whitelisted)", len(rows))
	}
}
