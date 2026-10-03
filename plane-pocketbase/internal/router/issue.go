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
}
