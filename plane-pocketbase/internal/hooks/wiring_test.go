package hooks_test

import (
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/plane-pocketbase/plane-pocketbase/internal/router"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

// TestWiringRouterRegisterProducesActivities is the L-1 reverse-verification
// target: it goes through the PRODUCTION registration path (router.Register,
// which is what main.go calls), not hooks.Register directly. Commenting the
// hooks.Register(app) line in internal/router/router.go must make this test
// FAIL; restoring it must make it PASS.
//
// The scenario is the minimal end-to-end slice of the L-1 contract: create an
// issue (verb="created") and change one property (field="state" with the state
// names), both driven by plain app.Save calls — the same calls the handlers
// make.
func TestWiringRouterRegisterProducesActivities(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	defer app.Cleanup()

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

	user := save("users", func(r *core.Record) {
		r.Set("username", "wiring")
		r.Set("email", "wiring@example.com")
		r.Set("password", "1234567890")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	ws := save("workspaces", func(r *core.Record) {
		r.Set("name", "WS")
		r.Set("slug", "ws")
		r.Set("owner", user.Id)
	})
	project := save("projects", func(r *core.Record) {
		r.Set("name", "P")
		r.Set("identifier", "P")
		r.Set("workspace", ws.Id)
	})
	todo := save("states", func(r *core.Record) {
		r.Set("name", "Todo")
		r.Set("color", "#fff")
		r.Set("group", "unstarted")
		r.Set("project", project.Id)
		r.Set("workspace", ws.Id)
	})
	doing := save("states", func(r *core.Record) {
		r.Set("name", "In Progress")
		r.Set("color", "#0f0")
		r.Set("group", "started")
		r.Set("project", project.Id)
		r.Set("workspace", ws.Id)
	})

	// The production wiring: exactly what main.go runs.
	router.Register(app)

	issue := save("issues", func(r *core.Record) {
		r.Set("name", "wired")
		r.Set("project", project.Id)
		r.Set("workspace", ws.Id)
		r.Set("state", todo.Id)
		r.Set("created_by", user.Id)
		r.Set("sequence_id", 1)
	})

	reloaded, err := app.FindRecordById("issues", issue.Id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	reloaded.Set("state", doing.Id)
	reloaded.Set("updated_by", user.Id)
	if err := app.Save(reloaded); err != nil {
		t.Fatalf("save update: %v", err)
	}

	rows, err := app.FindRecordsByFilter(
		"issue_activities",
		"issue = {:i} && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"i": issue.Id},
	)
	if err != nil {
		t.Fatalf("find activities: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("router.Register wiring produced %d activities, want 2 (created + state change)", len(rows))
	}

	byField := map[string]*core.Record{}
	for _, r := range rows {
		byField[r.GetString("field")] = r
	}
	created, ok := byField[""]
	if !ok || created.GetString("verb") != "created" {
		t.Errorf("missing verb=created activity: %+v", byField[""])
	}
	state, ok := byField["state"]
	if !ok {
		t.Fatalf("missing field=state activity: %+v", rows)
	}
	if state.GetString("old_value") != "Todo" || state.GetString("new_value") != "In Progress" {
		t.Errorf("state activity values = (%q, %q), want (Todo, In Progress)",
			state.GetString("old_value"), state.GetString("new_value"))
	}
}
