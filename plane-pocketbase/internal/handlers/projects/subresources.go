// Project sub-resources that the web app fetches on every project page:
//
//	GET   …/projects/{project_id}/project-members/me/
//	GET   …/projects/{project_id}/members/
//	GET   …/projects/{project_id}/user-properties/
//	PATCH …/projects/{project_id}/user-properties/
//	GET   …/projects/{project_id}/issue-labels/
//	GET   …/projects/{project_id}/estimates/
//
// All of these were previously unregistered, so they fell through to the
// `…/projects/{project_id}/` route — which is a *subtree* pattern and answered
// 200 with a project object. The damage was silent and severe:
//
//   - `project-members/me/` drives `workspaceProjectsPermissions[slug][pid]`;
//     receiving a project body meant `response.role` was `undefined`, which
//     overwrote the correct value from `…/project-roles/` and made the project
//     layout render "Project not found".
//   - `members/`, `issue-labels/`, `estimates/` and `user-properties/` all
//     handed object-shaped bodies to stores that call `.forEach()`, throwing
//     inside the store actions.
package projects

import (
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// MyMembership serves GET …/projects/{project_id}/project-members/me/
// (ProjectMemberUserEndpoint). Returns the caller's own ProjectMember row.
func (h *Handler) MyMembership(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	member, err := h.App.FindFirstRecordByFilter(
		"project_members",
		"project = {:project} && member = {:member} && is_active = true && deleted_at = ''",
		dbx.Params{"project": scope.Project.Id, "member": user.Id},
	)
	if err != nil || member == nil {
		return serializers.NotFound(e, "")
	}
	return e.JSON(http.StatusOK, serializers.ProjectMember(h.App, member))
}

// Members serves GET …/projects/{project_id}/members/ (ProjectMemberViewSet
// list, which uses ProjectMemberAdminSerializer — the member is rendered with
// the admin-lite shape that also carries the email).
func (h *Handler) Members(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}

	members, err := h.App.FindRecordsByFilter(
		"project_members",
		"project = {:project} && is_active = true && deleted_at = ''",
		"sort_order", 0, 0,
		dbx.Params{"project": scope.Project.Id},
	)
	if err != nil {
		return err
	}

	results := make([]any, 0, len(members))
	for _, member := range members {
		// `member__is_bot=False` is part of the Django queryset.
		if user, _ := h.App.FindRecordById("users", member.GetString("member")); user != nil && user.GetBool("is_bot") {
			continue
		}
		results = append(results, serializers.ProjectMemberAdmin(h.App, member))
	}
	return e.JSON(http.StatusOK, results)
}

// UserProperties serves GET/PATCH …/projects/{project_id}/user-properties/
// (ProjectUserDisplayPropertyEndpoint). Plane get_or_creates the row, so the
// GET is a write on first use.
func (h *Handler) UserProperties(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	properties, err := h.userProperties(scope, user)
	if err != nil {
		return err
	}

	if e.Request.Method == http.MethodPatch {
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		// ProjectUserPropertySerializer declares user/workspace/project
		// read-only; the JSON blobs and sort_order are writable.
		for _, field := range []string{"filters", "display_filters", "display_properties", "rich_filters", "preferences"} {
			if v, ok := body[field]; ok {
				properties.Set(field, v)
			}
		}
		if v, ok := body["sort_order"]; ok {
			properties.Set("sort_order", v)
		}
		properties.Set("updated_by", user.Id)
		if err := h.App.Save(properties); err != nil {
			return err
		}
	}

	return e.JSON(http.StatusOK, serializers.ProjectUserProperty(h.App, properties))
}

// IssueLabels serves GET …/projects/{project_id}/issue-labels/ (LabelViewSet
// list), ordered by sort_order like Django's model Meta.
func (h *Handler) IssueLabels(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	labels, err := h.App.FindRecordsByFilter(
		"labels",
		"project = {:project} && deleted_at = ''",
		"sort_order", 0, 0,
		dbx.Params{"project": scope.Project.Id},
	)
	if err != nil {
		return err
	}
	results := make([]any, 0, len(labels))
	for _, label := range labels {
		results = append(results, serializers.Label(h.App, label))
	}
	return e.JSON(http.StatusOK, results)
}

// IssueLabelsCreate serves POST …/projects/{project_id}/issue-labels/
// (LabelViewSet.create — the same view Django mounts at this project path,
// ADMIN-only, 201).
//
// The project-scoped half of the label API was missing while the GET half
// existed, so the frontend's `createIssueLabel` (issue_label.service.ts:35) had
// nowhere to go: creating a label from the project settings 404'd. The audit
// script cannot see this class of defect — it diffs *paths*, and the path is
// already registered — so it only surfaced from reading the service file.
func (h *Handler) IssueLabelsCreate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	if scope.ProjectRole < middleware.RoleAdmin {
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
	// Django raises IntegrityError on a duplicate (project, name) and maps it to
	// this exact 400 body, so mirror it rather than surfacing a driver error.
	if existing, _ := h.App.FindFirstRecordByFilter(
		"labels",
		"project = {:project} && name = {:name} && deleted_at = ''",
		dbx.Params{"project": scope.Project.Id, "name": name},
	); existing != nil {
		return e.JSON(http.StatusBadRequest, map[string]any{
			"error": "Label with the same name already exists in the project",
		})
	}

	collection, err := h.App.FindCollectionByNameOrId("labels")
	if err != nil {
		return err
	}
	label := core.NewRecord(collection)
	label.Set("uuid", serializers.NewUUID())
	label.Set("workspace", scope.Workspace.Id)
	label.Set("project", scope.Project.Id)
	label.Set("name", name)
	if v, ok := body["color"]; ok {
		label.Set("color", asString(v))
	}
	if v, ok := body["description"]; ok {
		label.Set("description", asString(v))
	}
	// `parent` is scoped to the same project — a label cannot be nested under
	// another project's label.
	if parentID := asString(body["parent"]); parentID != "" {
		if parent, _ := serializers.FindByUUID(h.App, "labels", parentID); parent != nil &&
			parent.GetString("project") == scope.Project.Id {
			label.Set("parent", parent.Id)
		}
	}
	label.Set("sort_order", h.nextProjectLabelSortOrder(scope.Project.Id))
	label.Set("created_by", user.Id)
	label.Set("updated_by", user.Id)
	if err := h.App.Save(label); err != nil {
		return serializers.Validation(e, "name", err.Error())
	}
	return e.JSON(http.StatusCreated, serializers.Label(h.App, label))
}

// IssueLabelsDetail serves PATCH and DELETE
// …/projects/{project_id}/issue-labels/{label_id}/ (LabelViewSet.partial_update
// / destroy, ADMIN-only). DELETE is a soft delete (`deleted_at`), matching the
// workspace-level handler and Django's soft-delete base.
func (h *Handler) IssueLabelsDetail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	if scope.ProjectRole < middleware.RoleAdmin {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}

	// Look up by Plane UUID, then require it to belong to *this* project, so a
	// label from another project cannot be read or mutated through this route.
	label, _ := serializers.FindByUUID(h.App, "labels", e.Request.PathValue("label_id"))
	if label == nil || label.GetString("project") != scope.Project.Id ||
		label.GetString("deleted_at") != "" {
		return serializers.NotFound(e, "Not found.")
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
			dbx.Params{"project": scope.Project.Id, "name": name, "id": label.Id},
		); existing != nil {
			return e.JSON(http.StatusBadRequest, map[string]any{
				"error": "Label with the same name already exists in the project",
			})
		}
		label.Set("name", name)
	}
	if v, ok := body["color"]; ok {
		label.Set("color", asString(v))
	}
	if v, ok := body["description"]; ok {
		label.Set("description", asString(v))
	}
	if raw, ok := body["parent"]; ok {
		parentID := asString(raw)
		if parentID == "" {
			label.Set("parent", "")
		} else if parent, _ := serializers.FindByUUID(h.App, "labels", parentID); parent != nil &&
			parent.GetString("project") == scope.Project.Id {
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

// nextProjectLabelSortOrder returns max(sort_order)+1 among the project's live
// labels, so a new label lands at the end of the list.
func (h *Handler) nextProjectLabelSortOrder(projectID string) float64 {
	labels, err := h.App.FindRecordsByFilter(
		"labels",
		"project = {:project} && deleted_at = ''",
		"-sort_order", 1, 0,
		dbx.Params{"project": projectID},
	)
	if err != nil || len(labels) == 0 {
		return 1
	}
	return labels[0].GetFloat("sort_order") + 1
}

// Estimates serves GET …/projects/{project_id}/estimates/
// (BulkEstimatePointEndpoint.list → EstimateReadSerializer).
func (h *Handler) Estimates(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	estimates, err := h.App.FindRecordsByFilter(
		"estimates",
		"project = {:project} && deleted_at = ''",
		"name", 0, 0,
		dbx.Params{"project": scope.Project.Id},
	)
	if err != nil {
		return err
	}
	results := make([]any, 0, len(estimates))
	for _, estimate := range estimates {
		results = append(results, serializers.Estimate(h.App, estimate))
	}
	return e.JSON(http.StatusOK, results)
}

// userProperties returns (creating when absent) the caller's
// `project_user_properties` row — Django's `get_or_create` in
// ProjectUserDisplayPropertyEndpoint.get.
func (h *Handler) userProperties(scope *middleware.Scope, user *core.Record) (*core.Record, error) {
	existing, err := h.App.FindFirstRecordByFilter(
		"project_user_properties",
		"user = {:user} && project = {:project} && deleted_at = ''",
		dbx.Params{"user": user.Id, "project": scope.Project.Id},
	)
	if err == nil && existing != nil {
		return existing, nil
	}

	collection, err := h.App.FindCollectionByNameOrId("project_user_properties")
	if err != nil {
		return nil, err
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("user", user.Id)
	record.Set("project", scope.Project.Id)
	record.Set("workspace", scope.Workspace.Id)
	record.Set("sort_order", 65535)
	record.Set("created_by", user.Id)
	if err := h.App.Save(record); err != nil {
		return nil, err
	}
	return record, nil
}
