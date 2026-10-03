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
