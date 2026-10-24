// Package module implements Plane's module endpoints for a project:
//
//	GET/POST  /api/workspaces/{slug}/projects/{project_id}/modules/   (plain array)
//	GET/PUT/PATCH/DELETE …/modules/{pk}/
//	GET/POST/DELETE …/modules/{module_id}/archive/
//	GET       …/archived-modules/       GET …/archived-modules/{pk}/
//	GET/POST  …/user-favorite-modules/  DELETE …/user-favorite-modules/{module_id}/
//	GET/PATCH …/modules/{module_id}/user-properties/
//	GET       /api/workspaces/{slug}/modules/   (workspace-wide plain array)
//
// The module-issue and module-link sub-resources live in issue.go / link.go.
//
// Wire-shape note: like cycles, ModuleViewSet.list returns a plain JSON array
// (frontend `ModuleService.projectModulesList` is typed `IModule[]`), so the
// api-spec's generic paginated-envelope response_shape does not apply here.
package module

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Handler holds the app dependency for the module routes.
type Handler struct {
	App core.App
}

// New returns a module handler bound to app.
func New(app core.App) *Handler {
	return &Handler{App: app}
}

// List serves GET …/modules/ — a plain array ordered by
// `-is_favorite, -created_at`. Readable by every project role.
func (h *Handler) List(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	modules, err := h.App.FindRecordsByFilter(
		"modules",
		"project = {:project} && workspace = {:workspace} && deleted_at = '' && archived_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"project": scope.Project.Id, "workspace": scope.Workspace.Id},
	)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, h.serializeList(e, scope, modules))
}

// WorkspaceList serves GET /api/workspaces/{slug}/modules/
// (WorkspaceModulesEndpoint): modules in every project the caller belongs to.
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
		orIDFilter("project", "p", projectIDs, params)
	modules, err := h.App.FindRecordsByFilter("modules", filter, "-created_at", 0, 0, params)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, h.serializeList(e, scope, modules))
}

// Create serves POST …/modules/ (ModuleViewSet.create). ADMIN or MEMBER only.
func (h *Handler) Create(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
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
	targetDate := asString(body["target_date"])
	if startDate != "" && targetDate != "" && startDate > targetDate {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Start date cannot exceed target date"})
	}
	// Plane rejects a duplicate module name within the project.
	if existing, _ := h.App.FindFirstRecordByFilter(
		"modules",
		"project = {:project} && name = {:name} && deleted_at = ''",
		dbx.Params{"project": scope.Project.Id, "name": name},
	); existing != nil {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Module with this name already exists"})
	}

	collection, err := h.App.FindCollectionByNameOrId("modules")
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
		record.Set("created_by", user.Id)
		record.Set("sort_order", h.nextSortOrder(txApp, scope.Project.Id))
		record.Set("status", defaultStatus(asString(body["status"])))
		for _, field := range []string{"description", "description_text", "description_html", "view_props", "external_source", "external_id", "logo_props"} {
			if v, ok := body[field]; ok {
				record.Set(field, v)
			}
		}
		if startDate != "" {
			record.Set("start_date", startDate)
		}
		if targetDate != "" {
			record.Set("target_date", targetDate)
		}
		if lead := asString(body["lead_id"]); lead != "" {
			if u, _ := serializers.FindByUUID(txApp, "users", lead); u != nil {
				record.Set("lead", u.Id)
			}
		}
		if err := txApp.Save(record); err != nil {
			return err
		}
		if err := h.replaceMembers(txApp, scope, record, uuidList(body, "member_ids"), user.Id); err != nil {
			return err
		}
		created = record
		return nil
	})
	if err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusCreated, serializers.Module(h.App, created, false, false))
}

// Detail serves GET …/modules/{pk}/ (ModuleViewSet.retrieve). ADMIN/MEMBER.
func (h *Handler) Detail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	record, err := h.findModule(scope, e.Request.PathValue("pk"))
	if err != nil {
		return err
	}
	if record == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Module not found"})
	}
	return e.JSON(http.StatusOK, serializers.Module(h.App, record, h.isFavorite(e, scope, serializers.UUID(record)), true))
}

// Update serves PUT/PATCH …/modules/{pk}/ (ModuleViewSet.partial_update).
func (h *Handler) Update(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	record, err := h.findModuleIncludingArchived(scope, e.Request.PathValue("pk"))
	if err != nil {
		return err
	}
	if record == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Module not found"})
	}
	if record.GetString("archived_at") != "" {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Archived module cannot be updated"})
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	if name := strings.TrimSpace(asString(body["name"])); name != "" {
		if existing, _ := h.App.FindFirstRecordByFilter(
			"modules",
			"project = {:project} && name = {:name} && deleted_at = '' && id != {:id}",
			dbx.Params{"project": scope.Project.Id, "name": name, "id": record.Id},
		); existing != nil {
			return e.JSON(http.StatusBadRequest, map[string]any{"error": "Module with this name already exists"})
		}
	}
	startDate := record.GetDateTime("start_date").Time()
	targetDate := record.GetDateTime("target_date").Time()
	newStart := asString(body["start_date"])
	newTarget := asString(body["target_date"])
	if newStart != "" {
		startDate = parseDate(newStart)
	}
	if newTarget != "" {
		targetDate = parseDate(newTarget)
	}
	if !startDate.IsZero() && !targetDate.IsZero() && startDate.After(targetDate) {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Start date cannot exceed target date"})
	}

	for _, field := range []string{
		"name", "description", "description_text", "description_html", "status",
		"view_props", "sort_order", "external_source", "external_id", "logo_props",
	} {
		if v, ok := body[field]; ok {
			record.Set(field, v)
		}
	}
	if newStart != "" {
		record.Set("start_date", newStart)
	}
	if newTarget != "" {
		record.Set("target_date", newTarget)
	}
	if lead := asString(body["lead_id"]); lead != "" {
		if u, _ := serializers.FindByUUID(h.App, "users", lead); u != nil {
			record.Set("lead", u.Id)
		}
	} else if _, ok := body["lead_id"]; ok {
		record.Set("lead", nil)
	}
	if user := middleware.CurrentUser(e); user != nil {
		record.Set("updated_by", user.Id)
	}
	if err := h.App.Save(record); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	if _, ok := body["member_ids"]; ok {
		if err := h.replaceMembers(h.App, scope, record, uuidList(body, "member_ids"), record.GetString("created_by")); err != nil {
			return err
		}
	}
	return e.JSON(http.StatusOK, serializers.Module(h.App, record, h.isFavorite(e, scope, serializers.UUID(record)), false))
}

// Delete serves DELETE …/modules/{pk}/ (ModuleViewSet.destroy). ADMIN, or the
// creator. Soft-deletes, then removes the module-issue links and favourites.
func (h *Handler) Delete(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	record, err := h.findModuleIncludingArchived(scope, e.Request.PathValue("pk"))
	if err != nil {
		return err
	}
	if record == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	if !middleware.IsCreator(e, record) && !middleware.ProjectRoleAtLeast(e, middleware.RoleAdmin) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	record.Set("deleted_at", serializers.Now())
	if err := h.App.Save(record); err != nil {
		return err
	}
	h.deleteModuleIssues(record.Id)
	h.clearFavorites(scope, serializers.UUID(record))
	return e.NoContent(http.StatusNoContent)
}

// FavoritesList serves GET …/user-favorite-modules/. Any project member.
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
		"user = {:user} && project = {:project} && entity_type = 'module' && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"user": user.Id, "project": scope.Project.Id},
	)
	if err != nil {
		return err
	}
	results := make([]any, 0, len(favorites))
	for _, fav := range favorites {
		record, _ := serializers.FindByUUID(h.App, "modules", fav.GetString("entity_identifier"))
		if record == nil || record.GetString("deleted_at") != "" {
			continue
		}
		results = append(results, serializers.Module(h.App, record, true, false))
	}
	return e.JSON(http.StatusOK, results)
}

// FavoriteCreate serves POST …/user-favorite-modules/ (body `{module: <uuid>}`).
// Any project member (ModuleFavoriteViewSet uses ProjectLitePermission).
func (h *Handler) FavoriteCreate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	moduleUUID := asString(body["module"])
	if moduleUUID == "" {
		moduleUUID = asString(body["module_id"])
	}
	if moduleUUID == "" {
		return serializers.BadRequest(e, "Module is required")
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
	record.Set("entity_type", "module")
	record.Set("entity_identifier", moduleUUID)
	record.Set("created_by", user.Id)
	if err := h.App.Save(record); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// FavoriteDestroy serves DELETE …/user-favorite-modules/{module_id}/.
func (h *Handler) FavoriteDestroy(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	h.deleteFavorite(user.Id, scope.Project.Id, "module", e.Request.PathValue("module_id"))
	return e.NoContent(http.StatusNoContent)
}

// UserPropertiesGet serves GET …/modules/{module_id}/user-properties/.
func (h *Handler) UserPropertiesGet(e *core.RequestEvent) error {
	record, err := h.userProperties(e, e.Request.PathValue("module_id"))
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, serializers.ModuleUserProperties(h.App, record))
}

// UserPropertiesPatch serves PATCH …/modules/{module_id}/user-properties/.
func (h *Handler) UserPropertiesPatch(e *core.RequestEvent) error {
	record, err := h.userProperties(e, e.Request.PathValue("module_id"))
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
	return e.JSON(http.StatusCreated, serializers.ModuleUserProperties(h.App, record))
}

// ArchiveGet serves GET …/modules/{module_id}/archive/.
func (h *Handler) ArchiveGet(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	record, err := h.findModuleIncludingArchived(scope, e.Request.PathValue("module_id"))
	if err != nil {
		return err
	}
	if record == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Module not found"})
	}
	return e.JSON(http.StatusOK, serializers.Module(h.App, record, h.isFavorite(e, scope, serializers.UUID(record)), true))
}

// Archive serves POST …/modules/{module_id}/archive/: only a completed or
// cancelled module can be archived.
func (h *Handler) Archive(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	record, err := h.findModuleIncludingArchived(scope, e.Request.PathValue("module_id"))
	if err != nil {
		return err
	}
	if record == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Module not found"})
	}
	status := record.GetString("status")
	if status != "completed" && status != "cancelled" {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Only completed or cancelled modules can be archived"})
	}
	archivedAt := serializers.Now()
	record.Set("archived_at", archivedAt)
	if err := h.App.Save(record); err != nil {
		return err
	}
	h.clearFavorites(scope, serializers.UUID(record))
	return e.JSON(http.StatusOK, map[string]any{"archived_at": serializers.Time(archivedAt)})
}

// Unarchive serves DELETE …/modules/{module_id}/archive/.
func (h *Handler) Unarchive(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	record, err := h.findModuleIncludingArchived(scope, e.Request.PathValue("module_id"))
	if err != nil {
		return err
	}
	if record == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Module not found"})
	}
	record.Set("archived_at", nil)
	if err := h.App.Save(record); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// ArchivedList serves GET …/archived-modules/ (plain array). Any project member.
func (h *Handler) ArchivedList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	modules, err := h.App.FindRecordsByFilter(
		"modules",
		"project = {:project} && deleted_at = '' && archived_at != ''",
		"-created_at", 0, 0,
		dbx.Params{"project": scope.Project.Id},
	)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, h.serializeList(e, scope, modules))
}

// ArchivedDetail serves GET …/archived-modules/{pk}/.
func (h *Handler) ArchivedDetail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	record, err := h.findModuleIncludingArchived(scope, e.Request.PathValue("pk"))
	if err != nil {
		return err
	}
	if record == nil || record.GetString("archived_at") == "" {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Module not found"})
	}
	return e.JSON(http.StatusOK, serializers.Module(h.App, record, h.isFavorite(e, scope, serializers.UUID(record)), true))
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

// findModule resolves a non-deleted, non-archived module within the project.
func (h *Handler) findModule(scope *middleware.Scope, uuid string) (*core.Record, error) {
	record, err := serializers.FindByUUID(h.App, "modules", uuid)
	if err != nil || record == nil {
		return nil, err
	}
	if record.GetString("project") != scope.Project.Id || record.GetString("deleted_at") != "" || record.GetString("archived_at") != "" {
		return nil, nil
	}
	return record, nil
}

// findModuleIncludingArchived resolves a non-deleted module regardless of
// archive state.
func (h *Handler) findModuleIncludingArchived(scope *middleware.Scope, uuid string) (*core.Record, error) {
	record, err := serializers.FindByUUID(h.App, "modules", uuid)
	if err != nil || record == nil {
		return nil, err
	}
	if record.GetString("project") != scope.Project.Id || record.GetString("deleted_at") != "" {
		return nil, nil
	}
	return record, nil
}

// serializeList renders modules with the caller's favourite flags applied and
// favourites ordered first.
func (h *Handler) serializeList(e *core.RequestEvent, scope *middleware.Scope, modules []*core.Record) []any {
	favorites := map[string]bool{}
	if user := middleware.CurrentUser(e); user != nil && scope.Project != nil {
		favorites = h.favoriteIDs(user.Id, scope.Project.Id)
	}
	fav := make([]any, 0, len(modules))
	rest := make([]any, 0, len(modules))
	for _, m := range modules {
		isFav := favorites[serializers.UUID(m)]
		item := serializers.Module(h.App, m, isFav, false)
		if isFav {
			fav = append(fav, item)
		} else {
			rest = append(rest, item)
		}
	}
	return append(fav, rest...)
}

// favoriteIDs returns the module UUIDs the user favourited in the project.
func (h *Handler) favoriteIDs(userID, projectID string) map[string]bool {
	out := map[string]bool{}
	records, err := h.App.FindRecordsByFilter(
		"user_favorites",
		"user = {:user} && project = {:project} && entity_type = 'module' && deleted_at = ''",
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

// isFavorite reports whether the caller favourited the module.
func (h *Handler) isFavorite(e *core.RequestEvent, scope *middleware.Scope, moduleUUID string) bool {
	user := middleware.CurrentUser(e)
	if user == nil || scope.Project == nil {
		return false
	}
	record, err := h.App.FindFirstRecordByFilter(
		"user_favorites",
		"user = {:user} && project = {:project} && entity_type = 'module' && entity_identifier = {:module} && deleted_at = ''",
		dbx.Params{"user": user.Id, "project": scope.Project.Id, "module": moduleUUID},
	)
	return err == nil && record != nil
}

// clearFavorites hard-deletes every favourite pointing at the module.
func (h *Handler) clearFavorites(scope *middleware.Scope, moduleUUID string) {
	records, err := h.App.FindRecordsByFilter(
		"user_favorites",
		"project = {:project} && entity_type = 'module' && entity_identifier = {:module}",
		"", 0, 0,
		dbx.Params{"project": scope.Project.Id, "module": moduleUUID},
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

// deleteModuleIssues removes the module's issue links (ModuleViewSet.destroy).
func (h *Handler) deleteModuleIssues(moduleID string) {
	links, err := h.App.FindRecordsByFilter(
		"module_issues",
		"module = {:module} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"module": moduleID},
	)
	if err != nil {
		return
	}
	for _, link := range links {
		_ = h.App.Delete(link)
	}
}

// replaceMembers rewrites `module_members` for the module. An empty list clears
// membership.
func (h *Handler) replaceMembers(app core.App, scope *middleware.Scope, module *core.Record, memberUUIDs []string, actorID string) error {
	existing, err := app.FindRecordsByFilter(
		"module_members",
		"module = {:module} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"module": module.Id},
	)
	if err != nil {
		return err
	}
	for _, row := range existing {
		if err := app.Delete(row); err != nil {
			return err
		}
	}
	if len(memberUUIDs) == 0 {
		return nil
	}
	collection, err := app.FindCollectionByNameOrId("module_members")
	if err != nil {
		return err
	}
	for _, userUUID := range memberUUIDs {
		member, _ := serializers.FindByUUID(app, "users", userUUID)
		if member == nil {
			continue
		}
		row := core.NewRecord(collection)
		row.Set("uuid", serializers.NewUUID())
		row.Set("module", module.Id)
		row.Set("member", member.Id)
		row.Set("project", scope.Project.Id)
		row.Set("workspace", scope.Workspace.Id)
		if actorID != "" {
			row.Set("created_by", actorID)
		}
		if err := app.Save(row); err != nil {
			return err
		}
	}
	return nil
}

// userProperties returns (creating if needed) the caller's properties row.
func (h *Handler) userProperties(e *core.RequestEvent, moduleUUID string) (*core.Record, error) {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return nil, serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return nil, serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	module, _ := serializers.FindByUUID(h.App, "modules", moduleUUID)
	if module == nil || module.GetString("project") != scope.Project.Id || module.GetString("deleted_at") != "" {
		return nil, serializers.NotFound(e, "")
	}
	existing, err := h.App.FindFirstRecordByFilter(
		"module_user_properties",
		"user = {:user} && module = {:module} && project = {:project} && deleted_at = ''",
		dbx.Params{"user": user.Id, "module": module.Id, "project": scope.Project.Id},
	)
	if err == nil && existing != nil {
		return existing, nil
	}
	collection, err := h.App.FindCollectionByNameOrId("module_user_properties")
	if err != nil {
		return nil, err
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("user", user.Id)
	record.Set("module", module.Id)
	record.Set("project", scope.Project.Id)
	record.Set("workspace", scope.Workspace.Id)
	record.Set("created_by", user.Id)
	if err := h.App.Save(record); err != nil {
		return nil, err
	}
	return record, nil
}

// nextSortOrder mirrors Module.save(): `min(sort_order) - 10000`, or 65535.
func (h *Handler) nextSortOrder(app core.App, projectID string) float64 {
	var min sql.NullFloat64
	err := app.DB().
		NewQuery("SELECT MIN(sort_order) FROM modules WHERE project = {:project} AND deleted_at = ''").
		Bind(dbx.Params{"project": projectID}).
		Row(&min)
	if err != nil || !min.Valid {
		return 65535
	}
	return min.Float64 - 10000
}

// memberProjectIDs returns the projects the user is an active member of.
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

// orIDFilter builds `(col = {:<prefix>0} || col = {:<prefix>1} …)` and
// registers the params. An empty list yields an always-false predicate. The
// prefix keeps multiple uses of the helper in one query from colliding on
// param names (named params collapse to their last value).
func orIDFilter(column, prefix string, ids []string, params dbx.Params) string {
	if len(ids) == 0 {
		params[prefix+"none"] = ""
		return column + " = {:" + prefix + "none}"
	}
	parts := make([]string, 0, len(ids))
	for i, id := range ids {
		key := prefix + strconv.Itoa(i)
		parts = append(parts, column+" = {:"+key+"}")
		params[key] = id
	}
	return "(" + strings.Join(parts, " || ") + ")"
}

func defaultStatus(value string) string {
	if value == "" {
		return "planned"
	}
	return value
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func uuidList(body map[string]any, key string) []string {
	raw, ok := body[key]
	if !ok {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func parseDate(value string) time.Time {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
