package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/auth"
	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/users"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
)

// registerUsers wires the identity endpoints Plane's web app calls on boot.
func registerUsers(e *core.ServeEvent, app core.App) {
	h := users.New(app)

	e.Router.GET("/api/users/me/{$}", h.Me)
	e.Router.PATCH("/api/users/me/{$}", h.PatchMe)
	// Account deactivation (UserEndpoint.delete — user.service.ts
	// deactivateAccount). Without it the settings page's delete action 405s.
	e.Router.DELETE("/api/users/me/{$}", h.DeleteMe)
	e.Router.GET("/api/users/me/profile/{$}", h.Profile)
	e.Router.PATCH("/api/users/me/profile/{$}", h.PatchProfile)
	e.Router.GET("/api/users/me/settings/{$}", h.Settings)
	e.Router.PATCH("/api/users/me/onboard/{$}", h.Onboard)
	e.Router.PATCH("/api/users/me/tour-completed/{$}", h.TourCompleted)
	e.Router.GET("/api/users/me/workspaces/{$}", h.MeWorkspaces)
	e.Router.GET("/api/workspace-slug-check/{$}", h.WorkspaceSlugCheck)

	// Linked OAuth accounts, instance-admin status, and the email-change flow
	// (AccountEndpoint, UserEndpoint.retrieve_instance_admin / update_email /
	// generate_email_verification_code).
	e.Router.GET("/api/users/me/accounts/{$}", h.Accounts)
	e.Router.GET("/api/users/me/instance-admin/{$}", h.InstanceAdmin)
	e.Router.POST("/api/users/me/email/generate-code/{$}", h.GenerateEmailCode)
	e.Router.PATCH("/api/users/me/email/{$}", h.UpdateEmail)

	// Invitations addressed to the caller, and the "accept a batch" action
	// (UserWorkspaceInvitationsViewSet). Registered before the
	// `/api/users/me/workspaces/` subtree above is only relevant for deeper
	// paths — the more specific patterns win.
	e.Router.GET("/api/users/me/workspaces/invitations/{$}", h.MyWorkspaceInvitations)
	e.Router.POST("/api/users/me/workspaces/invitations/{$}", h.JoinWorkspaces)

	// The workspace the user last visited, with their project memberships in it
	// (UserLastProjectWithWorkspaceEndpoint).
	e.Router.GET("/api/users/last-visited-workspace/{$}", h.LastVisitedWorkspace)

	// User-scoped workspace sub-resources. `/api/users/me/workspaces/` above is
	// a Go subtree pattern (trailing slash), so anything deeper would otherwise
	// be swallowed by MeWorkspaces and answered with the workspace list — which
	// is exactly how `/project-roles/` silently returned the wrong shape. The
	// more specific pattern below wins over the subtree.
	meWorkspace := e.Router.Group("/api/users/me/workspaces/{slug}").
		BindFunc(middleware.ResolveWorkspace(app))
	meWorkspace.GET("/project-roles/{$}", h.ProjectRoles)
	meWorkspace.POST("/projects/invitations/{$}", h.ProjectInvitations)

	// API token management (spec ids workspace.api-tokens.*).
	e.Router.GET("/api/users/api-tokens/{$}", h.ApiTokens)
	e.Router.POST("/api/users/api-tokens/{$}", h.ApiTokens)
	e.Router.GET("/api/users/api-tokens/{pk}/{$}", h.ApiTokenDetail)
	e.Router.PATCH("/api/users/api-tokens/{pk}/{$}", h.ApiTokenDetail)
	e.Router.DELETE("/api/users/api-tokens/{pk}/{$}", h.ApiTokenDetail)

	// The session probe lives under /api/users/ but is an authentication
	// concern, so it is served by the auth handler.
	e.Router.GET("/api/users/session/{$}", auth.New(app).Session)
}
