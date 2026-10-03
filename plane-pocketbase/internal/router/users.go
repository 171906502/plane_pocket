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
	e.Router.GET("/api/users/me/profile/{$}", h.Profile)
	e.Router.PATCH("/api/users/me/profile/{$}", h.PatchProfile)
	e.Router.GET("/api/users/me/settings/{$}", h.Settings)
	e.Router.PATCH("/api/users/me/onboard/{$}", h.Onboard)
	e.Router.PATCH("/api/users/me/tour-completed/{$}", h.TourCompleted)
	e.Router.GET("/api/users/me/workspaces/{$}", h.MeWorkspaces)
	e.Router.GET("/api/workspace-slug-check/{$}", h.WorkspaceSlugCheck)

	// User-scoped workspace sub-resources. `/api/users/me/workspaces/` above is
	// a Go subtree pattern (trailing slash), so anything deeper would otherwise
	// be swallowed by MeWorkspaces and answered with the workspace list — which
	// is exactly how `/project-roles/` silently returned the wrong shape. The
	// more specific pattern below wins over the subtree.
	meWorkspace := e.Router.Group("/api/users/me/workspaces/{slug}").
		BindFunc(middleware.ResolveWorkspace(app))
	meWorkspace.GET("/project-roles/{$}", h.ProjectRoles)

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
