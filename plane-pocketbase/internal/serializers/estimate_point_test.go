package serializers

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

// estimateFixture seeds one project with the five state groups, a points-type
// estimate and a categories-type estimate, plus their points.
type estimateFixture struct {
	app     *tests.TestApp
	project *core.Record
	ws      *core.Record
	owner   *core.Record
	// state IDs keyed by group
	states map[string]*core.Record
	// pointsEstimate/categoriesEstimate
	pointsEstimate     *core.Record
	categoriesEstimate *core.Record
	// points keyed by label ("p1"…"p5", "cat")
	points map[string]*core.Record
}

func newEstimateFixture(t *testing.T) *estimateFixture {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	f := &estimateFixture{app: app, states: map[string]*core.Record{}, points: map[string]*core.Record{}}

	owner := mustSaveEst(t, app, "users", func(r *core.Record) {
		r.Set("username", "owner")
		r.Set("email", "owner@example.com")
		r.Set("password", "1234567890")
		r.Set("display_name", "Owner")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	f.owner = owner
	f.ws = mustSaveEst(t, app, "workspaces", func(r *core.Record) {
		r.Set("name", "WS")
		r.Set("slug", "ws")
		r.Set("owner", owner.Id)
	})
	f.project = mustSaveEst(t, app, "projects", func(r *core.Record) {
		r.Set("name", "P")
		r.Set("identifier", "P")
		r.Set("workspace", f.ws.Id)
	})

	for _, group := range []string{"backlog", "unstarted", "started", "cancelled", "completed"} {
		g := group
		f.states[g] = mustSaveEst(t, app, "states", func(r *core.Record) {
			r.Set("name", g)
			r.Set("color", "#fff")
			r.Set("group", g)
			r.Set("project", f.project.Id)
			r.Set("workspace", f.ws.Id)
		})
	}

	f.pointsEstimate = mustSaveEst(t, app, "estimates", func(r *core.Record) {
		r.Set("name", "fibonacci")
		r.Set("type", "points")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
	})
	f.categoriesEstimate = mustSaveEst(t, app, "estimates", func(r *core.Record) {
		r.Set("name", "tshirt")
		r.Set("type", "categories")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
	})

	// Points 1..5 on the points estimate; a "cat" point on the categories one.
	for i, key := range []string{"p1", "p2", "p3", "p4", "p5"} {
		k, v := key, string(rune('1'+i))
		f.points[k] = mustSaveEst(t, app, "estimate_points", func(r *core.Record) {
			r.Set("estimate", f.pointsEstimate.Id)
			r.Set("project", f.project.Id)
			r.Set("workspace", f.ws.Id)
			r.Set("key", i+1)
			r.Set("value", v)
		})
	}
	f.points["cat"] = mustSaveEst(t, app, "estimate_points", func(r *core.Record) {
		r.Set("estimate", f.categoriesEstimate.Id)
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("key", 1)
		r.Set("value", "99")
	})
	return f
}

// addIssue creates an issue in the given state group, optionally carrying an
// estimate point, and links it to a cycle and/or module through the bridge
// tables (pass nil to skip a bridge).
func (f *estimateFixture) addIssue(t *testing.T, group string, point *core.Record, cycle, module *core.Record, completed bool) *core.Record {
	t.Helper()
	issue := mustSaveEst(t, f.app, "issues", func(r *core.Record) {
		r.Set("name", "issue-"+group)
		r.Set("state", f.states[group].Id)
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("sequence_id", 1)
		r.Set("sort_order", 65535)
		if point != nil {
			r.Set("estimate_point", point.Id)
		}
		if completed {
			r.Set("completed_at", "2026-10-20 10:00:00.000Z")
		}
	})
	if cycle != nil {
		f.bridge(t, "cycle_issues", "cycle", cycle, issue)
	}
	if module != nil {
		f.bridge(t, "module_issues", "module", module, issue)
	}
	return issue
}

func (f *estimateFixture) bridge(t *testing.T, table, fk string, owner, issue *core.Record) {
	t.Helper()
	mustSaveEst(t, f.app, table, func(r *core.Record) {
		r.Set(fk, owner.Id)
		r.Set("issue", issue.Id)
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
	})
}

func mustSaveEst(t *testing.T, app *tests.TestApp, collection string, set func(*core.Record)) *core.Record {
	t.Helper()
	c, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		t.Fatalf("collection %s: %v", collection, err)
	}
	r := core.NewRecord(c)
	r.Set("uuid", NewUUID())
	set(r)
	if err := app.Save(r); err != nil {
		t.Fatalf("save %s: %v", collection, err)
	}
	return r
}

// TestIssuesCollectionHasEstimatePointField pins the L-2 migration: `issues`
// carries a nullable single relation to `estimate_points`.
func TestIssuesCollectionHasEstimatePointField(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	defer app.Cleanup()

	collection, err := app.FindCollectionByNameOrId("issues")
	if err != nil {
		t.Fatalf("find issues: %v", err)
	}
	field := collection.Fields.GetByName("estimate_point")
	if field == nil {
		t.Fatal("issues.estimate_point is missing — the L-2 migration did not run")
	}
	rel, ok := field.(*core.RelationField)
	if !ok {
		t.Fatalf("issues.estimate_point is %T, want *core.RelationField", field)
	}
	if rel.CollectionId == "" {
		t.Fatal("issues.estimate_point has no target collection")
	}
	target, err := app.FindCollectionByNameOrId(rel.CollectionId)
	if err != nil || target == nil {
		t.Fatalf("resolve estimate_point target %q: %v", rel.CollectionId, err)
	}
	if target.Name != "estimate_points" {
		t.Fatalf("issues.estimate_point targets %q, want estimate_points", target.Name)
	}
	if rel.Required {
		t.Fatal("issues.estimate_point must stay nullable")
	}
	if rel.CascadeDelete {
		t.Fatal("issues.estimate_point must be SET_NULL, not CASCADE")
	}
}

// TestBridgeEstimatePointsGroupsByStateGroup is the core L-2 aggregation test:
// values are summed per state group, only points of a "points" estimate count,
// and issues without a point contribute nothing.
func TestBridgeEstimatePointsGroupsByStateGroup(t *testing.T) {
	f := newEstimateFixture(t)

	cycle := mustSaveEst(t, f.app, "cycles", func(r *core.Record) {
		r.Set("name", "Sprint 1")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("owned_by", f.owner.Id)
	})

	groups := []string{"backlog", "unstarted", "started", "cancelled", "completed"}
	values := map[string]string{"backlog": "1", "unstarted": "2", "started": "3", "cancelled": "4", "completed": "5"}
	for _, g := range groups {
		f.addIssue(t, g, f.points["p"+values[g]], cycle, nil, g == "completed")
	}
	// Noise that must NOT count: a categories-estimate point, and no point.
	f.addIssue(t, "backlog", f.points["cat"], cycle, nil, false)
	f.addIssue(t, "started", nil, cycle, nil, false)

	got := BridgeEstimatePoints(f.app, "cycle_issues", "cycle", cycle.Id)
	want := EstimatePoints{Backlog: 1, Unstarted: 2, Started: 3, Cancelled: 4, Completed: 5, Total: 15}
	if got != want {
		t.Fatalf("BridgeEstimatePoints = %+v, want %+v", got, want)
	}
}

// TestBridgeEstimatePointsEmptyIsZero guards the "no data" shape: a cycle
// without any estimated issue reports zeros, not nulls.
func TestBridgeEstimatePointsEmptyIsZero(t *testing.T) {
	f := newEstimateFixture(t)
	cycle := mustSaveEst(t, f.app, "cycles", func(r *core.Record) {
		r.Set("name", "Sprint empty")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("owned_by", f.owner.Id)
	})
	f.addIssue(t, "backlog", nil, cycle, nil, false)

	got := BridgeEstimatePoints(f.app, "cycle_issues", "cycle", cycle.Id)
	if got != (EstimatePoints{}) {
		t.Fatalf("BridgeEstimatePoints = %+v, want all zeros", got)
	}
}

// TestModuleSerializerEmitsEstimatePoints asserts the module serializer's six
// `*_estimate_points` fields carry the real aggregate (they used to be
// hardcoded 0 — docs/11-TASKLIST.md §3 L-2).
func TestModuleSerializerEmitsEstimatePoints(t *testing.T) {
	f := newEstimateFixture(t)
	module := mustSaveEst(t, f.app, "modules", func(r *core.Record) {
		r.Set("name", "Module 1")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
	})

	f.addIssue(t, "backlog", f.points["p1"], nil, module, false)
	f.addIssue(t, "completed", f.points["p2"], nil, module, true)

	out := Module(f.app, module, false, false)
	for field, want := range map[string]float64{
		"backlog_estimate_points":   1,
		"completed_estimate_points": 2,
		"unstarted_estimate_points": 0,
		"started_estimate_points":   0,
		"cancelled_estimate_points": 0,
		"total_estimate_points":     3,
	} {
		got, ok := out[field].(float64)
		if !ok {
			t.Fatalf("module.%s = %#v, want float64", field, out[field])
		}
		if got != want {
			t.Fatalf("module.%s = %v, want %v", field, got, want)
		}
	}
}

// TestIssueSerializerEmitsEstimatePointUUID asserts the issue serializer emits
// the related point's Plane UUID (frontend TIssue.estimate_point: string|null)
// and null when unset.
func TestIssueSerializerEmitsEstimatePointUUID(t *testing.T) {
	f := newEstimateFixture(t)

	withPoint := f.addIssue(t, "backlog", f.points["p3"], nil, nil, false)
	if got := Issue(f.app, withPoint)["estimate_point"]; got != UUID(f.points["p3"]) {
		t.Fatalf("issue.estimate_point = %#v, want %s", got, UUID(f.points["p3"]))
	}

	withoutPoint := f.addIssue(t, "backlog", nil, nil, nil, false)
	if got := Issue(f.app, withoutPoint)["estimate_point"]; got != nil {
		t.Fatalf("issue.estimate_point = %#v, want nil", got)
	}
}
