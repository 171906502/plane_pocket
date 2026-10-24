package hooks

import (
	"errors"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

// ---------------------------------------------------------------------------
// Probe harness
// ---------------------------------------------------------------------------

// probeGraph is the minimal Plane graph the hook probes need: workspace ->
// project -> state -> issue. Seeded with plain app.Save calls (no HTTP), which
// is exactly the situation the probes must characterise.
type probeGraph struct {
	app     core.App
	ws      *core.Record
	project *core.Record
	state   *core.Record
	issue   *core.Record
	user    *core.Record
}

func newProbeGraph(t *testing.T) *probeGraph {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

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
		r.Set("username", "probe")
		r.Set("email", "probe@example.com")
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
	state := save("states", func(r *core.Record) {
		r.Set("name", "Todo")
		r.Set("color", "#fff")
		r.Set("group", "unstarted")
		r.Set("project", project.Id)
		r.Set("workspace", ws.Id)
	})
	issue := save("issues", func(r *core.Record) {
		r.Set("name", "original")
		r.Set("project", project.Id)
		r.Set("workspace", ws.Id)
		r.Set("state", state.Id)
		r.Set("created_by", user.Id)
		r.Set("sequence_id", 1)
	})

	return &probeGraph{app: app, ws: ws, project: project, state: state, issue: issue, user: user}
}

// ---------------------------------------------------------------------------
// (a) + (b): do internal app.Save() calls fire the after-success hooks, and is
// record.Original() readable inside them?
// ---------------------------------------------------------------------------

// TestProbeInternalSaveFiresAfterUpdateHook pins premise (a): PocketBase's
// record hooks are model-level, so a plain app.Save() from a handler — no HTTP
// request involved — DOES fire OnRecordAfterUpdateSuccess.
func TestProbeInternalSaveFiresAfterUpdateHook(t *testing.T) {
	g := newProbeGraph(t)

	var updateFired, createFired int
	g.app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name == "issues" {
			updateFired++
		}
		return nil
	})
	g.app.OnRecordAfterCreateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name == "issues" {
			createFired++
		}
		return nil
	})

	g.issue.Set("name", "renamed")
	if err := g.app.Save(g.issue); err != nil {
		t.Fatalf("save: %v", err)
	}
	if updateFired != 1 {
		t.Fatalf("OnRecordAfterUpdateSuccess fired %d times for the internal app.Save, want 1", updateFired)
	}
	if createFired != 0 {
		t.Fatalf("OnRecordAfterCreateSuccess fired %d times on an update, want 0", createFired)
	}

	// A brand-new record saved internally must fire the create hook too —
	// that is what the "created the issue" activity depends on.
	c, _ := g.app.FindCollectionByNameOrId("issues")
	fresh := core.NewRecord(c)
	fresh.Set("uuid", serializers.NewUUID())
	fresh.Set("name", "fresh")
	fresh.Set("project", g.project.Id)
	fresh.Set("workspace", g.ws.Id)
	fresh.Set("created_by", g.user.Id)
	fresh.Set("sequence_id", 2)
	if err := g.app.Save(fresh); err != nil {
		t.Fatalf("save fresh: %v", err)
	}
	if createFired != 1 {
		t.Fatalf("OnRecordAfterCreateSuccess fired %d times for the internal app.Save, want 1", createFired)
	}
}

// TestProbeOriginalReadableInsideHook pins premise (b): inside the after-update
// hook, record.Original() still carries the pre-save DB state, so a diff can be
// computed without any extra query — but ONLY when the record was loaded from
// the database first. The handlers all do (findIssue -> FindByUUID -> Set ->
// Save), so the probe mirrors that flow by reloading the seeded issue.
func TestProbeOriginalReadableInsideHook(t *testing.T) {
	g := newProbeGraph(t)

	// Reload from the DB: this is what PostScan populates originalData with,
	// and it is exactly what every issue handler does before saving.
	issue, err := g.app.FindRecordById("issues", g.issue.Id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	type seen struct {
		origName  string
		origState string
		origIsNew bool
		newName   string
	}
	var got seen
	done := make(chan struct{}, 1)
	g.app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != "issues" {
			return nil
		}
		orig := e.Record.Original()
		got = seen{
			origName:  orig.GetString("name"),
			origState: orig.GetString("state"),
			origIsNew: orig.IsNew(),
			newName:   e.Record.GetString("name"),
		}
		done <- struct{}{}
		return nil
	})

	issue.Set("name", "renamed")
	if err := g.app.Save(issue); err != nil {
		t.Fatalf("save: %v", err)
	}
	<-done

	if got.origName != "original" {
		t.Errorf("Original().name = %q, want %q (pre-save DB value)", got.origName, "original")
	}
	if got.origState != g.state.Id {
		t.Errorf("Original().state = %q, want %q", got.origState, g.state.Id)
	}
	if got.origIsNew {
		t.Error("Original().IsNew() = true for a record loaded from the DB, want false")
	}
	if got.newName != "renamed" {
		t.Errorf("Record().name = %q, want %q (post-save value)", got.newName, "renamed")
	}
}

// TestProbeOriginalBlankForNewRecordBuiltRecords documents the trap behind
// premise (b): a record built with core.NewRecord and saved (never loaded from
// the DB) has a BLANK originalData, so Original() inside the hook returns
// defaults, not the just-written row. Any hook that needs old values must
// either operate on DB-loaded records or re-read them. The create path is
// immune because "created" activities carry no old values.
func TestProbeOriginalBlankForNewRecordBuiltRecords(t *testing.T) {
	g := newProbeGraph(t)

	var origName string
	done := make(chan struct{}, 1)
	g.app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != "issues" {
			return nil
		}
		origName = e.Record.Original().GetString("name")
		done <- struct{}{}
		return nil
	})

	// Same in-memory object the seeder used: never reloaded from the DB.
	g.issue.Set("name", "renamed")
	if err := g.app.Save(g.issue); err != nil {
		t.Fatalf("save: %v", err)
	}
	<-done

	if origName != "" {
		t.Errorf("Original().name = %q for a NewRecord-built record, want \"\" (blank defaults)", origName)
	}
}

// ---------------------------------------------------------------------------
// (c): does an error returned from the hook abort the main save?
// ---------------------------------------------------------------------------

// TestProbeHookErrorPropagatesToMainSave pins premise (c) — and the part that
// matters for the design: returning an error from OnRecordAfterUpdateSuccess
// makes app.Save() return that error, so the HTTP layer would answer 500 even
// though the DB write already happened. Derived-data failures must therefore be
// swallowed (log only), never returned.
func TestProbeHookErrorPropagatesToMainSave(t *testing.T) {
	g := newProbeGraph(t)

	g.app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != "issues" {
			return nil
		}
		return errors.New("derived data write failed")
	})

	g.issue.Set("name", "renamed")
	err := g.app.Save(g.issue)
	if err == nil {
		t.Fatal("app.Save returned nil although the after-update hook returned an error")
	}
	if !strings.Contains(err.Error(), "derived data write failed") {
		t.Fatalf("app.Save error = %v, want it to carry the hook error", err)
	}

	// The row was already written when the hook ran: the hook error surfaces
	// AFTER the successful UPDATE. Swallowing the error is therefore safe (the
	// main data is committed) and returning it would only fake a failure.
	reloaded, err := g.app.FindRecordById("issues", g.issue.Id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.GetString("name") != "renamed" {
		t.Errorf("stored name = %q, want %q — the hook error must not be mistaken for a rolled-back save", reloaded.GetString("name"), "renamed")
	}
}

// TestProbeHookErrorOnCreatePropagates is the create-side twin of the previous
// probe: OnRecordAfterCreateSuccess errors also bubble out of app.Save().
func TestProbeHookErrorOnCreatePropagates(t *testing.T) {
	g := newProbeGraph(t)

	g.app.OnRecordAfterCreateSuccess().BindFunc(func(e *core.RecordEvent) error {
		if e.Record.Collection().Name != "issues" {
			return nil
		}
		return errors.New("activity write failed")
	})

	c, _ := g.app.FindCollectionByNameOrId("issues")
	fresh := core.NewRecord(c)
	fresh.Set("uuid", serializers.NewUUID())
	fresh.Set("name", "fresh")
	fresh.Set("project", g.project.Id)
	fresh.Set("workspace", g.ws.Id)
	fresh.Set("created_by", g.user.Id)
	fresh.Set("sequence_id", 2)
	if err := g.app.Save(fresh); err == nil || !strings.Contains(err.Error(), "activity write failed") {
		t.Fatalf("app.Save error = %v, want it to carry the hook error", err)
	}
}

// ---------------------------------------------------------------------------
// Recursion: writing a row of an UNLISTENED collection from inside the hook
// ---------------------------------------------------------------------------

// TestProbeWritingOtherCollectionDoesNotRecurse answers the re-entrancy
// question from 11-TASKLIST §4: internal saves DO fire the hooks (premise a),
// so a hook that saved back into `issues` would recurse. But saving into a
// collection the hook does not listen to (issue_activities) fires the hook
// exactly once more — for the activity row — and stops there. No re-entrancy
// guard is needed as long as the hook never writes to a watched collection.
func TestProbeWritingOtherCollectionDoesNotRecurse(t *testing.T) {
	g := newProbeGraph(t)

	var issuesFired, activitiesFired int
	collector := func(e *core.RecordEvent) error {
		switch e.Record.Collection().Name {
		case "issues":
			issuesFired++
			// Simulate the real hook: derive one issue_activities row.
			c, err := e.App.FindCollectionByNameOrId("issue_activities")
			if err != nil {
				return err
			}
			row := core.NewRecord(c)
			row.Set("uuid", serializers.NewUUID())
			row.Set("verb", "updated")
			row.Set("field", "name")
			row.Set("issue", e.Record.Id)
			row.Set("project", g.project.Id)
			row.Set("workspace", g.ws.Id)
			if err := e.App.Save(row); err != nil {
				return err
			}
		case "issue_activities":
			activitiesFired++
		}
		return nil
	}
	g.app.OnRecordAfterUpdateSuccess().BindFunc(collector)
	g.app.OnRecordAfterCreateSuccess().BindFunc(collector)

	g.issue.Set("name", "renamed")
	if err := g.app.Save(g.issue); err != nil {
		t.Fatalf("save: %v", err)
	}
	if issuesFired != 1 {
		t.Fatalf("issues hook fired %d times, want 1 (no recursion)", issuesFired)
	}
	if activitiesFired != 1 {
		t.Fatalf("issue_activities hook fired %d times, want 1 (the derived row itself)", activitiesFired)
	}
	rows, err := g.app.FindRecordsByFilter("issue_activities", "issue = {:i}", "", 0, 0, dbx.Params{"i": g.issue.Id})
	if err != nil {
		t.Fatalf("find activities: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("stored %d activity rows, want 1", len(rows))
	}
}
