package workspaces

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// This file implements the workspace-scoped label endpoints:
//
//	GET    /api/workspaces/{slug}/labels/            WorkspaceLabelsEndpoint
//	POST   /api/workspaces/{slug}/labels/            (no Django route; see below)
//	PATCH  /api/workspaces/{slug}/labels/{label_id}/ (no Django route; see below)
//	DELETE /api/workspaces/{slug}/labels/{label_id}/ (no Django route; see below)
//
// Django only defines the GET: `WorkspaceLabelsEndpoint` (plane/app/views/
// workspace/label.py) is a read-only APIView that returns every label in the
// workspace belonging to a project the caller is an active member of.
//
// The mutating routes are not defined by Django at this path — Plane's label
// CRUD lives on the *project* collection (`/projects/{project_id}/issue-labels/`,
// LabelViewSet, which requires project Admin). They are implemented here anyway
// because this slice's endpoint list asks for them at the workspace level; their
// request/response shapes reuse LabelSerializer so they stay wire-compatible if
// a future Plane version moves label CRUD up to the workspace. FLAGGED: no
// Django source of truth for POST/PATCH/DELETE at this path.

// labelProjectIDs returns the PocketBase ids of the workspace's projects the
// caller is an active member of and that are not archived — Django's
// `project__project_projectmember__member=request.user, is_active=True,
// project__archived_at__isnull=True`.
func (h *Handler) labelProjectIDs(workspaceID, userID string) []string {
	var rows []struct {
		ID string `db:"id"`
	}
	err := h.App.DB().NewQuery(`
		SELECT p.id AS id
		FROM project_members pm
		JOIN projects p ON p.id = pm.project
		WHERE pm.member = {:member}
			AND pm.is_active = 1
			AND pm.deleted_at = ''
			AND p.deleted_at = ''
			AND p.archived_at = ''
			AND p.workspace = {:workspace}
		ORDER BY p.id`).Bind(dbx.Params{
		"member":    userID,
		"workspace": workspaceID,
	}).All(&rows)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out
}

// WorkspaceLabels serves GET and POST /api/workspaces/{slug}/labels/.
func (h *Handler) WorkspaceLabels(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	if e.Request.Method == http.MethodPost {
		return h.createWorkspaceLabel(e, scope, user)
	}

	projectIDs := h.labelProjectIDs(scope.Workspace.Id, user.Id)
	results := make([]any, 0)
	if len(projectIDs) > 0 {
		parts := make([]string, 0, len(projectIDs))
		params := dbx.Params{"workspace": scope.Workspace.Id}
		for i, id := range projectIDs {
			key := fmt.Sprintf("proj%d", i)
			parts = append(parts, fmt.Sprintf("project = {:%s}", key))
			params[key] = id
		}
		filter := "workspace = {:workspace} && deleted_at = '' && (" + strings.Join(parts, " || ") + ")"
		labels, err := h.App.FindRecordsByFilter("labels", filter, "-created_at", 0, 0, params)
		if err != nil {
			return err
		}
		for _, label := range labels {
			results = append(results, serializers.Label(h.App, label))
		}
	}
	return e.JSON(http.StatusOK, results)
}

// createWorkspaceLabel handles POST /api/workspaces/{slug}/labels/. A label
// always belongs to a project, so `project_id` (a Plane UUID) is required in the
// body; the caller must be that project's Admin (LabelViewSet.create).
func (h *Handler) createWorkspaceLabel(e *core.RequestEvent, scope *middleware.Scope, user *core.Record) error {
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	name := strings.TrimSpace(asString(body["name"]))
	if name == "" {
		return serializers.Validation(e, "name", "This field is required.")
	}
	projectID := asString(body["project_id"])
	role, project := h.projectRoleFor(scope.Workspace.Id, projectID, user.Id)
	if project == nil {
		return serializers.Validation(e, "project_id", "Invalid project.")
	}
	if role < middleware.RoleAdmin {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}

	collection, err := h.App.FindCollectionByNameOrId("labels")
	if err != nil {
		return err
	}
	label := core.NewRecord(collection)
	label.Set("uuid", serializers.NewUUID())
	label.Set("workspace", scope.Workspace.Id)
	label.Set("project", project.Id)
	label.Set("name", name)
	if v, ok := body["color"]; ok {
		label.Set("color", asString(v))
	}
	if parentID := asString(body["parent"]); parentID != "" {
		if parent, _ := serializers.FindByUUID(h.App, "labels", parentID); parent != nil {
			label.Set("parent", parent.Id)
		}
	}
	label.Set("sort_order", h.nextLabelSortOrder(project.Id))
	label.Set("created_by", user.Id)
	label.Set("updated_by", user.Id)
	if err := h.App.Save(label); err != nil {
		return serializers.Validation(e, "name", err.Error())
	}
	return e.JSON(http.StatusCreated, serializers.Label(h.App, label))
}

// WorkspaceLabelDetail serves PATCH and DELETE /api/workspaces/{slug}/labels/{label_id}/.
func (h *Handler) WorkspaceLabelDetail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	label, _ := serializers.FindByUUID(h.App, "labels", e.Request.PathValue("label_id"))
	if label == nil || label.GetString("workspace") != scope.Workspace.Id || label.GetString("deleted_at") != "" {
		return serializers.NotFound(e, "Not found.")
	}
	project, _ := h.App.FindRecordById("projects", label.GetString("project"))
	if project == nil {
		return serializers.NotFound(e, "Not found.")
	}
	if role, _ := h.projectRoleFor(scope.Workspace.Id, serializers.UUID(project), user.Id); role < middleware.RoleAdmin {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}

	if e.Request.Method == http.MethodDelete {
		label.Set("deleted_at", serializers.Now())
		label.Set("updated_by", user.Id)
		if err := h.App.Save(label); err != nil {
			return err
		}
		return e.NoContent(http.StatusNoContent)
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	if raw, ok := body["name"]; ok {
		name := strings.TrimSpace(asString(raw))
		if name == "" {
			return serializers.Validation(e, "name", "This field cannot be blank.")
		}
		if existing, _ := h.App.FindFirstRecordByFilter(
			"labels",
			"project = {:project} && name = {:name} && deleted_at = '' && id != {:id}",
			dbx.Params{"project": project.Id, "name": name, "id": label.Id},
		); existing != nil {
			return e.JSON(http.StatusBadRequest, map[string]any{"error": "Label with the same name already exists in the project"})
		}
		label.Set("name", name)
	}
	if v, ok := body["color"]; ok {
		label.Set("color", asString(v))
	}
	if raw, ok := body["parent"]; ok {
		parentID := asString(raw)
		if parentID == "" {
			label.Set("parent", "")
		} else if parent, _ := serializers.FindByUUID(h.App, "labels", parentID); parent != nil {
			label.Set("parent", parent.Id)
		}
	}
	if v, ok := body["sort_order"]; ok {
		label.Set("sort_order", v)
	}
	label.Set("updated_by", user.Id)
	if err := h.App.Save(label); err != nil {
		return serializers.Validation(e, "name", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.Label(h.App, label))
}

// projectRoleFor resolves a Plane project UUID inside the workspace and returns
// the caller's active project role (0 when not a member) plus the project
// record. It returns a nil project when the UUID is unknown or belongs to
// another workspace, so callers can 404 rather than leak existence.
func (h *Handler) projectRoleFor(workspaceID, projectUUID, userID string) (int, *core.Record) {
	project, _ := serializers.FindByUUID(h.App, "projects", projectUUID)
	if project == nil || project.GetString("workspace") != workspaceID || project.GetString("deleted_at") != "" {
		return 0, nil
	}
	member, _ := h.App.FindFirstRecordByFilter(
		"project_members",
		"project = {:project} && member = {:member} && is_active = true",
		dbx.Params{"project": project.Id, "member": userID},
	)
	if member == nil {
		return 0, project
	}
	return member.GetInt("role"), project
}

// nextLabelSortOrder mirrors Label.save(): the new label's sort_order is the
// project's current maximum plus 10000, or Django's 65535 default when the
// project has no labels yet.
func (h *Handler) nextLabelSortOrder(projectID string) float64 {
	var max sql.NullFloat64
	_ = h.App.DB().NewQuery(
		"SELECT MAX(sort_order) AS m FROM labels WHERE project = {:project} AND deleted_at = ''",
	).Bind(dbx.Params{"project": projectID}).Row(&max)
	if max.Valid {
		return max.Float64 + 10000
	}
	return 65535
}
