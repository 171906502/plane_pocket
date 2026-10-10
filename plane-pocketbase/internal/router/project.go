package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/projects"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
)

// registerProject wires /api/workspaces/{slug}/projects/… including the
// project-scoped states and labels collections.
func registerProject(e *core.ServeEvent, app core.App) {
	h := projects.New(app)

	ws := workspaceGroup(e, app)
	// Creating a project needs at least a workspace Member
	// (ProjectViewSet.create: @allow_permission([ADMIN, MEMBER], level="WORKSPACE")).
	ws.GET("/projects/{$}", h.List)
	// `projects/details/` is a sibling of the `{project_id}` route. It must be
	// registered too, otherwise Go's ServeMux matches the literal "details"
	// against `{project_id}` and the lookup 404s.
	//
	// `{$}` anchors the pattern to the end of the path. Without it the trailing
	// slash makes this a *subtree* pattern, which conflicts with
	// `/projects/{project_id}/states/` — both would match
	// `/projects/details/states/` and stdlib ServeMux panics on startup.
	ws.GET("/projects/details/{$}", h.Details)
	ws.POST("/projects/{$}", h.Create).BindFunc(middleware.RequireWorkspaceRole(middleware.RoleMember))

	// Project identifier availability check. NOTE: the web client calls this
	// path WITHOUT a trailing slash (`project.service.ts`), unlike almost every
	// other Plane route, so the bare spelling is registered as well as the
	// Django-style trailing-slash one. A path with no trailing slash is already
	// an exact ServeMux match, so it needs no `{$}`.
	ws.GET("/project-identifiers", h.ProjectIdentifiers).BindFunc(middleware.RequireWorkspaceRole(middleware.RoleMember))
	ws.POST("/project-identifiers", h.ProjectIdentifiers).BindFunc(middleware.RequireWorkspaceRole(middleware.RoleMember))
	ws.GET("/project-identifiers/{$}", h.ProjectIdentifiers).BindFunc(middleware.RequireWorkspaceRole(middleware.RoleMember))
	ws.POST("/project-identifiers/{$}", h.ProjectIdentifiers).BindFunc(middleware.RequireWorkspaceRole(middleware.RoleMember))

	proj := ws.Group("/projects/{project_id}").BindFunc(middleware.ResolveProject(app))
	// `{$}` anchors these to `/projects/{project_id}/` exactly. A bare "/" would
	// make them subtree patterns that answer any unregistered project sub-path
	// with the project object instead of 404.
	proj.GET("/{$}", h.Detail)
	// Editing/deleting a project needs project Admin (or a workspace Admin who
	// is a project member — ResolveProject grants that role).
	proj.PATCH("/{$}", h.Update).BindFunc(middleware.RequireProjectRole(middleware.RoleAdmin))
	proj.DELETE("/{$}", h.Delete).BindFunc(middleware.RequireProjectRole(middleware.RoleAdmin))
	proj.GET("/states/{$}", h.States)
	proj.GET("/labels/{$}", h.Labels)

	// Archive the project (ProjectArchiveUnarchiveEndpoint.post). ADMIN or
	// MEMBER, matching Django's @allow_permission.
	proj.POST("/archive/{$}", h.Archive).BindFunc(middleware.RequireProjectRole(middleware.RoleMember))
	// Unarchive the project (ProjectArchiveUnarchiveEndpoint.delete → 204).
	// The frontend's restoreProject() calls this; without it "Restore project"
	// 404s and the project is stuck in the archived list forever.
	proj.DELETE("/archive/{$}", h.Unarchive).BindFunc(middleware.RequireProjectRole(middleware.RoleMember))

	// Mark a state as the project default (StateViewSet.mark_as_default).
	// Django restricts this to project ADMIN.
	proj.POST("/states/{state_id}/mark-default/{$}", h.MarkDefault).
		BindFunc(middleware.RequireProjectRole(middleware.RoleAdmin))

	// Project sub-resources the web app loads on every project page. `{$}`
	// anchors each pattern to the exact path: without it the trailing slash
	// makes them subtree patterns that also swallow deeper, unregistered paths
	// (and `…/members/` would otherwise collide with `…/members/{pk}/`).
	proj.GET("/project-members/me/{$}", h.MyMembership)
	proj.GET("/members/{$}", h.Members)
	proj.GET("/user-properties/{$}", h.UserProperties)
	proj.PATCH("/user-properties/{$}", h.UserProperties)
	proj.GET("/issue-labels/{$}", h.IssueLabels)
	// Label CRUD (LabelViewSet.create / partial_update / destroy). Django
	// restricts all three to project ADMIN. The GET half existed alone, so
	// creating/editing/deleting a label from project settings 404'd.
	proj.POST("/issue-labels/{$}", h.IssueLabelsCreate).
		BindFunc(middleware.RequireProjectRole(middleware.RoleAdmin))
	proj.PATCH("/issue-labels/{label_id}/{$}", h.IssueLabelsDetail).
		BindFunc(middleware.RequireProjectRole(middleware.RoleAdmin))
	proj.DELETE("/issue-labels/{label_id}/{$}", h.IssueLabelsDetail).
		BindFunc(middleware.RequireProjectRole(middleware.RoleAdmin))
	proj.GET("/estimates/{$}", h.Estimates)

	// Publish (deploy-board) management — spec ids
	// `project.project-deploy-boards.list_create` / `.detail`.
	proj.GET("/project-deploy-boards/{$}", h.DeployBoards)
	proj.POST("/project-deploy-boards/{$}", h.DeployBoards).BindFunc(middleware.RequireProjectRole(middleware.RoleAdmin))
	proj.GET("/project-deploy-boards/{pk}/{$}", h.DeployBoardDetail)
	proj.PATCH("/project-deploy-boards/{pk}/{$}", h.DeployBoardDetail).BindFunc(middleware.RequireProjectRole(middleware.RoleAdmin))
	proj.DELETE("/project-deploy-boards/{pk}/{$}", h.DeployBoardDetail).BindFunc(middleware.RequireProjectRole(middleware.RoleAdmin))
}
