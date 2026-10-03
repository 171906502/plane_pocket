// Package view implements Plane's saved-view, favourite, sticky and
// recent-visit endpoints:
//
//	GET/POST      …/projects/{project_id}/views/              (IssueViewViewSet)
//	GET/PUT/PATCH/DELETE …/projects/{project_id}/views/{pk}/
//	POST          …/projects/{project_id}/project-views/      (ProjectUserViewsEndpoint)
//	GET/POST      …/projects/{project_id}/user-favorite-views/ (IssueViewFavoriteViewSet)
//	DELETE        …/projects/{project_id}/user-favorite-views/{view_id}/
//	GET/POST      /api/workspaces/{slug}/views/               (WorkspaceViewViewSet)
//	GET/PUT/PATCH/DELETE /api/workspaces/{slug}/views/{pk}/
//	POST          /api/workspaces/{slug}/workspace-views/     (WorkspaceMemberUserViewsEndpoint)
//	GET/POST/PATCH/DELETE /api/workspaces/{slug}/user-favorites/        (WorkspaceFavoriteEndpoint)
//	…/user-favorites/{favorite_id}/  …/user-favorites/{favorite_id}/group/
//	GET/POST/DELETE /api/workspaces/{slug}/user-favorite-projects/      (ProjectFavoritesViewSet)
//	GET           /api/workspaces/{slug}/recent-visits/       (UserRecentVisitViewSet)
//	GET/POST      /api/workspaces/{slug}/stickies/            (WorkspaceStickyViewSet)
//	GET/PATCH/DELETE /api/workspaces/{slug}/stickies/{pk}/
//
// Views, favourites, stickies and recent visits are all per-user: a row that
// belongs to someone else is a 404, never a 403, and an absent workspace
// membership is a 404 (enforced upstream by ResolveWorkspace/ResolveProject).
package view

import (
	"database/sql"
	"net/http"
	"sort"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Handler holds the app dependency for the view-domain routes.
type Handler struct {
	App core.App
}

// New returns a view handler bound to app.
func New(app core.App) *Handler {
	return &Handler{App: app}
}

// ---------------------------------------------------------------------------
// Project-scoped saved views (IssueViewViewSet)
// ---------------------------------------------------------------------------

// ProjectList serves GET …/projects/{project_id}/views/ — a plain array ordered
// by `-is_favorite, name`. Guests see only their own views unless the project
// has `guest_view_all_features`.
func (h *Handler) ProjectList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	filter := "workspace = {:workspace} && project = {:project} && deleted_at = '' && (owned_by = {:user} || access = 1)"
	params := dbx.Params{
		"workspace": scope.Workspace.Id,
		"project":   scope.Project.Id,
		"user":      user.Id,
	}
	if scope.ProjectRole == middleware.RoleGuest && !scope.Project.GetBool("guest_view_all_features") {
		filter += " && owned_by = {:user}"
	}

	records, err := h.App.FindRecordsByFilter("issue_views", filter, "", 0, 0, params)
	if err != nil {
		return err
	}
	favorites := h.viewFavoriteIDs(user.Id, scope.Project.Id)
	return e.JSON(http.StatusOK, h.serializeViews(records, favorites))
}

// ProjectCreate serves POST …/projects/{project_id}/views/ (201 + the view).
func (h *Handler) ProjectCreate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	record, err := h.createView(e, scope, user, true)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusCreated, serializers.IssueView(h.App, record, false))
}

// ProjectDetail serves GET …/projects/{project_id}/views/{pk}/.
func (h *Handler) ProjectDetail(e *core.RequestEvent) error {
	record, err := h.findProjectView(e)
	if err != nil {
		return err
	}
	if record == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	return e.JSON(http.StatusOK, serializers.IssueView(h.App, record, h.isViewFavorite(e, record)))
}

// ProjectUpdate serves PUT/PATCH …/projects/{project_id}/views/{pk}/.
func (h *Handler) ProjectUpdate(e *core.RequestEvent) error {
	return h.updateView(e, true)
}

// ProjectDestroy serves DELETE …/projects/{project_id}/views/{pk}/.
func (h *Handler) ProjectDestroy(e *core.RequestEvent) error {
	return h.destroyView(e, true)
}

// ---------------------------------------------------------------------------
// Workspace-level saved views (WorkspaceViewViewSet)
// ---------------------------------------------------------------------------

// WorkspaceList serves GET /api/workspaces/{slug}/views/ — a plain array of
// views with `project` null, ordered by `order_by` (default `-created_at`).
func (h *Handler) WorkspaceList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	filter := "workspace = {:workspace} && project = '' && deleted_at = '' && (owned_by = {:user} || access = 1)"
	params := dbx.Params{"workspace": scope.Workspace.Id, "user": user.Id}
	if scope.WorkspaceRole == middleware.RoleGuest {
		filter += " && owned_by = {:user}"
	}

	records, err := h.App.FindRecordsByFilter(
		"issue_views", filter, viewOrderBy(e.Request.URL.Query().Get("order_by")), 0, 0, params,
	)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, h.serializeViews(records, map[string]bool{}))
}

// WorkspaceCreate serves POST /api/workspaces/{slug}/views/ (201 + the view).
func (h *Handler) WorkspaceCreate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	record, err := h.createView(e, scope, user, false)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusCreated, serializers.IssueView(h.App, record, false))
}

// WorkspaceDetail serves GET /api/workspaces/{slug}/views/{pk}/.
func (h *Handler) WorkspaceDetail(e *core.RequestEvent) error {
	record, err := h.findWorkspaceView(e)
	if err != nil {
		return err
	}
	if record == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	return e.JSON(http.StatusOK, serializers.IssueView(h.App, record, false))
}

// WorkspaceUpdate serves PUT/PATCH /api/workspaces/{slug}/views/{pk}/.
func (h *Handler) WorkspaceUpdate(e *core.RequestEvent) error {
	return h.updateView(e, false)
}

// WorkspaceDestroy serves DELETE /api/workspaces/{slug}/views/{pk}/.
func (h *Handler) WorkspaceDestroy(e *core.RequestEvent) error {
	return h.destroyView(e, false)
}

// ---------------------------------------------------------------------------
// Per-user view properties (ProjectUserViewsEndpoint / WorkspaceMemberUserViewsEndpoint)
// ---------------------------------------------------------------------------

// ProjectViewsUpdate serves POST …/projects/{project_id}/project-views/: it
// stores the caller's project-member view properties and answers 204.
func (h *Handler) ProjectViewsUpdate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectMember == nil {
		return serializers.Forbidden(e, "Forbidden")
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	member := scope.ProjectMember
	for _, field := range []string{"view_props", "default_props", "preferences", "sort_order"} {
		if v, ok := body[field]; ok {
			member.Set(field, v)
		}
	}
	if err := h.App.Save(member); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.NoContent(http.StatusNoContent)
}

// WorkspaceViewsUpdate serves POST /api/workspaces/{slug}/workspace-views/: it
// stores the caller's workspace-member view props and answers 204.
func (h *Handler) WorkspaceViewsUpdate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if scope.WorkspaceMember == nil {
		return serializers.Forbidden(e, "Forbidden")
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	member := scope.WorkspaceMember
	if v, ok := body["view_props"]; ok {
		member.Set("view_props", v)
	}
	if err := h.App.Save(member); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.NoContent(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// saved-view shared helpers
// ---------------------------------------------------------------------------

// createView inserts an IssueView. Project-scoped views require a project; the
// workspace-scoped variant leaves `project` empty.
func (h *Handler) createView(e *core.RequestEvent, scope *middleware.Scope, user *core.Record, projectScoped bool) (*core.Record, error) {
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return nil, serializers.BadRequest(e, "Invalid request body.")
	}
	name := strings.TrimSpace(asString(body["name"]))
	if name == "" {
		return nil, serializers.Validation(e, "name", "This field is required.")
	}

	collection, err := h.App.FindCollectionByNameOrId("issue_views")
	if err != nil {
		return nil, err
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("workspace", scope.Workspace.Id)
	record.Set("owned_by", user.Id)
	record.Set("created_by", user.Id)
	record.Set("name", name)
	if projectScoped {
		record.Set("project", scope.Project.Id)
	}
	for _, field := range []string{"description", "display_filters", "display_properties", "rich_filters", "logo_props"} {
		if v, ok := body[field]; ok {
			record.Set(field, v)
		}
	}
	filters := asMap(body["filters"])
	record.Set("filters", filters)
	record.Set("query", issueQueryFromFilters(filters))

	access := 1
	if v, ok := body["access"]; ok {
		access = toInt(v)
	}
	record.Set("access", access)

	record.Set("sort_order", h.nextViewSortOrder(scope, projectScoped))
	if err := h.App.Save(record); err != nil {
		return nil, serializers.Validation(e, "detail", err.Error())
	}
	return record, nil
}

// updateView applies a partial update to a saved view, enforcing the lock and
// owner-only rules IssueViewViewSet applies before saving.
func (h *Handler) updateView(e *core.RequestEvent, projectScoped bool) error {
	var record *core.Record
	var err error
	if projectScoped {
		record, err = h.findProjectView(e)
	} else {
		record, err = h.findWorkspaceView(e)
	}
	if err != nil {
		return err
	}
	if record == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	user := middleware.CurrentUser(e)
	if record.GetBool("is_locked") {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "view is locked"})
	}
	if user == nil || record.GetString("owned_by") != user.Id {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Only the owner of the view can update the view"})
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	for _, field := range []string{"name", "description", "display_filters", "display_properties", "rich_filters", "logo_props", "access"} {
		if v, ok := body[field]; ok {
			record.Set(field, v)
		}
	}
	if _, ok := body["filters"]; ok {
		filters := asMap(body["filters"])
		record.Set("filters", filters)
		record.Set("query", issueQueryFromFilters(filters))
	}
	record.Set("updated_by", user.Id)
	if err := h.App.Save(record); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.IssueView(h.App, record, h.isViewFavorite(e, record)))
}

// destroyView soft-deletes a view and hard-deletes its favourites (and, for
// project views, its recent visits) — mirroring IssueViewViewSet.destroy.
func (h *Handler) destroyView(e *core.RequestEvent, projectScoped bool) error {
	var record *core.Record
	var err error
	if projectScoped {
		record, err = h.findProjectView(e)
	} else {
		record, err = h.findWorkspaceView(e)
	}
	if err != nil {
		return err
	}
	if record == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	scope := middleware.ScopeOf(e)
	isAdmin := false
	if projectScoped {
		isAdmin = scope.ProjectRole >= middleware.RoleAdmin
	} else {
		isAdmin = scope.WorkspaceRole >= middleware.RoleAdmin
	}
	if !isAdmin && record.GetString("owned_by") != user.Id {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Only admin or owner can delete the view"})
	}

	record.Set("deleted_at", serializers.Now())
	if err := h.App.Save(record); err != nil {
		return err
	}
	h.clearViewFavorites(scope, record)
	if projectScoped {
		h.clearViewRecentVisits(scope, record)
	}
	return e.NoContent(http.StatusNoContent)
}

// findProjectView resolves a non-deleted view in the resolved project that the
// caller may see (owner or public).
func (h *Handler) findProjectView(e *core.RequestEvent) (*core.Record, error) {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Project == nil {
		return nil, serializers.NotFound(e, "")
	}
	record, err := serializers.FindByUUID(h.App, "issue_views", e.Request.PathValue("pk"))
	if err != nil || record == nil {
		return nil, err
	}
	if record.GetString("project") != scope.Project.Id ||
		record.GetString("workspace") != scope.Workspace.Id ||
		record.GetString("deleted_at") != "" {
		return nil, nil
	}
	if user == nil || (record.GetString("owned_by") != user.Id && record.GetInt("access") != 1) {
		return nil, nil
	}
	return record, nil
}

// findWorkspaceView resolves a non-deleted workspace-level view (project null).
func (h *Handler) findWorkspaceView(e *core.RequestEvent) (*core.Record, error) {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return nil, serializers.NotFound(e, "")
	}
	record, err := serializers.FindByUUID(h.App, "issue_views", e.Request.PathValue("pk"))
	if err != nil || record == nil {
		return nil, err
	}
	if record.GetString("project") != "" ||
		record.GetString("workspace") != scope.Workspace.Id ||
		record.GetString("deleted_at") != "" {
		return nil, nil
	}
	if user == nil || (record.GetString("owned_by") != user.Id && record.GetInt("access") != 1) {
		return nil, nil
	}
	return record, nil
}

// serializeViews renders a view list with `is_favorite` and the
// `-is_favorite, name` ordering the project viewset applies.
func (h *Handler) serializeViews(records []*core.Record, favorites map[string]bool) []any {
	type item struct {
		isFavorite bool
		name       string
		data       map[string]any
	}
	items := make([]item, 0, len(records))
	for _, record := range records {
		isFav := favorites[serializers.UUID(record)]
		items = append(items, item{
			isFavorite: isFav,
			name:       strings.ToLower(record.GetString("name")),
			data:       serializers.IssueView(h.App, record, isFav),
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].isFavorite != items[j].isFavorite {
			return items[i].isFavorite
		}
		return items[i].name < items[j].name
	})
	out := make([]any, 0, len(items))
	for _, it := range items {
		out = append(out, it.data)
	}
	return out
}

// nextViewSortOrder mirrors IssueView.save(): max(sort_order) + 10000, scoped to
// the project when project-scoped, else to (workspace, project IS NULL).
func (h *Handler) nextViewSortOrder(scope *middleware.Scope, projectScoped bool) float64 {
	var max sql.NullFloat64
	var err error
	if projectScoped {
		err = h.App.DB().
			NewQuery("SELECT MAX(sort_order) FROM issue_views WHERE project = {:project} AND deleted_at = ''").
			Bind(dbx.Params{"project": scope.Project.Id}).
			Row(&max)
	} else {
		err = h.App.DB().
			NewQuery("SELECT MAX(sort_order) FROM issue_views WHERE workspace = {:workspace} AND project = '' AND deleted_at = ''").
			Bind(dbx.Params{"workspace": scope.Workspace.Id}).
			Row(&max)
	}
	if err != nil || !max.Valid {
		return 65535
	}
	return max.Float64 + 10000
}

// clearViewFavorites hard-deletes every favourite pointing at the view.
func (h *Handler) clearViewFavorites(scope *middleware.Scope, record *core.Record) {
	rows, err := h.App.FindRecordsByFilter(
		"user_favorites",
		"workspace = {:workspace} && entity_type = 'view' && entity_identifier = {:id}",
		"", 0, 0,
		dbx.Params{"workspace": scope.Workspace.Id, "id": serializers.UUID(record)},
	)
	if err != nil {
		return
	}
	for _, row := range rows {
		_ = h.App.Delete(row)
	}
}

// clearViewRecentVisits hard-deletes every recent visit pointing at the view.
func (h *Handler) clearViewRecentVisits(scope *middleware.Scope, record *core.Record) {
	rows, err := h.App.FindRecordsByFilter(
		"user_recent_visits",
		"workspace = {:workspace} && entity_name = 'view' && entity_identifier = {:id}",
		"", 0, 0,
		dbx.Params{"workspace": scope.Workspace.Id, "id": serializers.UUID(record)},
	)
	if err != nil {
		return
	}
	for _, row := range rows {
		_ = h.App.Delete(row)
	}
}

// viewOrderBy maps the workspace-view order_by allowlist onto a PocketBase sort.
func viewOrderBy(param string) string {
	switch param {
	case "name":
		return "name"
	case "-name":
		return "-name"
	case "created_at":
		return "created_at"
	case "updated_at":
		return "updated_at"
	case "-updated_at":
		return "-updated_at"
	default:
		return "-created_at"
	}
}

// issueQueryFromFilters ports the POST branch of plane.utils.issue_filters: it
// turns the frontend `filters` object into the Django-lookup dict Django stores
// in IssueView.query. It covers the keys Plane's view UI sends; `query` is a
// stored snapshot the frontends do not re-read, so unsupported keys are skipped.
func issueQueryFromFilters(filters map[string]any) map[string]any {
	query := map[string]any{}
	if filters == nil {
		return query
	}
	for key, raw := range filters {
		switch key {
		case "state":
			if v := filterValue(raw); v != nil {
				query["state__in"] = v
			}
		case "state_group":
			if v := filterValue(raw); v != nil {
				query["state__group__in"] = v
			}
		case "estimate_point":
			if v := filterValue(raw); v != nil {
				query["estimate_point__in"] = v
			}
		case "priority":
			if v := filterValue(raw); v != nil {
				query["priority__in"] = v
			}
		case "parent":
			if v := filterValue(raw); v != nil {
				query["parent__in"] = v
			}
		case "labels":
			if v := filterValue(raw); v != nil {
				query["labels__in"] = v
			}
			query["label_issue__deleted_at__isnull"] = true
		case "assignees":
			if v := filterValue(raw); v != nil {
				query["assignees__in"] = v
			}
			query["issue_assignee__deleted_at__isnull"] = true
		case "created_by":
			if v := filterValue(raw); v != nil {
				query["created_by__in"] = v
			}
		case "name":
			if s := asString(raw); s != "" {
				query["name__icontains"] = s
			}
		case "start_date":
			if v := filterValue(raw); v != nil {
				query["start_date"] = v
			}
		case "target_date":
			if v := filterValue(raw); v != nil {
				query["target_date"] = v
			}
		case "cycle":
			if v := filterValue(raw); v != nil {
				query["issue_cycle__cycle_id__in"] = v
			}
			query["issue_cycle__deleted_at__isnull"] = true
		case "module":
			if v := filterValue(raw); v != nil {
				query["issue_module__module_id__in"] = v
			}
			query["issue_module__deleted_at__isnull"] = true
		case "subscriber":
			if v := filterValue(raw); v != nil {
				query["issue_subscribers__subscriber_id__in"] = v
			}
			query["issue_subscribers__deleted_at__isnull"] = true
		case "project":
			if v := filterValue(raw); v != nil {
				query["project__in"] = v
			}
		case "sub_issue":
			if sub, ok := raw.(bool); ok && !sub {
				query["parent__isnull"] = true
			} else if s, ok := raw.(string); ok && (s == "false" || s == "") {
				query["parent__isnull"] = true
			}
		}
	}
	return query
}

// filterValue returns a filter value unless it is empty or the "null" sentinel.
func filterValue(raw any) any {
	switch value := raw.(type) {
	case nil:
		return nil
	case string:
		if value == "" || value == "null" {
			return nil
		}
		return value
	case []any:
		if len(value) == 0 {
			return nil
		}
		return value
	default:
		return raw
	}
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toInt(v any) int {
	switch value := v.(type) {
	case float64:
		return int(value)
	case int:
		return value
	case string:
		if value == "0" {
			return 0
		}
		if value == "1" {
			return 1
		}
	}
	return 1
}
