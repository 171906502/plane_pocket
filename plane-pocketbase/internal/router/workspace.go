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

	// Workspace-level work-item reads (cross-project).
	//
	// `/issues/` is WorkspaceViewIssuesViewSet.list (plane/app/urls/views.py).
	// `/issues-detail/` and `/my-issues/` are frontend call sites with no Django
	// view in this checkout; see the handler doc comments for how each shape was
	// derived. All three are anchored so an unknown sibling path 404s instead of
	// being answered with an issue list.
	ws.GET("/issues/{$}", h.WorkspaceIssues)
	ws.GET("/issues-detail/{$}", h.WorkspaceIssuesDetail)
	ws.GET("/my-issues/{$}", h.MyIssues)

	// Workspace labels (plane/app/views/workspace/label.py,
	// WorkspaceLabelsEndpoint — GET only in Django). The mutating verbs reuse
	// LabelSerializer so they stay wire-compatible; see handlers/workspaces/labels.go.
	ws.GET("/labels/{$}", h.WorkspaceLabels)
	ws.POST("/labels/{$}", h.WorkspaceLabels)
	ws.PATCH("/labels/{label_id}/{$}", h.WorkspaceLabelDetail)
	ws.DELETE("/labels/{label_id}/{$}", h.WorkspaceLabelDetail)

	// Workspace quick links (plane/app/views/workspace/quick_link.py,
	// QuickLinkViewSet), backed by the existing `workspace_user_links` collection.
	ws.GET("/quick-links/{$}", h.QuickLinks)
	ws.POST("/quick-links/{$}", h.QuickLinks)
	ws.GET("/quick-links/{link_id}/{$}", h.QuickLinkDetail)
	ws.PATCH("/quick-links/{link_id}/{$}", h.QuickLinkDetail)
	ws.DELETE("/quick-links/{link_id}/{$}", h.QuickLinkDetail)

	// Workspace invitations (plane/app/views/workspace/invite.py,
	// WorkspaceInvitationsViewset). The viewset's WorkSpaceAdminPermission
	// admits Admins and Members (role >= 15), not Guests.
	ws.GET("/invitations/{$}", h.Invitations).BindFunc(middleware.RequireWorkspaceRole(middleware.RoleMember))
	ws.POST("/invitations/{$}", h.Invitations).BindFunc(middleware.RequireWorkspaceRole(middleware.RoleMember))
	ws.PATCH("/invitations/{ik}/{$}", h.InvitationDetail).BindFunc(middleware.RequireWorkspaceRole(middleware.RoleMember))
	ws.DELETE("/invitations/{ik}/{$}", h.InvitationDetail).BindFunc(middleware.RequireWorkspaceRole(middleware.RoleMember))

	// The join endpoint is deliberately NOT under `ws`: it must answer an
	// invited person who has no session yet, so it cannot run the
	// ResolveWorkspace membership check (which 404s non-members). Django gives
	// it AllowAny (WorkspaceJoinEndpoint); the handler resolves the workspace
	// itself and gates acceptance on the invite token + the invited address.
	e.Router.GET("/api/workspaces/{slug}/invitations/{ik}/join/{$}", h.InvitationJoin)
	e.Router.POST("/api/workspaces/{slug}/invitations/{ik}/join/{$}", h.InvitationJoin)

	// Leaving a workspace / a project (WorkSpaceMemberViewSet.leave,
	// ProjectMemberViewSet.leave). Note the workspace-level path is
	// `/members/leave/`, NOT `/api/members/leave/`.
	ws.POST("/members/leave/{$}", h.LeaveWorkspace)

	proj := workspaceGroup(e, app).
		Group("/projects/{project_id}").
		BindFunc(middleware.ResolveProject(app))
	proj.POST("/members/leave/{$}", h.LeaveProject)
}
