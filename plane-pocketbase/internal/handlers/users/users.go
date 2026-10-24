// Package users implements /api/users/me/* — the identity endpoints Plane's web
// app calls on boot.
package users

import (
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/profiles"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Handler holds the app dependency for the user routes.
type Handler struct {
	App core.App
}

// New returns a users handler bound to app.
func New(app core.App) *Handler {
	return &Handler{App: app}
}

// Me serves GET /api/users/me/ (UserEndpoint.retrieve → UserMeSerializer).
func (h *Handler) Me(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	return e.JSON(http.StatusOK, serializers.UserMe(h.App, user))
}

// Profile serves GET /api/users/me/profile/ (ProfileEndpoint.get →
// ProfileSerializer, i.e. the whole Profile row). Plane reads onboarding_step
// and is_onboarded from here to decide whether to keep the user in the
// onboarding wizard, so this must reflect persisted state, not a synthesis.
func (h *Handler) Profile(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	profile, err := profiles.Ensure(h.App, user)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, serializers.Profile(h.App, profile))
}

// Settings serves GET /api/users/me/settings/ (UserSettingsEndpoint). Plane
// reads `workspace.last_workspace_slug` from it to pick the landing workspace,
// and the value comes from Profile.last_workspace_id.
func (h *Handler) Settings(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	profile, err := profiles.Ensure(h.App, user)
	if err != nil {
		return err
	}

	workspace := map[string]any{
		"last_workspace_id":   nil,
		"last_workspace_slug": nil,
		"last_workspace_name": nil,
		"last_workspace_logo": nil,
	}
	if wsUUID := profile.GetString("last_workspace_id"); wsUUID != "" {
		if ws, _ := serializers.FindByUUID(h.App, "workspaces", wsUUID); ws != nil {
			workspace = map[string]any{
				"last_workspace_id":   serializers.UUID(ws),
				"last_workspace_slug": ws.GetString("slug"),
				"last_workspace_name": ws.GetString("name"),
				"last_workspace_logo": serializers.NullableText(ws, "logo"),
			}
		}
	}
	workspace["fallback_workspace_id"] = workspace["last_workspace_id"]
	workspace["fallback_workspace_slug"] = workspace["last_workspace_slug"]
	workspace["invites"] = 0

	return e.JSON(http.StatusOK, map[string]any{
		"id":        serializers.UUID(user),
		"email":     user.GetString("email"),
		"workspace": workspace,
	})
}

// PatchProfile serves PATCH /api/users/me/profile/ (ProfileEndpoint.patch).
// ProfileSerializer is `fields = "__all__"` with only `user` read-only, so
// every other column is writable; unknown keys are ignored rather than
// rejected, matching DRF's behaviour when a field is not in the serializer.
func (h *Handler) PatchProfile(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	profile, err := profiles.Ensure(h.App, user)
	if err != nil {
		return err
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}

	for _, field := range profileWritableFields {
		if v, ok := body[field]; ok {
			profile.Set(field, v)
		}
	}
	if err := h.App.Save(profile); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.Profile(h.App, profile))
}

// Onboard serves PATCH /api/users/me/onboard/ (UpdateUserOnBoardedEndpoint).
func (h *Handler) Onboard(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	profile, err := profiles.Ensure(h.App, user)
	if err != nil {
		return err
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	// Django: `request.data.get("is_onboarded", False)` — an absent key means false.
	onboarded, _ := body["is_onboarded"].(bool)
	profile.Set("is_onboarded", onboarded)
	if err := h.App.Save(profile); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, map[string]any{"message": "Updated successfully"})
}

// TourCompleted serves PATCH /api/users/me/tour-completed/
// (UpdateUserTourCompletedEndpoint).
func (h *Handler) TourCompleted(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	profile, err := profiles.Ensure(h.App, user)
	if err != nil {
		return err
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	completed, _ := body["is_tour_completed"].(bool)
	profile.Set("is_tour_completed", completed)
	if err := h.App.Save(profile); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, map[string]any{"message": "Updated successfully"})
}

// PatchMe serves PATCH /api/users/me/. Only the fields DRF leaves writable on
// UserSerializer are applied; system fields are ignored rather than rejected.
// Profile-owned fields (onboarding_step, is_onboarded, last_workspace_id, …)
// are deliberately NOT here — they belong to /api/users/me/profile/.
func (h *Handler) PatchMe(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}

	for _, field := range []string{
		"display_name", "first_name", "last_name", "avatar", "cover_image",
		"user_timezone", "mobile_number", "username", "last_location",
		"created_location", "is_email_valid", "is_password_autoset",
	} {
		if v, ok := body[field]; ok {
			user.Set(field, v)
		}
	}
	if err := h.App.Save(user); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.UserFull(h.App, user))
}

// DeleteMe serves DELETE /api/users/me/ (UserEndpoint.delete) — the settings
// page's "Deactivate account" (`user.service.ts:230 deactivateAccount`).
// Django deactivates rather than destroys the row: `is_active` flips to false
// and the session is gone on the next request. Answer 204.
func (h *Handler) DeleteMe(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	user.Set("is_active", false)
	if err := h.App.Save(user); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.NoContent(http.StatusNoContent)
}

// ProjectRoles serves GET /api/users/me/workspaces/{slug}/project-roles/
// (UserProjectRolesEndpoint).
//
// Django returns a FLAT `{project_id: role}` map, and Plane's permission store
// reads exactly that shape (`workspaceProjectsPermissions[slug][projectId]` in
// store/user/base-permissions.store.ts). Returning a list of workspace objects
// instead makes `allowPermissions(..., PROJECT, …)` false for every project, so
// the whole app renders "Project not found" even though the project exists.
func (h *Handler) ProjectRoles(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "The requested workspace could not be found.")
	}

	members, err := h.App.FindRecordsByFilter(
		"project_members",
		"member = {:member} && workspace = {:workspace} && is_active = true && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"member": user.Id, "workspace": scope.Workspace.Id},
	)
	if err != nil {
		return err
	}

	out := make(map[string]int, len(members))
	for _, m := range members {
		project, _ := h.App.FindRecordById("projects", m.GetString("project"))
		if project == nil || project.GetString("deleted_at") != "" {
			continue
		}
		out[serializers.UUID(project)] = m.GetInt("role")
	}
	return e.JSON(http.StatusOK, out)
}

// WorkspaceSlugCheck serves GET /api/workspace-slug-check/. Unlike the
// instance-admin slug check, this endpoint is available to an authenticated app
// user before a workspace exists, so onboarding can probe a candidate slug.
func (h *Handler) WorkspaceSlugCheck(e *core.RequestEvent) error {
	slug := strings.ToLower(strings.TrimSpace(e.Request.URL.Query().Get("slug")))
	if slug == "" {
		return serializers.BadRequest(e, "Workspace Slug is required")
	}
	existing, err := h.App.FindFirstRecordByFilter("workspaces", "slug = {:slug}", dbx.Params{"slug": slug})
	if err != nil && existing == nil {
		return e.JSON(http.StatusOK, map[string]any{"status": true})
	}
	return e.JSON(http.StatusOK, map[string]any{"status": existing == nil})
}

// MeWorkspaces serves GET /api/users/me/workspaces/ (UserWorkSpacesEndpoint).
// Plane returns a plain, non-paginated array here — it is what the workspace
// switcher reads on boot — so do not wrap it in the cursor envelope.
func (h *Handler) MeWorkspaces(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	memberships, err := h.App.FindRecordsByFilter(
		"workspace_members",
		"member = {:member} && is_active = true && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"member": user.Id},
	)
	if err != nil {
		return err
	}

	results := make([]any, 0, len(memberships))
	for _, membership := range memberships {
		workspace, _ := h.App.FindRecordById("workspaces", membership.GetString("workspace"))
		if workspace == nil || workspace.GetString("deleted_at") != "" {
			continue
		}
		total := h.countActiveMembers(workspace.Id)
		results = append(results, serializers.Workspace(h.App, workspace, membership.GetInt("role"), total))
	}
	return e.JSON(http.StatusOK, results)
}

// profileWritableFields is ProfileSerializer minus the read-only `user`, the
// non-editable `id` and the auto-managed audit columns.
var profileWritableFields = []string{
	"theme", "is_app_rail_docked", "is_tour_completed", "onboarding_step",
	"use_case", "role", "is_onboarded", "last_workspace_id",
	"billing_address_country", "billing_address", "has_billing_address",
	"company_name", "notification_view_mode", "is_smooth_cursor_enabled",
	"is_mobile_onboarded", "mobile_onboarding_step", "mobile_timezone_auto_set",
	"language", "start_of_the_week", "goals", "background_color",
	"is_navigation_tour_completed", "has_marketing_email_consent",
	"is_subscribed_to_changelog", "product_tour",
}

// countActiveMembers counts the workspace's active, non-bot members, matching
// the `total_members` annotation in WorkSpaceViewSet.get_queryset.
func (h *Handler) countActiveMembers(workspaceID string) int {
	members, err := h.App.FindRecordsByFilter(
		"workspace_members",
		"workspace = {:workspace} && is_active = true && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"workspace": workspaceID},
	)
	if err != nil {
		return 0
	}
	total := 0
	for _, m := range members {
		user, _ := h.App.FindRecordById("users", m.GetString("member"))
		if user == nil || user.GetBool("is_bot") {
			continue
		}
		total++
	}
	return total
}
