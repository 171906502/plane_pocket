package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/module"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
)

// RegisterModule wires the module domain
// (`/api/workspaces/{slug}/projects/{project_id}/modules/…`).
// Contract: spec/api-spec.yaml, group `module`; Django views in
// apps/api/plane/app/views/module/**.
//
// The module-issue bridge (`…/modules/{module_id}/issues/` and
// `…/issues/{issue_id}/modules/`) and module links are registered here too,
// since they share the ModuleIssueViewSet/ModuleLinkViewSet handlers.
func RegisterModule(e *core.ServeEvent, app core.App) error {
	h := module.New(app)

	// Workspace-wide module list (WorkspaceModulesEndpoint).
	workspaceGroup(e, app).GET("/modules/{$}", h.WorkspaceList)

	proj := workspaceGroup(e, app).
		Group("/projects/{project_id}").
		BindFunc(middleware.ResolveProject(app))

	// Module CRUD.
	proj.GET("/modules/{$}", h.List)
	proj.POST("/modules/{$}", h.Create)
	proj.GET("/modules/{pk}/{$}", h.Detail)
	proj.PUT("/modules/{pk}/{$}", h.Update)
	proj.PATCH("/modules/{pk}/{$}", h.Update)
	proj.DELETE("/modules/{pk}/{$}", h.Delete)

	// Favourites.
	proj.GET("/user-favorite-modules/{$}", h.FavoritesList)
	proj.POST("/user-favorite-modules/{$}", h.FavoriteCreate)
	proj.DELETE("/user-favorite-modules/{module_id}/{$}", h.FavoriteDestroy)

	// Per-user properties.
	proj.GET("/modules/{module_id}/user-properties/{$}", h.UserPropertiesGet)
	proj.PATCH("/modules/{module_id}/user-properties/{$}", h.UserPropertiesPatch)

	// Module issues (both directions of the bridge).
	proj.GET("/modules/{module_id}/issues/{$}", h.IssuesList)
	proj.POST("/modules/{module_id}/issues/{$}", h.IssuesCreate)
	proj.POST("/issues/{issue_id}/modules/{$}", h.IssueModulesCreate)
	proj.GET("/modules/{module_id}/issues/{issue_id}/{$}", h.IssueDetail)
	proj.PUT("/modules/{module_id}/issues/{issue_id}/{$}", h.IssueUpdate)
	proj.PATCH("/modules/{module_id}/issues/{issue_id}/{$}", h.IssueUpdate)
	proj.DELETE("/modules/{module_id}/issues/{issue_id}/{$}", h.IssueDelete)

	// Module links.
	proj.GET("/modules/{module_id}/module-links/{$}", h.LinksList)
	proj.POST("/modules/{module_id}/module-links/{$}", h.LinkCreate)
	proj.GET("/modules/{module_id}/module-links/{pk}/{$}", h.LinkDetail)
	proj.PUT("/modules/{module_id}/module-links/{pk}/{$}", h.LinkUpdate)
	proj.PATCH("/modules/{module_id}/module-links/{pk}/{$}", h.LinkUpdate)
	proj.DELETE("/modules/{module_id}/module-links/{pk}/{$}", h.LinkDelete)

	// Archive / unarchive (read-only archived collection paths, as with cycles).
	proj.GET("/modules/{module_id}/archive/{$}", h.ArchiveGet)
	proj.POST("/modules/{module_id}/archive/{$}", h.Archive)
	proj.DELETE("/modules/{module_id}/archive/{$}", h.Unarchive)
	proj.GET("/archived-modules/{$}", h.ArchivedList)
	proj.GET("/archived-modules/{pk}/{$}", h.ArchivedDetail)

	return nil
}
