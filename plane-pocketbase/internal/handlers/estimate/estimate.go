// Package estimate implements Plane's estimate endpoints. The SAME resource is
// reachable at two scopes:
//
//	GET                /api/workspaces/{slug}/estimates/                         (workspace-wide)
//	POST               /api/workspaces/{slug}/projects/{project_id}/estimates/
//	GET, PATCH, DELETE …/projects/{project_id}/estimates/{estimate_id}/
//	GET, POST          …/estimates/{estimate_id}/estimate-points/
//	GET, PATCH, DELETE …/estimates/{estimate_id}/estimate-points/{point_id}/
//
// Contract: Django `apps/api/plane/app/views/estimate/base.py`
// (BulkEstimatePointEndpoint / EstimatePointEndpoint), `.../views/workspace/estimate.py`
// (WorkspaceEstimatesEndpoint) and `.../serializers/estimate.py`. All list
// responses are PLAIN ARRAYS (the serializers are used with `many=True` and the
// views do not paginate), NOT the cursor envelope.
//
// The project-scoped `GET …/estimates/` is registered in router/project.go
// (Wave 1, handlers/projects), so this file does NOT re-register it — doing so
// would panic ServeMux with a duplicate-pattern error.
//
// DERIVED SHAPE — `projects.estimate`: Django's WorkspaceEstimatesEndpoint
// selects estimates through `Project.estimate_id`, but that FK is explicitly
// deferred in this schema (pb_migrations/1759000001_init_identity_workspace_project.go:275).
// The workspace-wide list therefore returns every non-deleted estimate in the
// workspace (a superset that still satisfies the frontend, which uses the list
// only to render the estimate systems). See the accompanying report.
package estimate

import (
	"crypto/rand"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Handler holds the app dependency for the estimate routes.
type Handler struct {
	App core.App
}

// New returns an estimate handler bound to app.
func New(app core.App) *Handler {
	return &Handler{App: app}
}

// ---------------------------------------------------------------------------
// Estimates
// ---------------------------------------------------------------------------

// WorkspaceList serves GET /api/workspaces/{slug}/estimates/
// (WorkspaceEstimatesEndpoint). Bare array, ordered by name.
func (h *Handler) WorkspaceList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	records, err := h.App.FindRecordsByFilter(
		"estimates",
		"workspace = {:workspace} && deleted_at = ''",
		"name", 0, 0,
		dbx.Params{"workspace": scope.Workspace.Id},
	)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, h.serializeList(records))
}

// Create serves POST …/projects/{project_id}/estimates/
// (BulkEstimatePointEndpoint.create). ADMIN or MEMBER only. Body:
//
//	{"estimate": {"name"?, "type"?, "last_used"?}, "estimate_points": [{"key", "value", "description"?}]}
//
// Returns 200 (Plane does not use 201 here) with the read serializer.
func (h *Handler) Create(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	estimateBody, _ := body["estimate"].(map[string]any)

	name := strings.TrimSpace(asString(estimateBody["name"]))
	if name == "" {
		name = randomName()
	}
	estimateType := asString(estimateBody["type"])
	if estimateType == "" {
		estimateType = "categories"
	}
	if estimateType != "categories" && estimateType != "points" {
		return serializers.Validation(e, "type", "\""+estimateType+"\" is not a valid choice.")
	}
	lastUsed := asBool(estimateBody["last_used"])

	points := normalizePoints(body["estimate_points"])
	for _, p := range points {
		if msg := validatePoint(p); msg != "" {
			return serializers.Validation(e, "value", msg)
		}
	}

	collection, err := h.App.FindCollectionByNameOrId("estimates")
	if err != nil {
		return err
	}
	var created *core.Record
	err = h.App.RunInTransaction(func(txApp core.App) error {
		record := core.NewRecord(collection)
		record.Set("uuid", serializers.NewUUID())
		record.Set("name", name)
		record.Set("description", asString(estimateBody["description"]))
		record.Set("type", estimateType)
		record.Set("last_used", lastUsed)
		record.Set("project", scope.Project.Id)
		record.Set("workspace", scope.Workspace.Id)
		record.Set("created_by", user.Id)
		if err := txApp.Save(record); err != nil {
			return err
		}
		if err := h.createPoints(txApp, scope, user, record.Id, points); err != nil {
			return err
		}
		created = record
		return nil
	})
	if err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.Estimate(h.App, created))
}

// Detail serves GET …/estimates/{estimate_id}/ (BulkEstimatePointEndpoint.retrieve).
func (h *Handler) Detail(e *core.RequestEvent) error {
	record, err := h.scopedEstimate(e)
	if err != nil {
		return err
	}
	if record == nil {
		return serializers.NotFound(e, "")
	}
	return e.JSON(http.StatusOK, serializers.Estimate(h.App, record))
}

// Update serves PATCH …/estimates/{estimate_id}/
// (BulkEstimatePointEndpoint.partial_update). Requires a non-empty
// `estimate_points` list; updates the estimate's name/type and the named points.
func (h *Handler) Update(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	record, err := h.scopedEstimate(e)
	if err != nil {
		return err
	}
	if record == nil {
		return serializers.NotFound(e, "")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	points := normalizePoints(body["estimate_points"])
	if len(points) == 0 {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Estimate points are required"})
	}
	for _, p := range points {
		if msg := validatePoint(p); msg != "" {
			return serializers.Validation(e, "value", msg)
		}
	}

	if estimateBody, ok := body["estimate"].(map[string]any); ok && estimateBody != nil {
		if name := strings.TrimSpace(asString(estimateBody["name"])); name != "" {
			record.Set("name", name)
		}
		if t := asString(estimateBody["type"]); t != "" {
			record.Set("type", t)
		}
	}
	if user := middleware.CurrentUser(e); user != nil {
		record.Set("updated_by", user.Id)
	}

	err = h.App.RunInTransaction(func(txApp core.App) error {
		if err := txApp.Save(record); err != nil {
			return err
		}
		for _, p := range points {
			id := asString(p["id"])
			if id == "" {
				continue
			}
			point, _ := serializers.FindByUUID(txApp, "estimate_points", id)
			if point == nil || point.GetString("estimate") != record.Id {
				continue
			}
			if v, ok := p["value"]; ok {
				point.Set("value", v)
			}
			if v, ok := p["key"]; ok {
				point.Set("key", v)
			}
			if v, ok := p["description"]; ok {
				point.Set("description", v)
			}
			if err := txApp.Save(point); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.Estimate(h.App, record))
}

// Destroy serves DELETE …/estimates/{estimate_id}/ (soft delete).
func (h *Handler) Destroy(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	record, err := h.scopedEstimate(e)
	if err != nil {
		return err
	}
	if record == nil {
		return serializers.NotFound(e, "")
	}
	record.Set("deleted_at", serializers.Now())
	if err := h.App.Save(record); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Estimate points
// ---------------------------------------------------------------------------

// PointsList serves GET …/estimates/{estimate_id}/estimate-points/. Bare array
// ordered by `value` (EstimatePoint.Meta.ordering = ("value",)).
func (h *Handler) PointsList(e *core.RequestEvent) error {
	estimate, err := h.scopedEstimate(e)
	if err != nil {
		return err
	}
	if estimate == nil {
		return serializers.NotFound(e, "")
	}
	return e.JSON(http.StatusOK, h.pointsFor(estimate.Id))
}

// PointCreate serves POST …/estimates/{estimate_id}/estimate-points/
// (EstimatePointEndpoint.create). ADMIN or MEMBER only; requires `key` and
// `value`.
func (h *Handler) PointCreate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	estimate, err := h.scopedEstimate(e)
	if err != nil {
		return err
	}
	if estimate == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Estimate not found"})
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	// Mirror Django exactly: `if not request.data.get("key") or not request.data.get("value")`.
	if isFalsy(body["key"]) || isFalsy(body["value"]) {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Key and value are required"})
	}
	if msg := validatePoint(body); msg != "" {
		return serializers.Validation(e, "value", msg)
	}

	collection, err := h.App.FindCollectionByNameOrId("estimate_points")
	if err != nil {
		return err
	}
	point := core.NewRecord(collection)
	point.Set("uuid", serializers.NewUUID())
	point.Set("estimate", estimate.Id)
	point.Set("project", scope.Project.Id)
	point.Set("workspace", scope.Workspace.Id)
	point.Set("key", asInt(body["key"]))
	point.Set("value", asString(body["value"]))
	point.Set("description", asString(body["description"]))
	point.Set("created_by", user.Id)
	if err := h.App.Save(point); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.EstimatePoint(h.App, point))
}

// PointDetail serves GET …/estimate-points/{point_id}/.
func (h *Handler) PointDetail(e *core.RequestEvent) error {
	point, err := h.scopedPoint(e)
	if err != nil {
		return err
	}
	if point == nil {
		return serializers.NotFound(e, "")
	}
	return e.JSON(http.StatusOK, serializers.EstimatePoint(h.App, point))
}

// PointUpdate serves PATCH …/estimate-points/{point_id}/
// (EstimatePointEndpoint.partial_update).
func (h *Handler) PointUpdate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	point, err := h.scopedPoint(e)
	if err != nil {
		return err
	}
	if point == nil {
		return serializers.NotFound(e, "")
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	if msg := validatePoint(body); msg != "" {
		return serializers.Validation(e, "value", msg)
	}
	if v, ok := body["key"]; ok {
		point.Set("key", asInt(v))
	}
	if v, ok := body["value"]; ok {
		point.Set("value", asString(v))
	}
	if v, ok := body["description"]; ok {
		point.Set("description", asString(v))
	}
	if user := middleware.CurrentUser(e); user != nil {
		point.Set("updated_by", user.Id)
	}
	if err := h.App.Save(point); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.EstimatePoint(h.App, point))
}

// PointDestroy serves DELETE …/estimate-points/{point_id}/
// (EstimatePointEndpoint.destroy): deletes the point, renumbers the remaining
// points whose key was greater, and returns the remaining points as an array.
func (h *Handler) PointDestroy(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	point, err := h.scopedPoint(e)
	if err != nil {
		return err
	}
	if point == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Estimate point not found"})
	}
	estimateID := point.GetString("estimate")
	deletedKey := point.GetInt("key")

	remaining, err := h.App.FindRecordsByFilter(
		"estimate_points",
		"estimate = {:estimate} && project = {:project} && workspace = {:workspace} && deleted_at = ''",
		"value", 0, 0,
		dbx.Params{"estimate": estimateID, "project": scope.Project.Id, "workspace": scope.Workspace.Id},
	)
	if err != nil {
		return err
	}

	err = h.App.RunInTransaction(func(txApp core.App) error {
		for _, p := range remaining {
			if p.Id == point.Id {
				continue
			}
			if p.GetInt("key") > deletedKey {
				p.Set("key", p.GetInt("key")-1)
				if err := txApp.Save(p); err != nil {
					return err
				}
			}
		}
		return txApp.Delete(point)
	})
	if err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}

	out := make([]any, 0, len(remaining))
	for _, p := range remaining {
		if p.Id == point.Id {
			continue
		}
		out = append(out, serializers.EstimatePoint(h.App, p))
	}
	return e.JSON(http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// scopedEstimate resolves the route's `estimate_id` within the project scope.
func (h *Handler) scopedEstimate(e *core.RequestEvent) (*core.Record, error) {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return nil, serializers.NotFound(e, "")
	}
	record, _ := serializers.FindByUUID(h.App, "estimates", e.Request.PathValue("estimate_id"))
	if record == nil ||
		record.GetString("project") != scope.Project.Id ||
		record.GetString("workspace") != scope.Workspace.Id ||
		record.GetString("deleted_at") != "" {
		return nil, nil
	}
	return record, nil
}

// scopedPoint resolves the route's `point_id`, scoped to its estimate/project.
func (h *Handler) scopedPoint(e *core.RequestEvent) (*core.Record, error) {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return nil, serializers.NotFound(e, "")
	}
	estimate, err := h.scopedEstimate(e)
	if err != nil {
		return nil, err
	}
	if estimate == nil {
		return nil, nil
	}
	point, _ := serializers.FindByUUID(h.App, "estimate_points", e.Request.PathValue("point_id"))
	if point == nil ||
		point.GetString("estimate") != estimate.Id ||
		point.GetString("project") != scope.Project.Id ||
		point.GetString("deleted_at") != "" {
		return nil, nil
	}
	return point, nil
}

// serializeList renders estimates with their nested points.
func (h *Handler) serializeList(records []*core.Record) []any {
	out := make([]any, 0, len(records))
	for _, r := range records {
		out = append(out, serializers.Estimate(h.App, r))
	}
	return out
}

// pointsFor renders an estimate's points, ordered by `value`.
func (h *Handler) pointsFor(estimateID string) []any {
	records, err := h.App.FindRecordsByFilter(
		"estimate_points",
		"estimate = {:estimate} && deleted_at = ''",
		"value", 0, 0,
		dbx.Params{"estimate": estimateID},
	)
	out := make([]any, 0, len(records))
	if err != nil {
		return out
	}
	for _, r := range records {
		out = append(out, serializers.EstimatePoint(h.App, r))
	}
	return out
}

// createPoints bulk-creates the estimate's points.
func (h *Handler) createPoints(app core.App, scope *middleware.Scope, user *core.Record, estimateID string, points []map[string]any) error {
	if len(points) == 0 {
		return nil
	}
	collection, err := app.FindCollectionByNameOrId("estimate_points")
	if err != nil {
		return err
	}
	for _, p := range points {
		record := core.NewRecord(collection)
		record.Set("uuid", serializers.NewUUID())
		record.Set("estimate", estimateID)
		record.Set("project", scope.Project.Id)
		record.Set("workspace", scope.Workspace.Id)
		record.Set("key", asInt(p["key"]))
		record.Set("value", asString(p["value"]))
		record.Set("description", asString(p["description"]))
		record.Set("created_by", user.Id)
		if err := app.Save(record); err != nil {
			return err
		}
	}
	return nil
}

// normalizePoints coerces the request's `estimate_points` value into a list of
// maps. A nil/absent value yields an empty list.
func normalizePoints(v any) []map[string]any {
	raw, ok := v.([]any)
	if !ok {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok && m != nil {
			out = append(out, m)
		}
	}
	return out
}

// validatePoint reproduces EstimatePointSerializer.validate: `value` may not
// exceed 20 characters. Returns "" when the point is acceptable.
func validatePoint(p map[string]any) string {
	if value, ok := p["value"]; ok {
		if len(asString(value)) > 20 {
			return "Value can't be more than 20 characters"
		}
	}
	return ""
}

// randomName mirrors Django's generate_random_name(): 10 lowercase letters.
func randomName() string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	buf := make([]byte, 10)
	if _, err := rand.Read(buf); err != nil {
		return "estimate"
	}
	for i := range buf {
		buf[i] = letters[int(buf[i])%len(letters)]
	}
	return string(buf)
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// isFalsy mirrors Python's truthiness for the estimate-point key/value guard.
func isFalsy(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case float64:
		return t == 0
	case int:
		return t == 0
	case int64:
		return t == 0
	case bool:
		return !t
	}
	return false
}
