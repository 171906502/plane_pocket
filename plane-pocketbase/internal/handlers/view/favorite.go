package view

import (
	"database/sql"
	"net/http"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// ---------------------------------------------------------------------------
// Workspace favourites (WorkspaceFavoriteEndpoint / WorkspaceFavoriteGroupEndpoint)
// ---------------------------------------------------------------------------

// FavoritesList serves GET /api/workspaces/{slug}/user-favorites/.
//
// Wire shape: Django's WorkspaceFavoriteEndpoint is a BaseAPIView, so it returns
// a plain JSON array (not the paginated envelope the api-spec's generic
// `response_shape` suggests); Plane's web client iterates it with `.forEach`,
// so the array shape is the one that must hold.
func (h *Handler) FavoritesList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	rows, err := h.App.FindRecordsByFilter(
		"user_favorites",
		"user = {:user} && workspace = {:workspace} && parent = '' && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"user": user.Id, "workspace": scope.Workspace.Id},
	)
	if err != nil {
		return err
	}

	projectIDs := h.memberProjectIDs(user.Id, scope.Workspace.Id)
	results := make([]any, 0, len(rows))
	for _, row := range rows {
		project := row.GetString("project")
		if project == "" {
			// Workspace-level favourites are visible except for pages.
			if row.GetString("entity_type") == "page" {
				continue
			}
		} else if !projectIDs[project] {
			// Project-scoped favourites require active project membership.
			continue
		}
		results = append(results, serializers.UserFavorite(h.App, row))
	}
	return e.JSON(http.StatusOK, results)
}

// FavoriteCreate serves POST /api/workspaces/{slug}/user-favorites/. It is
// idempotent: an existing favourite for the same (entity_type,
// entity_identifier) is returned rather than duplicated.
func (h *Handler) FavoriteCreate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	entityType := asString(body["entity_type"])
	if entityType == "" {
		return serializers.Validation(e, "entity_type", "This field is required.")
	}
	entityIdentifier := asString(body["entity_identifier"])

	if entityIdentifier != "" {
		existing, err := h.App.FindFirstRecordByFilter(
			"user_favorites",
			"workspace = {:workspace} && user = {:user} && entity_type = {:type} && entity_identifier = {:entity} && deleted_at = ''",
			dbx.Params{
				"workspace": scope.Workspace.Id,
				"user":      user.Id,
				"type":      entityType,
				"entity":    entityIdentifier,
			},
		)
		if err == nil && existing != nil {
			return e.JSON(http.StatusOK, serializers.UserFavorite(h.App, existing))
		}
	}

	collection, err := h.App.FindCollectionByNameOrId("user_favorites")
	if err != nil {
		return err
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("user", user.Id)
	record.Set("workspace", scope.Workspace.Id)
	record.Set("created_by", user.Id)
	record.Set("entity_type", entityType)
	if entityIdentifier != "" {
		record.Set("entity_identifier", entityIdentifier)
	}
	if v := asString(body["name"]); v != "" {
		record.Set("name", v)
	}
	if v, ok := body["is_folder"]; ok {
		record.Set("is_folder", v)
	}
	if projectID := asString(body["project_id"]); projectID != "" {
		if project, _ := serializers.FindByUUID(h.App, "projects", projectID); project != nil {
			record.Set("project", project.Id)
		}
	}
	if parentID := asString(body["parent"]); parentID != "" {
		if parent, _ := h.findUserFavorite(user.Id, scope.Workspace.Id, parentID); parent != nil {
			record.Set("parent", parent.Id)
		}
	}
	record.Set("sequence", h.nextFavoriteSequence(scope.Workspace.Id))
	if err := h.App.Save(record); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.UserFavorite(h.App, record))
}

// FavoriteDetail serves GET /api/workspaces/{slug}/user-favorites/{favorite_id}/.
func (h *Handler) FavoriteDetail(e *core.RequestEvent) error {
	record, err := h.ownFavorite(e)
	if err != nil {
		return err
	}
	if record == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	return e.JSON(http.StatusOK, serializers.UserFavorite(h.App, record))
}

// FavoriteUpdate serves PATCH /api/workspaces/{slug}/user-favorites/{favorite_id}/.
func (h *Handler) FavoriteUpdate(e *core.RequestEvent) error {
	record, err := h.ownFavorite(e)
	if err != nil {
		return err
	}
	if record == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	if v, ok := body["name"]; ok {
		record.Set("name", v)
	}
	if v, ok := body["is_folder"]; ok {
		record.Set("is_folder", v)
	}
	if v, ok := body["sequence"]; ok {
		record.Set("sequence", v)
	}
	if v, ok := body["parent"]; ok {
		record.Set("parent", v)
	}
	if err := h.App.Save(record); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.UserFavorite(h.App, record))
}

// FavoriteDestroy serves DELETE /api/workspaces/{slug}/user-favorites/{favorite_id}/.
func (h *Handler) FavoriteDestroy(e *core.RequestEvent) error {
	record, err := h.ownFavorite(e)
	if err != nil {
		return err
	}
	if record == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	if err := h.App.Delete(record); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// FavoriteGroup serves GET /api/workspaces/{slug}/user-favorites/{favorite_id}/group/.
func (h *Handler) FavoriteGroup(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	parent, err := h.ownFavorite(e)
	if err != nil {
		return err
	}
	if parent == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	rows, err := h.App.FindRecordsByFilter(
		"user_favorites",
		"user = {:user} && workspace = {:workspace} && parent = {:parent} && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"user": user.Id, "workspace": scope.Workspace.Id, "parent": parent.Id},
	)
	if err != nil {
		return err
	}
	projectIDs := h.memberProjectIDs(user.Id, scope.Workspace.Id)
	results := make([]any, 0, len(rows))
	for _, row := range rows {
		if project := row.GetString("project"); project != "" && !projectIDs[project] {
			continue
		}
		results = append(results, serializers.UserFavorite(h.App, row))
	}
	return e.JSON(http.StatusOK, results)
}

// ---------------------------------------------------------------------------
// Project favourites (ProjectFavoritesViewSet)
// ---------------------------------------------------------------------------

// ProjectFavoritesList serves GET /api/workspaces/{slug}/user-favorite-projects/.
//
// Django's list path is unreachable (the viewset declares no serializer_class),
// so the shape is not pinned by the backend. Plane's client types the result as
// an array, so an array of the caller's project favourites is returned.
func (h *Handler) ProjectFavoritesList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	rows, err := h.App.FindRecordsByFilter(
		"user_favorites",
		"user = {:user} && workspace = {:workspace} && entity_type = 'project' && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"user": user.Id, "workspace": scope.Workspace.Id},
	)
	if err != nil {
		return err
	}
	results := make([]any, 0, len(rows))
	for _, row := range rows {
		results = append(results, serializers.UserFavorite(h.App, row))
	}
	return e.JSON(http.StatusOK, results)
}

// ProjectFavoriteCreate serves POST /api/workspaces/{slug}/user-favorite-projects/
// (body `{project: <uuid>}`) and answers 204.
func (h *Handler) ProjectFavoriteCreate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	projectUUID := asString(body["project"])
	if projectUUID == "" {
		projectUUID = asString(body["project_id"])
	}
	if projectUUID == "" {
		return serializers.BadRequest(e, "Project is required")
	}
	project, _ := serializers.FindByUUID(h.App, "projects", projectUUID)
	if project == nil || project.GetString("workspace") != scope.Workspace.Id {
		return serializers.NotFound(e, "")
	}
	collection, err := h.App.FindCollectionByNameOrId("user_favorites")
	if err != nil {
		return err
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("user", user.Id)
	record.Set("workspace", scope.Workspace.Id)
	record.Set("project", project.Id)
	record.Set("created_by", user.Id)
	record.Set("entity_type", "project")
	record.Set("entity_identifier", projectUUID)
	record.Set("sequence", h.nextFavoriteSequence(scope.Workspace.Id))
	if err := h.App.Save(record); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.NoContent(http.StatusNoContent)
}

// ProjectFavoriteDestroy serves DELETE
// /api/workspaces/{slug}/user-favorite-projects/{project_id}/.
func (h *Handler) ProjectFavoriteDestroy(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	projectUUID := e.Request.PathValue("project_id")
	project, _ := serializers.FindByUUID(h.App, "projects", projectUUID)
	if project == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	record, err := h.App.FindFirstRecordByFilter(
		"user_favorites",
		"user = {:user} && workspace = {:workspace} && project = {:project} && entity_type = 'project' && entity_identifier = {:entity}",
		dbx.Params{
			"user":      user.Id,
			"workspace": scope.Workspace.Id,
			"project":   project.Id,
			"entity":    projectUUID,
		},
	)
	if err != nil || record == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	if err := h.App.Delete(record); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// View favourites (IssueViewFavoriteViewSet)
// ---------------------------------------------------------------------------

// ViewFavoritesList serves GET …/projects/{project_id}/user-favorite-views/.
func (h *Handler) ViewFavoritesList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	rows, err := h.App.FindRecordsByFilter(
		"user_favorites",
		"user = {:user} && workspace = {:workspace} && project = {:project} && entity_type = 'view' && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"user": user.Id, "workspace": scope.Workspace.Id, "project": scope.Project.Id},
	)
	if err != nil {
		return err
	}
	results := make([]any, 0, len(rows))
	for _, row := range rows {
		results = append(results, serializers.UserFavorite(h.App, row))
	}
	return e.JSON(http.StatusOK, results)
}

// ViewFavoriteCreate serves POST …/projects/{project_id}/user-favorite-views/
// (body `{view: <uuid>}`) and answers 204. Requires project role >= MEMBER.
func (h *Handler) ViewFavoriteCreate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
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
	viewUUID := asString(body["view"])
	if viewUUID == "" {
		viewUUID = asString(body["view_id"])
	}
	if viewUUID == "" {
		return serializers.BadRequest(e, "View is required")
	}
	collection, err := h.App.FindCollectionByNameOrId("user_favorites")
	if err != nil {
		return err
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("user", user.Id)
	record.Set("workspace", scope.Workspace.Id)
	record.Set("project", scope.Project.Id)
	record.Set("created_by", user.Id)
	record.Set("entity_type", "view")
	record.Set("entity_identifier", viewUUID)
	record.Set("sequence", h.nextFavoriteSequence(scope.Workspace.Id))
	if err := h.App.Save(record); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.NoContent(http.StatusNoContent)
}

// ViewFavoriteDestroy serves DELETE
// …/projects/{project_id}/user-favorite-views/{view_id}/. Requires project role
// >= MEMBER.
func (h *Handler) ViewFavoriteDestroy(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	record, err := h.App.FindFirstRecordByFilter(
		"user_favorites",
		"user = {:user} && workspace = {:workspace} && project = {:project} && entity_type = 'view' && entity_identifier = {:view}",
		dbx.Params{
			"user":      user.Id,
			"workspace": scope.Workspace.Id,
			"project":   scope.Project.Id,
			"view":      e.Request.PathValue("view_id"),
		},
	)
	if err != nil || record == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	if err := h.App.Delete(record); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// favourite shared helpers
// ---------------------------------------------------------------------------

// ownFavorite resolves a favourite belonging to the caller in the resolved
// workspace; another user's row is a 404.
func (h *Handler) ownFavorite(e *core.RequestEvent) (*core.Record, error) {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return nil, serializers.NotFound(e, "")
	}
	if user == nil {
		return nil, serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	return h.findUserFavorite(user.Id, scope.Workspace.Id, e.Request.PathValue("favorite_id"))
}

// findUserFavorite resolves one of the caller's favourites by Plane UUID.
func (h *Handler) findUserFavorite(userID, workspaceID, uuid string) (*core.Record, error) {
	record, err := serializers.FindByUUID(h.App, "user_favorites", uuid)
	if err != nil || record == nil {
		return nil, err
	}
	if record.GetString("user") != userID ||
		record.GetString("workspace") != workspaceID ||
		record.GetString("deleted_at") != "" {
		return nil, nil
	}
	return record, nil
}

// viewFavoriteIDs returns the set of view UUIDs the user favourited in a project.
func (h *Handler) viewFavoriteIDs(userID, projectID string) map[string]bool {
	out := map[string]bool{}
	rows, err := h.App.FindRecordsByFilter(
		"user_favorites",
		"user = {:user} && project = {:project} && entity_type = 'view' && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"user": userID, "project": projectID},
	)
	if err != nil {
		return out
	}
	for _, row := range rows {
		out[row.GetString("entity_identifier")] = true
	}
	return out
}

// isViewFavorite reports whether the caller favourited the given view.
func (h *Handler) isViewFavorite(e *core.RequestEvent, record *core.Record) bool {
	user := middleware.CurrentUser(e)
	if user == nil || record == nil {
		return false
	}
	row, err := h.App.FindFirstRecordByFilter(
		"user_favorites",
		"user = {:user} && entity_type = 'view' && entity_identifier = {:view} && deleted_at = ''",
		dbx.Params{"user": user.Id, "view": serializers.UUID(record)},
	)
	return err == nil && row != nil
}

// memberProjectIDs returns the set of project ids the user is an active member of.
func (h *Handler) memberProjectIDs(userID, workspaceID string) map[string]bool {
	out := map[string]bool{}
	rows, err := h.App.FindRecordsByFilter(
		"project_members",
		"member = {:member} && workspace = {:workspace} && is_active = true && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"member": userID, "workspace": workspaceID},
	)
	if err != nil {
		return out
	}
	for _, row := range rows {
		out[row.GetString("project")] = true
	}
	return out
}

// nextFavoriteSequence mirrors UserFavorite.save(): max(sequence) + 10000 over
// the workspace's favourites.
func (h *Handler) nextFavoriteSequence(workspaceID string) float64 {
	var max sql.NullFloat64
	err := h.App.DB().
		NewQuery("SELECT MAX(sequence) FROM user_favorites WHERE workspace = {:workspace} AND deleted_at = ''").
		Bind(dbx.Params{"workspace": workspaceID}).
		Row(&max)
	if err != nil || !max.Valid {
		return 65535
	}
	return max.Float64 + 10000
}
