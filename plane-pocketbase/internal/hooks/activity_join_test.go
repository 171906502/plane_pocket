package hooks

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// seedTaxonomy creates the extra graph pieces the join-table tests need: a
// second user, a label, a cycle and a module — all inside the probe project.
func seedTaxonomy(t *testing.T, g *probeGraph) (user *core.Record, label, cycle, module *core.Record) {
	t.Helper()
	newRec := func(collection string, set func(*core.Record)) *core.Record {
		t.Helper()
		c, err := g.app.FindCollectionByNameOrId(collection)
		if err != nil {
			t.Fatalf("collection %s: %v", collection, err)
		}
		r := core.NewRecord(c)
		r.Set("uuid", serializers.NewUUID())
		set(r)
		if err := g.app.Save(r); err != nil {
			t.Fatalf("save %s: %v", collection, err)
		}
		return r
	}

	user = newRec("users", func(r *core.Record) {
		r.Set("username", "mate")
		r.Set("email", "mate@example.com")
		r.Set("password", "1234567890")
		r.Set("display_name", "Mate")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	label = newRec("labels", func(r *core.Record) {
		r.Set("name", "bug")
		r.Set("color", "#f00")
		r.Set("project", g.project.Id)
		r.Set("workspace", g.ws.Id)
	})
	cycle = newRec("cycles", func(r *core.Record) {
		r.Set("name", "Sprint 1")
		r.Set("owned_by", g.user.Id)
		r.Set("project", g.project.Id)
		r.Set("workspace", g.ws.Id)
	})
	module = newRec("modules", func(r *core.Record) {
		r.Set("name", "Payments")
		r.Set("project", g.project.Id)
		r.Set("workspace", g.ws.Id)
	})
	return user, label, cycle, module
}

// TestActivityAssigneeAdded: creating an issue_assignees bridge row writes
// field="assignees" with the user's display_name (rendered) and the user's
// Plane UUID in new_identifier (linked), mirroring track_assignees' added leg.
func TestActivityAssigneeAdded(t *testing.T) {
	g := newProbeGraph(t)
	Register(g.app)
	mate, _, _, _ := seedTaxonomy(t, g)

	c, _ := g.app.FindCollectionByNameOrId("issue_assignees")
	row := core.NewRecord(c)
	row.Set("uuid", serializers.NewUUID())
	row.Set("issue", g.issue.Id)
	row.Set("assignee", mate.Id)
	row.Set("project", g.project.Id)
	row.Set("workspace", g.ws.Id)
	row.Set("created_by", g.user.Id)
	if err := g.app.Save(row); err != nil {
		t.Fatalf("save: %v", err)
	}

	rows := activitiesFor(t, g.app, g.issue.Id)
	got := findActivity(rows, "assignees")
	if got == nil {
		t.Fatalf("no assignees activity among %d rows", len(rows))
	}
	if got.GetString("old_value") != "" || got.GetString("new_value") != "Mate" {
		t.Errorf("values = (%q, %q), want (\"\", \"Mate\")", got.GetString("old_value"), got.GetString("new_value"))
	}
	if got.GetString("new_identifier") != mate.GetString("uuid") {
		t.Errorf("new_identifier = %q, want %q", got.GetString("new_identifier"), mate.GetString("uuid"))
	}
	if got.GetString("verb") != "updated" {
		t.Errorf("verb = %q, want updated", got.GetString("verb"))
	}
}

// TestActivityLabelAdded: creating an issue_labels bridge row writes
// field="labels" with the label name and UUID.
func TestActivityLabelAdded(t *testing.T) {
	g := newProbeGraph(t)
	Register(g.app)
	_, label, _, _ := seedTaxonomy(t, g)

	c, _ := g.app.FindCollectionByNameOrId("issue_labels")
	row := core.NewRecord(c)
	row.Set("uuid", serializers.NewUUID())
	row.Set("issue", g.issue.Id)
	row.Set("label", label.Id)
	row.Set("project", g.project.Id)
	row.Set("workspace", g.ws.Id)
	row.Set("created_by", g.user.Id)
	if err := g.app.Save(row); err != nil {
		t.Fatalf("save: %v", err)
	}

	got := findActivity(activitiesFor(t, g.app, g.issue.Id), "labels")
	if got == nil {
		t.Fatal("no labels activity")
	}
	if got.GetString("new_value") != "bug" {
		t.Errorf("new_value = %q, want bug", got.GetString("new_value"))
	}
	if got.GetString("new_identifier") != label.GetString("uuid") {
		t.Errorf("new_identifier = %q, want %q", got.GetString("new_identifier"), label.GetString("uuid"))
	}
}

// TestActivityModuleLinked: creating a module_issues bridge row writes
// verb="created", field="modules" with the module name and UUID — the shape
// the module renderer's "added this work item to the module" branch reads.
func TestActivityModuleLinked(t *testing.T) {
	g := newProbeGraph(t)
	Register(g.app)
	_, _, _, module := seedTaxonomy(t, g)

	c, _ := g.app.FindCollectionByNameOrId("module_issues")
	row := core.NewRecord(c)
	row.Set("uuid", serializers.NewUUID())
	row.Set("issue", g.issue.Id)
	row.Set("module", module.Id)
	row.Set("project", g.project.Id)
	row.Set("workspace", g.ws.Id)
	row.Set("created_by", g.user.Id)
	if err := g.app.Save(row); err != nil {
		t.Fatalf("save: %v", err)
	}

	got := findActivity(activitiesFor(t, g.app, g.issue.Id), "modules")
	if got == nil {
		t.Fatal("no modules activity")
	}
	if got.GetString("verb") != "created" {
		t.Errorf("verb = %q, want created", got.GetString("verb"))
	}
	if got.GetString("new_value") != "Payments" {
		t.Errorf("new_value = %q, want Payments", got.GetString("new_value"))
	}
	if got.GetString("new_identifier") != module.GetString("uuid") {
		t.Errorf("new_identifier = %q, want %q", got.GetString("new_identifier"), module.GetString("uuid"))
	}
}

// TestActivityCycleLinkLifecycle walks a cycle bridge row through its three
// shapes: linked (verb=created), moved to another cycle (verb=updated with both
// names), and removed via soft delete (verb=deleted) — mirroring upstream
// create_/update_/delete_cycle_issue_activity.
func TestActivityCycleLinkLifecycle(t *testing.T) {
	g := newProbeGraph(t)
	Register(g.app)
	_, _, cycle, _ := seedTaxonomy(t, g)
	otherCycle := seedCycle(t, g, "Sprint 2")

	c, _ := g.app.FindCollectionByNameOrId("cycle_issues")
	link := core.NewRecord(c)
	link.Set("uuid", serializers.NewUUID())
	link.Set("issue", g.issue.Id)
	link.Set("cycle", cycle.Id)
	link.Set("project", g.project.Id)
	link.Set("workspace", g.ws.Id)
	link.Set("created_by", g.user.Id)
	if err := g.app.Save(link); err != nil {
		t.Fatalf("save link: %v", err)
	}

	if got := findActivity(activitiesFor(t, g.app, g.issue.Id), "cycles"); got == nil || got.GetString("verb") != "created" || got.GetString("new_value") != "Sprint 1" {
		t.Errorf("linked activity wrong: %+v", got)
	}

	// Reload from the DB before each mutation: the real handlers do
	// find -> set -> save, and only a DB-loaded record carries the old value in
	// Original() (06-PITFALLS.md D-12 premise 2).
	link, err := g.app.FindRecordById("cycle_issues", link.Id)
	if err != nil {
		t.Fatalf("reload link: %v", err)
	}
	link.Set("cycle", otherCycle.Id)
	link.Set("updated_by", g.user.Id)
	if err := g.app.Save(link); err != nil {
		t.Fatalf("save move: %v", err)
	}

	moved := findActivity(activitiesFor(t, g.app, g.issue.Id), "cycles")
	if moved == nil || moved.GetString("verb") != "updated" {
		t.Fatalf("moved activity wrong: %+v", moved)
	}
	if moved.GetString("old_value") != "Sprint 1" || moved.GetString("new_value") != "Sprint 2" {
		t.Errorf("moved values = (%q, %q), want (Sprint 1, Sprint 2)", moved.GetString("old_value"), moved.GetString("new_value"))
	}
	if moved.GetString("old_identifier") != cycle.GetString("uuid") || moved.GetString("new_identifier") != otherCycle.GetString("uuid") {
		t.Errorf("moved identifiers wrong: %+v", moved)
	}

	link, err = g.app.FindRecordById("cycle_issues", link.Id)
	if err != nil {
		t.Fatalf("reload link: %v", err)
	}
	link.Set("deleted_at", serializers.Now())
	if err := g.app.Save(link); err != nil {
		t.Fatalf("save remove: %v", err)
	}

	removed := findActivity(activitiesFor(t, g.app, g.issue.Id), "cycles")
	if removed == nil || removed.GetString("verb") != "deleted" {
		t.Fatalf("removed activity wrong: %+v", removed)
	}
	if removed.GetString("old_value") != "Sprint 2" || removed.GetString("new_value") != "" {
		t.Errorf("removed values = (%q, %q), want (Sprint 2, \"\")", removed.GetString("old_value"), removed.GetString("new_value"))
	}
}

// seedCycle creates a cycle owned by the probe user.
func seedCycle(t *testing.T, g *probeGraph, name string) *core.Record {
	t.Helper()
	c, err := g.app.FindCollectionByNameOrId("cycles")
	if err != nil {
		t.Fatalf("collection cycles: %v", err)
	}
	r := core.NewRecord(c)
	r.Set("uuid", serializers.NewUUID())
	r.Set("name", name)
	r.Set("owned_by", g.user.Id)
	r.Set("project", g.project.Id)
	r.Set("workspace", g.ws.Id)
	if err := g.app.Save(r); err != nil {
		t.Fatalf("save cycle: %v", err)
	}
	return r
}
