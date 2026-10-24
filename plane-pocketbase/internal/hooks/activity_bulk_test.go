package hooks

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// TestActivityBulkUpdateWritesOneSummary: POST bulk-operation-issues saves N
// issues in one request loop, each going through the same hook. Without
// coalescing that is N activity rows; the burst detector must keep exactly one
// summary row (the first of the batch) per (actor, project, field, new_value).
func TestActivityBulkUpdateWritesOneSummary(t *testing.T) {
	g := newProbeGraph(t)
	Register(g.app)

	// Three more issues, all in the probe state — like a bulk selection.
	issues := []*core.Record{g.issue}
	for i := 2; i <= 4; i++ {
		issues = append(issues, seedIssue(t, g, "bulk", i))
	}

	for _, issue := range issues {
		rec, err := g.app.FindRecordById("issues", issue.Id)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		rec.Set("priority", "urgent")
		rec.Set("updated_by", g.user.Id)
		if err := g.app.Save(rec); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	total := 0
	for _, issue := range issues {
		rows := activitiesFor(t, g.app, issue.Id)
		for _, r := range rows {
			if r.GetString("field") == "priority" {
				total++
			}
		}
	}
	if total != 1 {
		t.Fatalf("bulk update produced %d priority activities across %d issues, want exactly 1 summary", total, len(issues))
	}
}

// TestActivityBulkSuppressionScope pins the coalescing key semantics: an
// identical (actor, project, field, new_value) transition inside the window is
// one row (the bulk case), while a DIFFERENT target value is a different key
// and must never be swallowed.
func TestActivityBulkSuppressionScope(t *testing.T) {
	g := newProbeGraph(t)
	Register(g.app)

	issue1 := g.issue
	issue2 := seedIssue(t, g, "second", 2)
	issue3 := seedIssue(t, g, "third", 3)

	setPriority := func(issue *core.Record, value string) {
		t.Helper()
		rec, err := g.app.FindRecordById("issues", issue.Id)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		rec.Set("priority", value)
		rec.Set("updated_by", g.user.Id)
		if err := g.app.Save(rec); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	setPriority(issue1, "high")   // first of the burst: recorded
	setPriority(issue2, "high")   // identical key inside the window: suppressed
	setPriority(issue3, "urgent") // different new_value: a different key, recorded

	burst := countField(activitiesFor(t, g.app, issue1.Id), "priority") +
		countField(activitiesFor(t, g.app, issue2.Id), "priority")
	if burst != 1 {
		t.Fatalf("identical transitions across two issues produced %d priority activities, want 1", burst)
	}
	if n := countField(activitiesFor(t, g.app, issue3.Id), "priority"); n != 1 {
		t.Fatalf("distinct transition produced %d priority activities, want 1 (must not be swallowed)", n)
	}
}

// TestActivityDerivedWriteFailureDoesNotFailMainSave: the L-1 hard constraint.
// When the issue_activities collection cannot even be resolved, the derived
// write is logged and swallowed — the issue save itself must still succeed.
func TestActivityDerivedWriteFailureDoesNotFailMainSave(t *testing.T) {
	g := newProbeGraph(t)
	Register(g.app)

	// Simulate a broken derived store: rename the collection out from under
	// the hook. writeActivity's FindCollectionByNameOrId then fails and the
	// error must be logged, not returned.
	collection, err := g.app.FindCollectionByNameOrId("issue_activities")
	if err != nil {
		t.Fatalf("find collection: %v", err)
	}
	collection.Name = "issue_activities_disabled"
	if err := g.app.Save(collection); err != nil {
		t.Fatalf("rename collection: %v", err)
	}

	issue, err := g.app.FindRecordById("issues", g.issue.Id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	issue.Set("name", "still saved")
	issue.Set("updated_by", g.user.Id)
	if err := g.app.Save(issue); err != nil {
		t.Fatalf("main save failed although only the derived write is broken: %v", err)
	}

	reloaded, err := g.app.FindRecordById("issues", g.issue.Id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.GetString("name") != "still saved" {
		t.Errorf("stored name = %q, want %q", reloaded.GetString("name"), "still saved")
	}
}

// countField counts rows with the given field value.
func countField(rows []*core.Record, field string) int {
	n := 0
	for _, r := range rows {
		if r.GetString("field") == field {
			n++
		}
	}
	return n
}
