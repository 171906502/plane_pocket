package issues

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

// estimatePointFixture seeds two projects in one workspace (to exercise the
// cross-project rejection), a member, states and one estimate + points per
// project.
type estimatePointFixture struct {
	app      *tests.TestApp
	member   *core.Record
	ws       *core.Record
	projectA *core.Record
	projectB *core.Record
	stateA   *core.Record
	pointA   *core.Record
	pointB   *core.Record
	issueA   *core.Record
}

func newEstimatePointFixture(t *testing.T) *estimatePointFixture {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	f := &estimatePointFixture{app: app}
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
	newProject := func(name, identifier string) *core.Record {
		return save("projects", func(r *core.Record) {
			r.Set("name", name)
			r.Set("identifier", identifier)
			r.Set("workspace", f.ws.Id)
		})
	}
	f.projectA = newProject("A", "A")
	f.projectB = newProject("B", "B")
	save("workspace_members", func(r *core.Record) {
		r.Set("workspace", f.ws.Id)
		r.Set("member", f.member.Id)
		r.Set("is_active", true)
		r.Set("role", 20)
	})
	for _, p := range []*core.Record{f.projectA, f.projectB} {
		save("project_members", func(r *core.Record) {
			r.Set("project", p.Id)
			r.Set("member", f.member.Id)
			r.Set("workspace", f.ws.Id)
			r.Set("is_active", true)
			r.Set("role", 20)
		})
	}

	newState := func(p *core.Record) *core.Record {
		return save("states", func(r *core.Record) {
			r.Set("name", "Todo-"+p.GetString("identifier"))
			r.Set("color", "#fff")
			r.Set("group", "unstarted")
			r.Set("project", p.Id)
			r.Set("workspace", f.ws.Id)
		})
	}
	f.stateA = newState(f.projectA)

	newPoint := func(p *core.Record, value string) *core.Record {
		estimate := save("estimates", func(r *core.Record) {
			r.Set("name", "est-"+p.GetString("identifier"))
			r.Set("type", "points")
			r.Set("project", p.Id)
			r.Set("workspace", f.ws.Id)
		})
		return save("estimate_points", func(r *core.Record) {
			r.Set("estimate", estimate.Id)
			r.Set("project", p.Id)
			r.Set("workspace", f.ws.Id)
			r.Set("key", 1)
			r.Set("value", value)
		})
	}
	f.pointA = newPoint(f.projectA, "3")
	f.pointB = newPoint(f.projectB, "8")

	f.issueA = save("issues", func(r *core.Record) {
		r.Set("name", "issue A")
		r.Set("state", f.stateA.Id)
		r.Set("project", f.projectA.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("sequence_id", 1)
		r.Set("sort_order", 65535)
		r.Set("created_by", f.member.Id)
	})
	return f
}

func (f *estimatePointFixture) scope() *middleware.Scope {
	wsMember, _ := f.app.FindFirstRecordByFilter(
		"workspace_members",
		"workspace = {:w} && member = {:m} && is_active = true",
		map[string]any{"w": f.ws.Id, "m": f.member.Id},
	)
	pjMember, _ := f.app.FindFirstRecordByFilter(
		"project_members",
		"project = {:p} && member = {:m} && is_active = true",
		map[string]any{"p": f.projectA.Id, "m": f.member.Id},
	)
	return &middleware.Scope{
		Workspace:       f.ws,
		Project:         f.projectA,
		WorkspaceMember: wsMember,
		ProjectMember:   pjMember,
		WorkspaceRole:   20,
		ProjectRole:     20,
	}
}

func (f *estimatePointFixture) event(t *testing.T, method, target, body string) (*core.RequestEvent, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: f.app}
	e.Request = req
	e.Response = rec
	e.Auth = f.member
	e.Set("plane.scope", f.scope())
	return e, rec
}

func (f *estimatePointFixture) patchIssue(t *testing.T, body string) (*httptest.ResponseRecorder, error) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/issues/"+serializers.UUID(f.issueA)+"/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("pk", serializers.UUID(f.issueA))
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: f.app}
	e.Request = req
	e.Response = rec
	e.Auth = f.member
	e.Set("plane.scope", f.scope())
	return rec, New(f.app).Update(e)
}

// TestUpdateAcceptsEstimatePoint: PATCH with a same-project point stores it and
// the serializer projects the point's UUID.
func TestUpdateAcceptsEstimatePoint(t *testing.T) {
	f := newEstimatePointFixture(t)
	body := `{"estimate_point":"` + serializers.UUID(f.pointA) + `"}`

	rec, err := f.patchIssue(t, body)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PATCH estimate_point: status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}

	reloaded, _ := serializers.FindByUUID(f.app, "issues", serializers.UUID(f.issueA))
	if got := reloaded.GetString("estimate_point"); got != f.pointA.Id {
		t.Fatalf("issues.estimate_point = %q, want the project-A point id", got)
	}
	if got := serializers.Issue(f.app, reloaded)["estimate_point"]; got != serializers.UUID(f.pointA) {
		t.Fatalf("serialized estimate_point = %#v, want %s", got, serializers.UUID(f.pointA))
	}
}

// TestUpdateRejectsForeignProjectEstimatePoint: a point from another project is
// refused with upstream's validation message and leaves the issue untouched.
func TestUpdateRejectsForeignProjectEstimatePoint(t *testing.T) {
	f := newEstimatePointFixture(t)
	body := `{"estimate_point":"` + serializers.UUID(f.pointB) + `"}`

	rec, err := f.patchIssue(t, body)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cross-project PATCH: status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Estimate point is not valid please pass a valid estimate_point_id") {
		t.Fatalf("cross-project PATCH body = %s, want the upstream validation message", rec.Body.String())
	}
	reloaded, _ := serializers.FindByUUID(f.app, "issues", serializers.UUID(f.issueA))
	if got := reloaded.GetString("estimate_point"); got != "" {
		t.Fatalf("issues.estimate_point = %q after rejection, want it untouched", got)
	}
}

// TestUpdateRejectsUnknownEstimatePoint: a syntactically fine but nonexistent
// uuid is refused the same way.
func TestUpdateRejectsUnknownEstimatePoint(t *testing.T) {
	f := newEstimatePointFixture(t)
	rec, err := f.patchIssue(t, `{"estimate_point":"00000000-0000-4000-8000-000000000000"}`)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown point PATCH: status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestUpdateClearsEstimatePointWithNull: the frontend's EstimateSelect emits
// null for "No estimate", so an explicit null must clear the relation.
func TestUpdateClearsEstimatePointWithNull(t *testing.T) {
	f := newEstimatePointFixture(t)
	if _, err := f.patchIssue(t, `{"estimate_point":"`+serializers.UUID(f.pointA)+`"}`); err != nil {
		t.Fatalf("seed Update: %v", err)
	}

	rec, err := f.patchIssue(t, `{"estimate_point":null}`)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("null PATCH: status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
	reloaded, _ := serializers.FindByUUID(f.app, "issues", serializers.UUID(f.issueA))
	if got := reloaded.GetString("estimate_point"); got != "" {
		t.Fatalf("issues.estimate_point = %q after null PATCH, want cleared", got)
	}
}

// TestCreateAcceptsEstimatePoint: the create path accepts and returns the point.
func TestCreateAcceptsEstimatePoint(t *testing.T) {
	f := newEstimatePointFixture(t)
	body := `{"name":"created","estimate_point":"` + serializers.UUID(f.pointA) + `"}`

	e, rec := f.event(t, http.MethodPost, "/issues/", body)
	if err := New(f.app).Create(e); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("create with estimate_point: status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), serializers.UUID(f.pointA)) {
		t.Fatalf("create response = %s, want it to carry the point UUID", rec.Body.String())
	}
}

// TestCreateRejectsForeignProjectEstimatePoint: create with another project's
// point fails the whole transaction.
func TestCreateRejectsForeignProjectEstimatePoint(t *testing.T) {
	f := newEstimatePointFixture(t)
	body := `{"name":"created","estimate_point":"` + serializers.UUID(f.pointB) + `"}`

	e, rec := f.event(t, http.MethodPost, "/issues/", body)
	if err := New(f.app).Create(e); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cross-project create: status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	rows, _ := f.app.FindRecordsByFilter("issues", "name = 'created'", "", 0, 0, nil)
	if len(rows) != 0 {
		t.Fatalf("cross-project create stored %d issues, want 0", len(rows))
	}
}

// TestBulkOperationAppliesEstimatePoint: the bulk-update whitelist carries
// estimate_point (it used to be explicitly ignored — wave1.go:405) and applies
// the same project-scoping rule.
func TestBulkOperationAppliesEstimatePoint(t *testing.T) {
	f := newEstimatePointFixture(t)
	bulk := func(t *testing.T, body string) *httptest.ResponseRecorder {
		t.Helper()
		e, rec := f.event(t, http.MethodPost, "/bulk-operation-issues/", body)
		if err := New(f.app).BulkOperation(e); err != nil {
			t.Fatalf("BulkOperation: %v", err)
		}
		return rec
	}

	rec := bulk(t, `{"issue_ids":["`+serializers.UUID(f.issueA)+`"],"properties":{"estimate_point":"`+serializers.UUID(f.pointA)+`"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk update: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	reloaded, _ := serializers.FindByUUID(f.app, "issues", serializers.UUID(f.issueA))
	if got := reloaded.GetString("estimate_point"); got != f.pointA.Id {
		t.Fatalf("bulk update: issues.estimate_point = %q, want the project-A point id", got)
	}

	// A point from another project must be refused, not silently dropped.
	rec = bulk(t, `{"issue_ids":["`+serializers.UUID(f.issueA)+`"],"properties":{"estimate_point":"`+serializers.UUID(f.pointB)+`"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bulk cross-project: status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	reloaded, _ = serializers.FindByUUID(f.app, "issues", serializers.UUID(f.issueA))
	if got := reloaded.GetString("estimate_point"); got != f.pointA.Id {
		t.Fatalf("bulk cross-project rejection: issues.estimate_point = %q, want the previously stored point", got)
	}
}

// TestResolveEstimatePoint covers the resolver directly, including the
// non-string / empty-string cases.
func TestResolveEstimatePoint(t *testing.T) {
	f := newEstimatePointFixture(t)

	id, err := resolveEstimatePoint(f.app, f.projectA.Id, serializers.UUID(f.pointA))
	if err != nil || id != f.pointA.Id {
		t.Fatalf("same-project point: (%q, %v), want (%q, nil)", id, err, f.pointA.Id)
	}
	if id, err := resolveEstimatePoint(f.app, f.projectA.Id, serializers.UUID(f.pointB)); err == nil {
		t.Fatalf("foreign point: (%q, nil), want an error", id)
	}
	if id, err := resolveEstimatePoint(f.app, f.projectA.Id, nil); err != nil || id != "" {
		t.Fatalf("nil value: (%q, %v), want (\"\", nil) — null clears", id, err)
	}
	if id, err := resolveEstimatePoint(f.app, f.projectA.Id, ""); err != nil || id != "" {
		t.Fatalf("empty string: (%q, %v), want (\"\", nil)", id, err)
	}
	if id, err := resolveEstimatePoint(f.app, f.projectA.Id, 42); err == nil {
		t.Fatalf("non-string value: (%q, nil), want an error", id)
	}
}
