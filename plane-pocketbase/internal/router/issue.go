package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/issues"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
)

// registerIssue wires the project's issue endpoints. Every route runs through
// ResolveProject, so the handler always has an authorized project scope, and
// `{pk}` is a Plane UUID.
func registerIssue(e *core.ServeEvent, app core.App) {
	h := issues.New(app)

	proj := workspaceGroup(e, app).
		Group("/projects/{project_id}").
		BindFunc(middleware.ResolveProject(app))

	proj.GET("/issues/{$}", h.List)
	proj.POST("/issues/{$}", h.Create)
	// Plain, non-paginated array — distinct from the paginated collection above.
	proj.GET("/issues/list/{$}", h.PlainList)
	proj.GET("/issues/{pk}/{$}", h.Detail)
	proj.PATCH("/issues/{pk}/{$}", h.Update)
	proj.DELETE("/issues/{pk}/{$}", h.Delete)

	// Bulk mutations and the archive/deleted/meta views.
	proj.POST("/bulk-archive-issues/{$}", h.BulkArchive)
	proj.DELETE("/bulk-delete-issues/{$}", h.BulkDelete)
	proj.POST("/bulk-operation-issues/{$}", h.BulkOperation)
	proj.POST("/bulk-subscribe-issues/{$}", h.BulkSubscribe)
	proj.GET("/deleted-issues/{$}", h.DeletedIssues)
	proj.GET("/archived-issues/{$}", h.ArchivedIssues)
	proj.GET("/issues/{pk}/meta/{$}", h.Meta)

	// Workspace-level: the profile page's Assigned / Created / Subscribed tabs
	// (WorkspaceUserProfileIssuesEndpoint). It is not project-scoped, so it
	// hangs off the workspace group rather than `proj`.
	ws := workspaceGroup(e, app)
	ws.GET("/user-issues/{user_id}/{$}", h.UserIssues)
}
