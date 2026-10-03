package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/workspaces"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
)

// registerWorkspace wires /api/workspaces/ and /api/workspaces/{slug}/…
func registerWorkspace(e *core.ServeEvent, app core.App) {
	h := workspaces.New(app)

	// Collection-level routes: the caller must be authenticated, but the
	// workspace is implied by membership rather than a slug. `{$}` keeps these
	// to the exact path so an unknown `/api/workspaces/...` path 404s instead
	// of being answered with the workspace list.
	e.Router.GET("/api/workspaces/{$}", h.List).BindFunc(middleware.RequireAuth())
	e.Router.POST("/api/workspaces/{$}", h.Create).BindFunc(middleware.RequireAuth())

	// Workspace-scoped routes resolve the workspace and the caller's role.
	//
	// `/{$}` — not `/` — anchors these to the exact path. A bare "/" on a group
	// produces the ServeMux pattern `/api/workspaces/{slug}/`, which is a
	// *subtree* pattern: it swallows every unregistered sub-path and answers it
	// with the workspace object. That is how
	// `/api/workspaces/{slug}/user-profile/{id}/` silently returned a workspace
	// (HTTP 200) instead of 404, which crashed the web app's profile page.
	ws := workspaceGroup(e, app)
	ws.GET("/{$}", h.Detail)
	ws.PATCH("/{$}", h.Update).BindFunc(middleware.RequireWorkspaceRole(middleware.RoleAdmin))
	ws.DELETE("/{$}", h.Delete).BindFunc(middleware.RequireWorkspaceRole(middleware.RoleAdmin))
	ws.GET("/members/{$}", h.Members)

	// The caller's own membership — the web app's workspace access gate
	// (`layouts/auth-layout/workspace-wrapper.tsx`).
	ws.GET("/workspace-members/me/{$}", h.MemberMe)

	// Workspace-wide reference data and per-user preferences, all of which the
	// web app fetches on every workspace load.
	ws.GET("/states/{$}", h.States)
	ws.GET("/user-properties/{$}", h.UserProperties)
	ws.PATCH("/user-properties/{$}", h.UserProperties)
	ws.GET("/sidebar-preferences/{$}", h.SidebarPreferences)
	ws.PATCH("/sidebar-preferences/{$}", h.SidebarPreferences)

	// Workspace home-page widgets (WorkspaceHomePreferenceViewSet). The list is
	// GET /home-preferences/; the per-widget PATCH carries the key in the path.
	ws.GET("/home-preferences/{$}", h.HomePreferences)
	ws.PATCH("/home-preferences/{key}/{$}", h.HomePreferences)

	// User profile pages (apps/api/plane/app/views/workspace/user.py).
	ws.GET("/user-profile/{user_id}/{$}", h.UserProfile)
	ws.GET("/user-stats/{user_id}/{$}", h.UserStats)
	ws.GET("/user-activity/{user_id}/{$}", h.UserActivity)
}
