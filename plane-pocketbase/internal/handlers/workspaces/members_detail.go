// Workspace member detail — the last method-level audit gap on a non-excluded
// path (surfaced by tests/browser/audit-methods.py as UNSERVED):
//
//	GET/PATCH/DELETE /api/workspaces/{slug}/members/{pk}/
//
// (WorkSpaceMemberViewSet retrieve / partial_update / destroy; the frontend's
// updateWorkspaceMember / removeWorkspaceMember, workspace.service.ts:149,157.)
// Permission matrix from spec/permissions.md §3.1: retrieve for any workspace
// member; role change and removal are workspace-ADMIN only, with the source
// guards member.py:76-150 — cannot change your own role, cannot touch a member
// with a higher role, cannot remove yourself (400 on each; use `members/leave/`).
package workspaces

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// MemberDetail serves GET/PATCH/DELETE /api/workspaces/{slug}/members/{pk}/.
func (h *Handler) MemberDetail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	membership, _ := serializers.FindByUUID(h.App, "workspace_members", e.Request.PathValue("pk"))
	if membership == nil || membership.GetString("workspace") != scope.Workspace.Id ||
		membership.GetString("deleted_at") != "" {
		return serializers.NotFound(e, "Not found.")
	}

	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, serializers.WorkspaceMember(h.App, membership, scope.WorkspaceRole > middleware.RoleGuest))

	case http.MethodPatch:
		if scope.WorkspaceRole < middleware.RoleAdmin {
			return serializers.Forbidden(e, "You do not have permission to perform this action.")
		}
		if membership.GetString("member") == user.Id {
			// member.py:76-96 — a member cannot change their own role.
			return e.JSON(http.StatusBadRequest, map[string]any{"error": "You cannot change your own role"})
		}
		if membership.GetInt("role") > scope.WorkspaceRole {
			return e.JSON(http.StatusBadRequest, map[string]any{"error": "You cannot change the role of a member with a higher role"})
		}
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		raw, ok := body["role"]
		if !ok {
			return serializers.Validation(e, "role", "This field is required.")
		}
		role, ok := raw.(float64)
		if !ok || int(role) <= 0 {
			return serializers.Validation(e, "role", "A valid integer is required.")
		}
		if int(role) > scope.WorkspaceRole {
			return e.JSON(http.StatusBadRequest, map[string]any{"error": "You cannot grant a role higher than your own"})
		}
		membership.Set("role", int(role))
		membership.Set("updated_by", user.Id)
		if err := h.App.Save(membership); err != nil {
			return serializers.Validation(e, "role", err.Error())
		}
		return e.JSON(http.StatusOK, serializers.WorkspaceMember(h.App, membership, true))

	case http.MethodDelete:
		if scope.WorkspaceRole < middleware.RoleAdmin {
			return serializers.Forbidden(e, "You do not have permission to perform this action.")
		}
		if membership.GetString("member") == user.Id {
			// member.py:98-150 — self-removal goes through POST members/leave/.
			return e.JSON(http.StatusBadRequest, map[string]any{"error": "You cannot remove yourself from the workspace"})
		}
		if membership.GetInt("role") > scope.WorkspaceRole {
			return e.JSON(http.StatusBadRequest, map[string]any{"error": "You cannot remove a member with a higher role"})
		}
		membership.Set("is_active", false)
		membership.Set("deleted_at", serializers.Now())
		membership.Set("updated_by", user.Id)
		if err := h.App.Save(membership); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}
