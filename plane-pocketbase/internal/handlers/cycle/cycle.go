// Package cycle implements Plane's cycle endpoints for a project:
//
//	GET/POST  /api/workspaces/{slug}/projects/{project_id}/cycles/       (plain array)
//	GET/PUT/PATCH/DELETE /api/workspaces/{slug}/projects/{project_id}/cycles/{pk}/
//	POST      …/cycles/date-check/
//	GET/POST  …/user-favorite-cycles/            DELETE …/user-favorite-cycles/{cycle_id}/
//	POST      …/cycles/{cycle_id}/transfer-issues/
//	GET/PATCH …/cycles/{cycle_id}/user-properties/
//	GET/POST/DELETE …/cycles/{cycle_id}/archive/
//	GET       …/archived-cycles/                 GET …/archived-cycles/{pk}/
//	GET       …/cycles/{cycle_id}/progress/
//	GET       …/cycles/{cycle_id}/analytics/
//	GET       /api/workspaces/{slug}/cycles/     (workspace-wide plain array)
//
// The cycle-issue sub-resource lives in issue.go.
//
// Wire-shape note: CycleViewSet.list overrides DRF's pagination and returns a
// plain JSON array, and the frontend (`CycleService.getWithParams`) is typed as
// `ICycle[]`. The api-spec's generic "cursor-paginated envelope" response_shape
// does not apply to cycles — see the report accompanying this change.
package cycle

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Handler holds the app dependency for the cycle routes.
type Handler struct {
	App core.App
}

// New returns a cycle handler bound to app.
func New(app core.App) *Handler {
	return &Handler{App: app}
}

// List serves GET /api/workspaces/{slug}/projects/{project_id}/cycles/.
// It returns a plain array ordered by `-is_favorite, -created_at`.
func (h *Handler) List(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}

	cycles, err := h.App.FindRecordsByFilter(
		"cycles",
		"project = {:project} && workspace = {:workspace} && deleted_at = '' && archived_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"project": scope.Project.Id, "workspace": scope.Workspace.Id},
	)
	if err != nil {
		return err
	}
	if e.Request.URL.Query().Get("cycle_view") == "current" {
		current := make([]*core.Record, 0, len(cycles))
		for _, c := range cycles {
			if serializers.CycleStatus(c) == "CURRENT" {
				current = append(current, c)
			}
		}
		cycles = current
	}
	return e.JSON(http.StatusOK, h.serializeList(e, scope, cycles))
}

// WorkspaceList serves GET /api/workspaces/{slug}/cycles/ (WorkspaceCyclesEndpoint):
// every non-archived cycle in the workspace that belongs to a project the caller
// is an active member of. Returns a plain array.
func (h *Handler) WorkspaceList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	projectIDs := h.memberProjectIDs(user.Id, scope.Workspace.Id)
	if len(projectIDs) == 0 {
		return e.JSON(http.StatusOK, []any{})
	}

	params := dbx.Params{"workspace": scope.Workspace.Id}
	filter := "workspace = {:workspace} && deleted_at = '' && archived_at = '' && " +
		orIDFilter("project", projectIDs, params)

	cycles, err := h.App.FindRecordsByFilter("cycles", filter, "-created_at", 0, 0, params)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, h.serializeList(e, scope, cycles))
}

// Create serves POST …/cycles/ (CycleViewSet.create). ADMIN or MEMBER only.
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
	name := strings.TrimSpace(asString(body["name"]))
	if name == "" {
		return serializers.Validation(e, "name", "This field is required.")
	}
	startDate := asString(body["start_date"])
	endDate := asString(body["end_date"])
	if (startDate == "") != (endDate == "") {
		return e.JSON(http.StatusBadRequest, map[string]any{
			"error": "Both start date and end date are either required or are to be null",
		})
	}
	if startDate != "" && endDate != "" && startDate > endDate {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Start date cannot exceed end date"})
	}

	collection, err := h.App.FindCollectionByNameOrId("cycles")
	if err != nil {
		return err
	}

	var created *core.Record
	err = h.App.RunInTransaction(func(txApp core.App) error {
		record := core.NewRecord(collection)
		record.Set("uuid", serializers.NewUUID())
		record.Set("project", scope.Project.Id)
		record.Set("workspace", scope.Workspace.Id)
		record.Set("name", name)
		record.Set("owned_by", user.Id)
		record.Set("created_by", user.Id)
		record.Set("sort_order", h.nextSortOrder(txApp, scope.Project.Id))
		record.Set("version", 1)
		for _, field := range []string{"description", "view_props", "external_source", "external_id", "logo_props"} {
			if v, ok := body[field]; ok {
				record.Set(field, v)
			}
		}
		if tz := asString(body["timezone"]); tz != "" {
			record.Set("timezone", tz)
		} else if tz := scope.Project.GetString("timezone"); tz != "" {
			record.Set("timezone", tz)
		}
		if startDate != "" {
			record.Set("start_date", startDate)
		}
		if endDate != "" {
			record.Set("end_date", endDate)
		}
		// `owned_by` is read-only in the serializer but Plane defaults it to the
		// caller; honour an explicit owner when it resolves to a real user.
		if owner := asString(body["owned_by"]); owner != "" {
			if u, _ := serializers.FindByUUID(txApp, "users", owner); u != nil {
				record.Set("owned_by", u.Id)
			}
		}
		if err := txApp.Save(record); err != nil {
			return err
		}
		created = record
		return nil
	})
	if err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusCreated, serializers.Cycle(h.App, created, false))
}

// Detail serves GET …/cycles/{pk}/ (CycleViewSet.retrieve).
func (h *Handler) Detail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	record, err := h.findCycle(scope, e.Request.PathValue("pk"))
	if err != nil {
		return err
	}
	if record == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Cycle not found"})
	}
	return e.JSON(http.StatusOK, serializers.Cycle(h.App, record, h.isFavorite(e, scope, serializers.UUID(record))))
}

// Update serves PUT/PATCH …/cycles/{pk}/ (CycleViewSet.partial_update).
// ADMIN or MEMBER only. A completed cycle may only have its `sort_order` edited.
func (h *Handler) Update(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	record, err := h.findCycle(scope, e.Request.PathValue("pk"))
	if err != nil {
		return err
	}
	if record == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Cycle not found"})
	}
	if record.GetString("archived_at") != "" {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Archived cycle cannot be updated"})
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}

	end := record.GetDateTime("end_date").Time()
	completed := !end.IsZero() && end.Before(serializers.Now())
	if completed {
		if _, ok := body["sort_order"]; !ok {
			return e.JSON(http.StatusBadRequest, map[string]any{
				"error": "The Cycle has already been completed so it cannot be edited",
			})
		}
		// Only the sort order may change on a completed cycle.
		body = map[string]any{"sort_order": body["sort_order"]}
	}

	for _, field := range []string{
		"name", "description", "start_date", "end_date", "view_props",
		"sort_order", "external_source", "external_id", "logo_props", "timezone", "progress_snapshot",
	} {
		if v, ok := body[field]; ok {
			record.Set(field, v)
		}
	}
	if owner := asString(body["owned_by"]); owner != "" {
		if u, _ := serializers.FindByUUID(h.App, "users", owner); u != nil {
			record.Set("owned_by", u.Id)
		}
	}
	if user := middleware.CurrentUser(e); user != nil {
		record.Set("updated_by", user.Id)
	}
	if err := h.App.Save(record); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.Cycle(h.App, record, h.isFavorite(e, scope, serializers.UUID(record))))
}

// Delete serves DELETE …/cycles/{pk}/ (CycleViewSet.destroy). ADMIN, or the
// creator. Soft-deletes and clears the caller's favourite.
func (h *Handler) Delete(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	record, err := h.findCycle(scope, e.Request.PathValue("pk"))
	if err != nil {
		return err
	}
	if record == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	if !middleware.IsCreator(e, record) && scope.ProjectRole < middleware.RoleAdmin {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	record.Set("deleted_at", serializers.Now())
	if err := h.App.Save(record); err != nil {
		return err
	}
	h.clearFavorites(scope, serializers.UUID(record))
	return e.NoContent(http.StatusNoContent)
}

// DateCheck serves POST …/cycles/date-check/ (CycleDateCheckEndpoint).
func (h *Handler) DateCheck(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	start := asString(body["start_date"])
	end := asString(body["end_date"])
	if start == "" || end == "" {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Start date and end date both are required"})
	}
	skip := asString(body["cycle_id"])
	newStart, newEnd := datePart(start), datePart(end)

	cycles, err := h.App.FindRecordsByFilter(
		"cycles",
		"project = {:project} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"project": scope.Project.Id},
	)
	if err != nil {
		return err
	}
	for _, c := range cycles {
		if serializers.UUID(c) == skip {
			continue
		}
		s := dateTimePart(c, "start_date")
		t := dateTimePart(c, "end_date")
		if s == "" || t == "" {
			continue
		}
		if (s <= newStart && t >= newStart) || (s <= newEnd && t >= newEnd) || (s >= newStart && t <= newEnd) {
			return e.JSON(http.StatusOK, map[string]any{
				"error":  "You have a cycle already on the given dates, if you want to create a draft cycle you can do that by removing dates",
				"status": false,
			})
		}
	}
	return e.JSON(http.StatusOK, map[string]any{"status": true})
}

// FavoritesList serves GET …/user-favorite-cycles/: the cycles the caller has
// favourited, as a plain array.
func (h *Handler) FavoritesList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	favorites, err := h.App.FindRecordsByFilter(
		"user_favorites",
		"user = {:user} && project = {:project} && entity_type = 'cycle' && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"user": user.Id, "project": scope.Project.Id},
	)
	if err != nil {
		return err
	}
	results := make([]any, 0, len(favorites))
	for _, fav := range favorites {
		record, _ := serializers.FindByUUID(h.App, "cycles", fav.GetString("entity_identifier"))
		if record == nil || record.GetString("deleted_at") != "" {
			continue
		}
		results = append(results, serializers.Cycle(h.App, record, true))
	}
	return e.JSON(http.StatusOK, results)
}

// FavoriteCreate serves POST …/user-favorite-cycles/ (body `{cycle: <uuid>}`).
// ADMIN or MEMBER only, matching CycleFavoriteViewSet.
func (h *Handler) FavoriteCreate(e *core.RequestEvent) error {
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
	cycleUUID := asString(body["cycle"])
	if cycleUUID == "" {
		cycleUUID = asString(body["cycle_id"])
	}
	if cycleUUID == "" {
		return serializers.BadRequest(e, "Cycle is required")
	}
	collection, err := h.App.FindCollectionByNameOrId("user_favorites")
	if err != nil {
		return err
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("user", user.Id)
	record.Set("project", scope.Project.Id)
	record.Set("workspace", scope.Workspace.Id)
	record.Set("entity_type", "cycle")
	record.Set("entity_identifier", cycleUUID)
	record.Set("created_by", user.Id)
	if err := h.App.Save(record); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// FavoriteDestroy serves DELETE …/user-favorite-cycles/{cycle_id}/.
func (h *Handler) FavoriteDestroy(e *core.RequestEvent) error {
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
	h.deleteFavorite(user.Id, scope.Project.Id, "cycle", e.Request.PathValue("cycle_id"))
	return e.NoContent(http.StatusNoContent)
}

// UserPropertiesGet serves GET …/cycles/{cycle_id}/user-properties/. The row is
// created on demand (CycleUserPropertiesEndpoint.get).
func (h *Handler) UserPropertiesGet(e *core.RequestEvent) error {
	record, err := h.userProperties(e, e.Request.PathValue("cycle_id"))
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, serializers.CycleUserProperties(h.App, record))
}

// UserPropertiesPatch serves PATCH …/cycles/{cycle_id}/user-properties/.
func (h *Handler) UserPropertiesPatch(e *core.RequestEvent) error {
	record, err := h.userProperties(e, e.Request.PathValue("cycle_id"))
	if err != nil {
		return err
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	for _, field := range []string{"filters", "rich_filters", "display_filters", "display_properties"} {
		if v, ok := body[field]; ok {
			record.Set(field, v)
		}
	}
	if err := h.App.Save(record); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusCreated, serializers.CycleUserProperties(h.App, record))
}

// ArchiveGet serves GET …/cycles/{cycle_id}/archive/.
func (h *Handler) ArchiveGet(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	record, err := h.findCycleIncludingArchived(scope, e.Request.PathValue("cycle_id"))
	if err != nil {
		return err
	}
	if record == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Cycle not found"})
	}
	return e.JSON(http.StatusOK, serializers.Cycle(h.App, record, h.isFavorite(e, scope, serializers.UUID(record))))
}

// Archive serves POST …/cycles/{cycle_id}/archive/: only a completed cycle can
// be archived.
func (h *Handler) Archive(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	record, err := h.findCycleIncludingArchived(scope, e.Request.PathValue("cycle_id"))
	if err != nil {
		return err
	}
	if record == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Cycle not found"})
	}
	end := record.GetDateTime("end_date").Time()
	if end.IsZero() || !end.Before(serializers.Now()) {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Only completed cycles can be archived"})
	}
	archivedAt := serializers.Now()
	record.Set("archived_at", archivedAt)
	if err := h.App.Save(record); err != nil {
		return err
	}
	h.clearFavorites(scope, serializers.UUID(record))
	return e.JSON(http.StatusOK, map[string]any{"archived_at": serializers.Time(archivedAt)})
}

// Unarchive serves DELETE …/cycles/{cycle_id}/archive/.
func (h *Handler) Unarchive(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	record, err := h.findCycleIncludingArchived(scope, e.Request.PathValue("cycle_id"))
	if err != nil {
		return err
	}
	if record == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Cycle not found"})
	}
	record.Set("archived_at", nil)
	if err := h.App.Save(record); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// ArchivedList serves GET …/archived-cycles/ (plain array).
func (h *Handler) ArchivedList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	cycles, err := h.App.FindRecordsByFilter(
		"cycles",
		"project = {:project} && deleted_at = '' && archived_at != ''",
		"-created_at", 0, 0,
		dbx.Params{"project": scope.Project.Id},
	)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, h.serializeList(e, scope, cycles))
}

// ArchivedDetail serves GET …/archived-cycles/{pk}/.
func (h *Handler) ArchivedDetail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	record, err := h.findCycleIncludingArchived(scope, e.Request.PathValue("pk"))
	if err != nil {
		return err
	}
	if record == nil || record.GetString("archived_at") == "" {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Cycle not found"})
	}
	return e.JSON(http.StatusOK, serializers.Cycle(h.App, record, h.isFavorite(e, scope, serializers.UUID(record))))
}

// Progress serves GET …/cycles/{cycle_id}/progress/ (CycleProgressEndpoint).
func (h *Handler) Progress(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	record, err := h.findCycleIncludingArchived(scope, e.Request.PathValue("cycle_id"))
	if err != nil {
		return err
	}
	if record == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Cycle not found"})
	}

	serialized := serializers.Cycle(h.App, record, false)
	backlog := toInt(serialized["backlog_issues"])
	unstarted := toInt(serialized["unstarted_issues"])
	started := toInt(serialized["started_issues"])
	cancelled := toInt(serialized["cancelled_issues"])
	completed := toInt(serialized["completed_issues"])
	total := toInt(serialized["total_issues"])

	// A transferred cycle carries a frozen snapshot; prefer it (Plane does).
	if snapshot, ok := record.Get("progress_snapshot").(map[string]any); ok && snapshot != nil {
		backlog = toInt(snapshot["backlog_issues"])
		unstarted = toInt(snapshot["unstarted_issues"])
		started = toInt(snapshot["started_issues"])
		cancelled = toInt(snapshot["cancelled_issues"])
		completed = toInt(snapshot["completed_issues"])
		total = toInt(snapshot["total_issues"])
	}

	return e.JSON(http.StatusOK, map[string]any{
		// Estimate-point aggregates are 0: `issues.estimate_point` is absent from
		// this vertical slice (see serializers/module.go).
		"backlog_estimate_points":   0,
		"unstarted_estimate_points": 0,
		"started_estimate_points":   0,
		"cancelled_estimate_points": 0,
		"completed_estimate_points": 0,
		"total_estimate_points":     0,
		"backlog_issues":            backlog,
		"total_issues":              total,
		"completed_issues":          completed,
		"cancelled_issues":          cancelled,
		"started_issues":            started,
		"unstarted_issues":          unstarted,
	})
}

// Analytics serves GET …/cycles/{cycle_id}/analytics/ (CycleAnalyticsEndpoint).
// Returns the assignee/label distributions over the cycle's issues. The
// `completion_chart` burndown is left empty (it needs issue history), and the
// `type=points` estimate variant degrades to the issues distribution because
// `issues.estimate_point` is not part of this slice.
func (h *Handler) Analytics(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	record, err := h.findCycleIncludingArchived(scope, e.Request.PathValue("cycle_id"))
	if err != nil {
		return err
	}
	if record == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Cycle not found"})
	}
	if record.GetDateTime("start_date").IsZero() || record.GetDateTime("end_date").IsZero() {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Cycle has no start or end date"})
	}
	if snapshot, ok := record.Get("progress_snapshot").(map[string]any); ok && snapshot != nil {
		if distribution, ok := snapshot["distribution"].(map[string]any); ok && distribution != nil {
			return e.JSON(http.StatusOK, map[string]any{
				"labels":           valueOr(distribution["labels"], []any{}),
				"assignees":        valueOr(distribution["assignees"], []any{}),
				"completion_chart": valueOr(distribution["completion_chart"], map[string]any{}),
			})
		}
	}
	return e.JSON(http.StatusOK, map[string]any{
		"assignees":        h.assigneeDistribution(record.Id),
		"labels":           h.labelDistribution(record.Id),
		"completion_chart": map[string]any{},
	})
}

// TransferIssues serves POST …/cycles/{cycle_id}/transfer-issues/
// (TransferCycleIssueEndpoint): move the cycle's issues onto `new_cycle_id`.
func (h *Handler) TransferIssues(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	source, err := h.findCycleIncludingArchived(scope, e.Request.PathValue("cycle_id"))
	if err != nil {
		return err
	}
	if source == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Cycle not found"})
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	newCycleUUID := asString(body["new_cycle_id"])
	if newCycleUUID == "" {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "New Cycle Id is required"})
	}
	target, _ := serializers.FindByUUID(h.App, "cycles", newCycleUUID)
	if target == nil || target.GetString("project") != scope.Project.Id || target.GetString("deleted_at") != "" {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "New cycle does not exist"})
	}

	links, err := h.App.FindRecordsByFilter(
		"cycle_issues",
		"cycle = {:cycle} && project = {:project} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"cycle": source.Id, "project": scope.Project.Id},
	)
	if err != nil {
		return err
	}
	err = h.App.RunInTransaction(func(txApp core.App) error {
		for _, link := range links {
			link.Set("cycle", target.Id)
			if user := middleware.CurrentUser(e); user != nil {
				link.Set("updated_by", user.Id)
			}
			if err := txApp.Save(link); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, map[string]any{"message": "Success"})
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

// findCycle resolves a non-deleted, non-archived cycle within the project.
func (h *Handler) findCycle(scope *middleware.Scope, uuid string) (*core.Record, error) {
	record, err := serializers.FindByUUID(h.App, "cycles", uuid)
	if err != nil || record == nil {
		return nil, err
	}
	if record.GetString("project") != scope.Project.Id || record.GetString("deleted_at") != "" || record.GetString("archived_at") != "" {
		return nil, nil
	}
	return record, nil
}

// findCycleIncludingArchived resolves a non-deleted cycle regardless of archive
// state (used by the archive/unarchive/progress/analytics endpoints).
func (h *Handler) findCycleIncludingArchived(scope *middleware.Scope, uuid string) (*core.Record, error) {
	record, err := serializers.FindByUUID(h.App, "cycles", uuid)
	if err != nil || record == nil {
		return nil, err
	}
	if record.GetString("project") != scope.Project.Id || record.GetString("deleted_at") != "" {
		return nil, nil
	}
	return record, nil
}

// serializeList renders cycles with the caller's favourite flags applied and
// favourites ordered first (Plane orders by `-is_favorite, -created_at`).
func (h *Handler) serializeList(e *core.RequestEvent, scope *middleware.Scope, cycles []*core.Record) []any {
	favorites := map[string]bool{}
	if user := middleware.CurrentUser(e); user != nil && scope.Project != nil {
		favorites = h.favoriteIDs(user.Id, scope.Project.Id)
	}
	fav := make([]any, 0, len(cycles))
	rest := make([]any, 0, len(cycles))
	for _, c := range cycles {
		isFav := favorites[serializers.UUID(c)]
		item := serializers.Cycle(h.App, c, isFav)
		if isFav {
			fav = append(fav, item)
		} else {
			rest = append(rest, item)
		}
	}
	return append(fav, rest...)
}

// favoriteIDs returns the set of cycle UUIDs the user has favourited in the
// project.
func (h *Handler) favoriteIDs(userID, projectID string) map[string]bool {
	out := map[string]bool{}
	records, err := h.App.FindRecordsByFilter(
		"user_favorites",
		"user = {:user} && project = {:project} && entity_type = 'cycle' && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"user": userID, "project": projectID},
	)
	if err != nil {
		return out
	}
	for _, record := range records {
		out[record.GetString("entity_identifier")] = true
	}
	return out
}

// isFavorite reports whether the caller favourited the given cycle.
func (h *Handler) isFavorite(e *core.RequestEvent, scope *middleware.Scope, cycleUUID string) bool {
	user := middleware.CurrentUser(e)
	if user == nil || scope.Project == nil {
		return false
	}
	record, err := h.App.FindFirstRecordByFilter(
		"user_favorites",
		"user = {:user} && project = {:project} && entity_type = 'cycle' && entity_identifier = {:cycle} && deleted_at = ''",
		dbx.Params{"user": user.Id, "project": scope.Project.Id, "cycle": cycleUUID},
	)
	return err == nil && record != nil
}

// clearFavorites hard-deletes every favourite pointing at the cycle
// (CycleArchiveUnarchiveEndpoint.post and CycleViewSet.destroy both do this).
func (h *Handler) clearFavorites(scope *middleware.Scope, cycleUUID string) {
	records, err := h.App.FindRecordsByFilter(
		"user_favorites",
		"project = {:project} && entity_type = 'cycle' && entity_identifier = {:cycle}",
		"", 0, 0,
		dbx.Params{"project": scope.Project.Id, "cycle": cycleUUID},
	)
	if err != nil {
		return
	}
	for _, record := range records {
		_ = h.App.Delete(record)
	}
}

// deleteFavorite removes one caller's favourite row.
func (h *Handler) deleteFavorite(userID, projectID, entityType, entityUUID string) {
	record, err := h.App.FindFirstRecordByFilter(
		"user_favorites",
		"user = {:user} && project = {:project} && entity_type = {:type} && entity_identifier = {:entity}",
		dbx.Params{"user": userID, "project": projectID, "type": entityType, "entity": entityUUID},
	)
	if err == nil && record != nil {
		_ = h.App.Delete(record)
	}
}

// userProperties returns (creating if needed) the caller's properties row for
// the cycle.
func (h *Handler) userProperties(e *core.RequestEvent, cycleUUID string) (*core.Record, error) {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return nil, serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return nil, serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	cycle, _ := serializers.FindByUUID(h.App, "cycles", cycleUUID)
	if cycle == nil || cycle.GetString("project") != scope.Project.Id || cycle.GetString("deleted_at") != "" {
		return nil, serializers.NotFound(e, "")
	}
	existing, err := h.App.FindFirstRecordByFilter(
		"cycle_user_properties",
		"user = {:user} && cycle = {:cycle} && project = {:project} && deleted_at = ''",
		dbx.Params{"user": user.Id, "cycle": cycle.Id, "project": scope.Project.Id},
	)
	if err == nil && existing != nil {
		return existing, nil
	}
	collection, err := h.App.FindCollectionByNameOrId("cycle_user_properties")
	if err != nil {
		return nil, err
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("user", user.Id)
	record.Set("cycle", cycle.Id)
	record.Set("project", scope.Project.Id)
	record.Set("workspace", scope.Workspace.Id)
	record.Set("created_by", user.Id)
	if err := h.App.Save(record); err != nil {
		return nil, err
	}
	return record, nil
}

// nextSortOrder mirrors Cycle.save(): `min(sort_order) - 10000`, or 65535 when
// the project has no cycles yet.
func (h *Handler) nextSortOrder(app core.App, projectID string) float64 {
	var min sql.NullFloat64
	err := app.DB().
		NewQuery("SELECT MIN(sort_order) FROM cycles WHERE project = {:project} AND deleted_at = ''").
		Bind(dbx.Params{"project": projectID}).
		Row(&min)
	if err != nil || !min.Valid {
		return 65535
	}
	return min.Float64 - 10000
}

// memberProjectIDs returns the PocketBase ids of the projects the user is an
// active member of within the workspace.
func (h *Handler) memberProjectIDs(userID, workspaceID string) []string {
	members, err := h.App.FindRecordsByFilter(
		"project_members",
		"member = {:member} && workspace = {:workspace} && is_active = true && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"member": userID, "workspace": workspaceID},
	)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.GetString("project"))
	}
	return ids
}

// assigneeDistribution builds the per-assignee issue breakdown for a cycle.
func (h *Handler) assigneeDistribution(cycleID string) []any {
	var rows []struct {
		AssigneeID      string `db:"assignee_id"`
		FirstName       string `db:"first_name"`
		LastName        string `db:"last_name"`
		DisplayName     string `db:"display_name"`
		AvatarURL       string `db:"avatar_url"`
		TotalIssues     int    `db:"total_issues"`
		CompletedIssues int    `db:"completed_issues"`
	}
	err := h.App.DB().NewQuery(`
		SELECT u.uuid AS assignee_id, u.first_name AS first_name, u.last_name AS last_name,
		       u.display_name AS display_name, u.avatar AS avatar_url,
		       COUNT(DISTINCT i.id) AS total_issues,
		       COUNT(DISTINCT CASE WHEN i.completed_at != '' THEN i.id END) AS completed_issues
		FROM cycle_issues ci
		JOIN issues i ON i.id = ci.issue AND i.deleted_at = '' AND i.archived_at = '' AND i.is_draft = 0
		JOIN issue_assignees ia ON ia.issue = i.id AND ia.deleted_at = ''
		JOIN users u ON u.id = ia.assignee
		WHERE ci.cycle = {:id} AND ci.deleted_at = ''
		GROUP BY u.id
		ORDER BY u.first_name, u.last_name`).
		Bind(dbx.Params{"id": cycleID}).
		All(&rows)
	if err != nil {
		return []any{}
	}
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"assignee_id":      row.AssigneeID,
			"first_name":       row.FirstName,
			"last_name":        row.LastName,
			"display_name":     row.DisplayName,
			"avatar_url":       nullableString(row.AvatarURL),
			"total_issues":     row.TotalIssues,
			"completed_issues": row.CompletedIssues,
			"pending_issues":   row.TotalIssues - row.CompletedIssues,
		})
	}
	return out
}

// labelDistribution builds the per-label issue breakdown for a cycle.
func (h *Handler) labelDistribution(cycleID string) []any {
	var rows []struct {
		LabelID         string `db:"label_id"`
		LabelName       string `db:"label_name"`
		Color           string `db:"color"`
		TotalIssues     int    `db:"total_issues"`
		CompletedIssues int    `db:"completed_issues"`
	}
	err := h.App.DB().NewQuery(`
		SELECT l.uuid AS label_id, l.name AS label_name, l.color AS color,
		       COUNT(DISTINCT i.id) AS total_issues,
		       COUNT(DISTINCT CASE WHEN i.completed_at != '' THEN i.id END) AS completed_issues
		FROM cycle_issues ci
		JOIN issues i ON i.id = ci.issue AND i.deleted_at = '' AND i.archived_at = '' AND i.is_draft = 0
		JOIN issue_labels il ON il.issue = i.id AND il.deleted_at = ''
		JOIN labels l ON l.id = il.label
		WHERE ci.cycle = {:id} AND ci.deleted_at = ''
		GROUP BY l.id
		ORDER BY l.name`).
		Bind(dbx.Params{"id": cycleID}).
		All(&rows)
	if err != nil {
		return []any{}
	}
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"label_id":         row.LabelID,
			"label_name":       row.LabelName,
			"color":            nullableString(row.Color),
			"total_issues":     row.TotalIssues,
			"completed_issues": row.CompletedIssues,
			"pending_issues":   row.TotalIssues - row.CompletedIssues,
		})
	}
	return out
}

// orIDFilter builds `(col = {:k0} || col = {:k1} …)` and registers the params.
// An empty list yields an always-false predicate.
func orIDFilter(column string, ids []string, params dbx.Params) string {
	if len(ids) == 0 {
		params["none"] = ""
		return column + " = {:none}"
	}
	parts := make([]string, 0, len(ids))
	for i, id := range ids {
		key := "f" + strconv.Itoa(i)
		parts = append(parts, column+" = {:"+key+"}")
		params[key] = id
	}
	return "(" + strings.Join(parts, " || ") + ")"
}

// datePart keeps only the YYYY-MM-DD portion of an ISO-ish date string.
func datePart(value string) string {
	if len(value) >= 10 {
		return value[:10]
	}
	return value
}

// dateTimePart renders a PocketBase date column as YYYY-MM-DD.
func dateTimePart(r *core.Record, field string) string {
	dt := r.GetDateTime(field)
	if dt.IsZero() {
		return ""
	}
	return dt.Time().UTC().Format("2006-01-02")
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	}
	return 0
}

func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func valueOr(v, fallback any) any {
	if v == nil {
		return fallback
	}
	return v
}
