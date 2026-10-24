package projects

import (
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

// estimateFixture seeds one workspace with two projects and one estimate per
// project (to exercise the cross-project rejection), plus a project-admin
// caller on project A.
type estimateFixture struct {
	app       *tests.TestApp
	user      *core.Record
	workspace *core.Record
	projectA  *core.Record
	projectB  *core.Record
	estimateA *core.Record
	estimateB *core.Record
	scopeA    *middleware.Scope
}

func newEstimateFixture(t *testing.T) *estimateFixture {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	f := &estimateFixture{app: app}
	f.user = mustSave(t, app, "users", func(r *core.Record) {
		r.Set("username", "admin")
		r.Set("email", "admin@example.com")
		r.Set("password", "1234567890")
		r.Set("display_name", "Admin")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	f.workspace = mustSave(t, app, "workspaces", func(r *core.Record) {
		r.Set("name", "WS")
		r.Set("slug", "ws")
		r.Set("owner", f.user.Id)
	})
	newProject := func(name, identifier string) *core.Record {
		return mustSave(t, app, "projects", func(r *core.Record) {
			r.Set("name", name)
			r.Set("identifier", identifier)
			r.Set("workspace", f.workspace.Id)
		})
	}
	f.projectA = newProject("A", "A")
	f.projectB = newProject("B", "B")
	member := mustSave(t, app, "project_members", func(r *core.Record) {
		r.Set("project", f.projectA.Id)
		r.Set("member", f.user.Id)
		r.Set("workspace", f.workspace.Id)
		r.Set("is_active", true)
		r.Set("role", middleware.RoleAdmin)
	})
	mustSave(t, app, "workspace_members", func(r *core.Record) {
		r.Set("workspace", f.workspace.Id)
		r.Set("member", f.user.Id)
		r.Set("is_active", true)
		r.Set("role", middleware.RoleAdmin)
	})
	newEstimate := func(p *core.Record, name string) *core.Record {
		return mustSave(t, app, "estimates", func(r *core.Record) {
			r.Set("name", name)
			r.Set("type", "points")
			r.Set("project", p.Id)
			r.Set("workspace", f.workspace.Id)
		})
	}
	f.estimateA = newEstimate(f.projectA, "fib")
	f.estimateB = newEstimate(f.projectB, "other")

	f.scopeA = &middleware.Scope{
		Workspace:       f.workspace,
		Project:         f.projectA,
		WorkspaceMember: member,
		ProjectMember:   member,
		WorkspaceRole:   middleware.RoleAdmin,
		ProjectRole:     middleware.RoleAdmin,
	}
	return f
}

func (f *estimateFixture) patchProject(t *testing.T, body string) (*httptest.ResponseRecorder, error) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/projects/"+serializers.UUID(f.projectA)+"/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: f.app}
	e.Request = req
	e.Response = rec
	e.Auth = f.user
	e.Set("plane.scope", f.scopeA)
	return rec, New(f.app).Update(e)
}

func (f *estimateFixture) reloadProjectA(t *testing.T) *core.Record {
	t.Helper()
	project, _ := serializers.FindByUUID(f.app, "projects", serializers.UUID(f.projectA))
	if project == nil {
		t.Fatal("project A vanished")
	}
	return project
}

// TestProjectsCollectionHasEstimateField: the L-11 migration must have added
// `projects.estimate` as a nullable single relation to `estimates` with
// SET_NULL (upstream: on_delete=models.SET_NULL, null=True).
func TestProjectsCollectionHasEstimateField(t *testing.T) {
	f := newEstimateFixture(t)

	collection, err := f.app.FindCollectionByNameOrId("projects")
	if err != nil {
		t.Fatalf("find projects: %v", err)
	}
	field := collection.Fields.GetByName("estimate")
	if field == nil {
		t.Fatal("projects has no `estimate` field — the L-11 migration did not apply")
	}
	relation, ok := field.(*core.RelationField)
	if !ok {
		t.Fatalf("projects.estimate is %T, want a relation field", field)
	}
	if relation.CollectionId != collectionOf(t, f.app, "estimates").Id {
		t.Fatal("projects.estimate does not point at the estimates collection")
	}
	if relation.MaxSelect != 1 {
		t.Fatalf("projects.estimate MaxSelect = %d, want 1 (single-value FK)", relation.MaxSelect)
	}
	if relation.Required {
		t.Fatal("projects.estimate is required, want nullable (Django null=True)")
	}
	if relation.CascadeDelete {
		t.Fatal("projects.estimate cascades on delete, want SET_NULL (Django on_delete=SET_NULL)")
	}
}

func collectionOf(t *testing.T, app core.App, name string) *core.Collection {
	t.Helper()
	c, err := app.FindCollectionByNameOrId(name)
	if err != nil {
		t.Fatalf("find collection %s: %v", name, err)
	}
	return c
}

// TestUpdateAcceptsSameProjectEstimate: PATCH with the project's own estimate
// stores it, answers 200, and the serialized response carries the flat UUID.
func TestUpdateAcceptsSameProjectEstimate(t *testing.T) {
	f := newEstimateFixture(t)
	body := `{"estimate":"` + serializers.UUID(f.estimateA) + `"}`

	rec, err := f.patchProject(t, body)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH estimate: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), serializers.UUID(f.estimateA)) {
		t.Fatalf("PATCH response = %s, want it to carry the estimate UUID", rec.Body.String())
	}
	if got := f.reloadProjectA(t).GetString("estimate"); got != f.estimateA.Id {
		t.Fatalf("projects.estimate = %q, want the project-A estimate id", got)
	}
	if got := serializers.Project(f.app, f.reloadProjectA(t), f.user)["estimate"]; got != serializers.UUID(f.estimateA) {
		t.Fatalf("serialized estimate = %#v, want the flat UUID %s", got, serializers.UUID(f.estimateA))
	}
}

// TestUpdateRejectsForeignProjectEstimate: an estimate belonging to another
// project is refused with 400 and leaves the project untouched.
func TestUpdateRejectsForeignProjectEstimate(t *testing.T) {
	f := newEstimateFixture(t)
	body := `{"estimate":"` + serializers.UUID(f.estimateB) + `"}`

	rec, err := f.patchProject(t, body)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cross-project PATCH: status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Estimate is not valid please pass a valid estimate_id") {
		t.Fatalf("cross-project PATCH body = %s, want the validation message", rec.Body.String())
	}
	if got := f.reloadProjectA(t).GetString("estimate"); got != "" {
		t.Fatalf("projects.estimate = %q after rejection, want it untouched", got)
	}
}

// TestUpdateRejectsUnknownEstimate: a syntactically fine but nonexistent uuid
// is refused the same way.
func TestUpdateRejectsUnknownEstimate(t *testing.T) {
	f := newEstimateFixture(t)

	rec, err := f.patchProject(t, `{"estimate":"00000000-0000-4000-8000-000000000000"}`)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown estimate PATCH: status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestUpdateClearsEstimateWithNull: the frontend's EstimateDisableSwitch
// PATCHes {"estimate": null} to turn estimates off, so an explicit null must
// clear the relation.
func TestUpdateClearsEstimateWithNull(t *testing.T) {
	f := newEstimateFixture(t)
	if _, err := f.patchProject(t, `{"estimate":"`+serializers.UUID(f.estimateA)+`"}`); err != nil {
		t.Fatalf("seed Update: %v", err)
	}

	rec, err := f.patchProject(t, `{"estimate":null}`)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("null PATCH: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := f.reloadProjectA(t).GetString("estimate"); got != "" {
		t.Fatalf("projects.estimate = %q after null PATCH, want cleared", got)
	}
	if got := serializers.Project(f.app, f.reloadProjectA(t), f.user)["estimate"]; got != nil {
		t.Fatalf("serialized estimate = %#v after null PATCH, want nil", got)
	}
}

// TestResolveProjectEstimate covers the resolver directly, including the
// non-string / empty-string cases.
func TestResolveProjectEstimate(t *testing.T) {
	f := newEstimateFixture(t)

	if id, err := resolveProjectEstimate(f.app, f.projectA.Id, serializers.UUID(f.estimateA)); err != nil || id != f.estimateA.Id {
		t.Fatalf("same-project estimate: (%q, %v), want (%q, nil)", id, err, f.estimateA.Id)
	}
	if id, err := resolveProjectEstimate(f.app, f.projectA.Id, serializers.UUID(f.estimateB)); err == nil {
		t.Fatalf("foreign estimate: (%q, nil), want an error", id)
	}
	if id, err := resolveProjectEstimate(f.app, f.projectA.Id, nil); err != nil || id != "" {
		t.Fatalf("nil value: (%q, %v), want (\"\", nil) — null clears", id, err)
	}
	if id, err := resolveProjectEstimate(f.app, f.projectA.Id, ""); err != nil || id != "" {
		t.Fatalf("empty string: (%q, %v), want (\"\", nil)", id, err)
	}
	if id, err := resolveProjectEstimate(f.app, f.projectA.Id, 42); err == nil {
		t.Fatalf("non-string value: (%q, nil), want an error", id)
	}
}
