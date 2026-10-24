package estimate

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

// writebackFixture seeds one workspace, one project (the caller is a project
// member — Create requires ADMIN or MEMBER) and NO estimate: the project
// starts estimate-less, which is the state the write-back must change.
type writebackFixture struct {
	app     *tests.TestApp
	user    *core.Record
	ws      *core.Record
	project *core.Record
	scope   *middleware.Scope
}

func newWritebackFixture(t *testing.T) *writebackFixture {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	f := &writebackFixture{app: app}
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

	f.user = save("users", func(r *core.Record) {
		r.Set("username", "member")
		r.Set("email", "member@example.com")
		r.Set("password", "1234567890")
		r.Set("display_name", "Member")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	f.ws = save("workspaces", func(r *core.Record) {
		r.Set("name", "WS")
		r.Set("slug", "ws")
		r.Set("owner", f.user.Id)
	})
	f.project = save("projects", func(r *core.Record) {
		r.Set("name", "P")
		r.Set("identifier", "P")
		r.Set("workspace", f.ws.Id)
	})
	pjMember := save("project_members", func(r *core.Record) {
		r.Set("project", f.project.Id)
		r.Set("member", f.user.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("is_active", true)
		r.Set("role", middleware.RoleMember)
	})
	f.scope = &middleware.Scope{
		Workspace:       f.ws,
		Project:         f.project,
		WorkspaceMember: pjMember,
		ProjectMember:   pjMember,
		WorkspaceRole:   middleware.RoleMember,
		ProjectRole:     middleware.RoleMember,
	}
	return f
}

// createEstimate posts the nested body upstream's BulkEstimatePointEndpoint
// expects: {"estimate":{...},"estimate_points":[...]}.
func (f *writebackFixture) createEstimate(t *testing.T, body string) (*httptest.ResponseRecorder, error) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/estimates/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: f.app}
	e.Request = req
	e.Response = rec
	e.Auth = f.user
	e.Set("plane.scope", f.scope)
	return rec, New(f.app).Create(e)
}

func (f *writebackFixture) projectEstimate(t *testing.T) string {
	t.Helper()
	project, _ := serializers.FindByUUID(f.app, "projects", serializers.UUID(f.project))
	if project == nil {
		t.Fatal("project vanished")
	}
	return project.GetString("estimate")
}

// TestCreateAttachesEstimateToProjectWithoutOne: a project that has no
// estimate yet gets the newly created one written back — otherwise the
// frontend's `areEstimateEnabledByProjectId` gate stays closed and the whole
// estimate UI (issue sidebar row, cycle estimate-type dropdown) is invisible.
func TestCreateAttachesEstimateToProjectWithoutOne(t *testing.T) {
	f := newWritebackFixture(t)
	body := `{"estimate":{"name":"fib","type":"points"},"estimate_points":[{"key":1,"value":"1"},{"key":2,"value":"2"}]}`

	rec, err := f.createEstimate(t, body)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("create estimate: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	attached := f.projectEstimate(t)
	if attached == "" {
		t.Fatal("projects.estimate is still empty after Create — the write-back did not happen")
	}
	// Parse the response properly — the serialized map's key order is not
	// stable, so string-searching for `"id":"` can hit a point's id first.
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v (body %s)", err, rec.Body.String())
	}
	estimate, _ := serializers.FindByUUID(f.app, "estimates", created.ID)
	if estimate == nil {
		t.Fatalf("created estimate %q not found after Create", created.ID)
	}
	if attached != estimate.Id {
		t.Fatalf("projects.estimate = %q, want the newly created estimate %q", attached, estimate.Id)
	}
	if !strings.Contains(rec.Body.String(), `"name":"fib"`) {
		t.Fatalf("create response = %s, want the created estimate", rec.Body.String())
	}
}

// TestCreateKeepsExistingProjectEstimate: a project that already carries an
// estimate system keeps it — Create must not silently switch the active one
// (upstream leaves that decision to the settings switch / PATCH).
func TestCreateKeepsExistingProjectEstimate(t *testing.T) {
	f := newWritebackFixture(t)
	existing := mustSaveEstimate(t, f, "old")
	if err := setProjectEstimate(f.app, f.project.Id, existing.Id); err != nil {
		t.Fatalf("seed project estimate: %v", err)
	}

	body := `{"estimate":{"name":"fib","type":"points"},"estimate_points":[{"key":1,"value":"1"}]}`
	rec, err := f.createEstimate(t, body)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("create estimate: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := f.projectEstimate(t); got != existing.Id {
		t.Fatalf("projects.estimate = %q, want the pre-existing estimate %q (Create must not switch it)", got, existing.Id)
	}
}

func mustSaveEstimate(t *testing.T, f *writebackFixture, name string) *core.Record {
	t.Helper()
	c, err := f.app.FindCollectionByNameOrId("estimates")
	if err != nil {
		t.Fatalf("collection estimates: %v", err)
	}
	r := core.NewRecord(c)
	r.Set("uuid", serializers.NewUUID())
	r.Set("name", name)
	r.Set("type", "points")
	r.Set("project", f.project.Id)
	r.Set("workspace", f.ws.Id)
	if err := f.app.Save(r); err != nil {
		t.Fatalf("save estimate: %v", err)
	}
	return r
}

func setProjectEstimate(app core.App, projectID, estimateID string) error {
	project, err := app.FindRecordById("projects", projectID)
	if err != nil {
		return err
	}
	project.Set("estimate", estimateID)
	return app.Save(project)
}
