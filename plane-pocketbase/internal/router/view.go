package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/view"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
)

// RegisterView wires saved views, favourites, stickies and recent visits.
// Contract: spec/api-spec.yaml, groups `view` and `favorite`, plus the
// `workspace.recent-visits` / `workspace.stickies` entries; Django views in
// apps/api/plane/app/views/view/**, .../workspace/favorite.py,
// .../workspace/sticky.py and .../workspace/recent_visit.py.
//
// The project-scoped favourite cycles/modules endpoints live in
// RegisterCycle/RegisterModule and are deliberately not repeated here.
func RegisterView(e *core.ServeEvent, app core.App) error {
	h := view.New(app)

	ws := workspaceGroup(e, app)

	// Workspace-level saved views.
	ws.GET("/views/{$}", h.WorkspaceList)
	ws.POST("/views/{$}", h.WorkspaceCreate)
	ws.GET("/views/{pk}/{$}", h.WorkspaceDetail)
	ws.PUT("/views/{pk}/{$}", h.WorkspaceUpdate)
	ws.PATCH("/views/{pk}/{$}", h.WorkspaceUpdate)
	ws.DELETE("/views/{pk}/{$}", h.WorkspaceDestroy)
	ws.POST("/workspace-views/{$}", h.WorkspaceViewsUpdate)

	// Workspace favourites (user-scoped rows).
	ws.GET("/user-favorites/{$}", h.FavoritesList)
	ws.POST("/user-favorites/{$}", h.FavoriteCreate)
	ws.GET("/user-favorites/{favorite_id}/{$}", h.FavoriteDetail)
	ws.POST("/user-favorites/{favorite_id}/{$}", h.FavoriteCreate)
	ws.PATCH("/user-favorites/{favorite_id}/{$}", h.FavoriteUpdate)
	ws.DELETE("/user-favorites/{favorite_id}/{$}", h.FavoriteDestroy)
	ws.GET("/user-favorites/{favorite_id}/group/{$}", h.FavoriteGroup)

	// Project favourites.
	ws.GET("/user-favorite-projects/{$}", h.ProjectFavoritesList)
	ws.POST("/user-favorite-projects/{$}", h.ProjectFavoriteCreate)
	ws.DELETE("/user-favorite-projects/{project_id}/{$}", h.ProjectFavoriteDestroy)

	// Recent visits.
	ws.GET("/recent-visits/{$}", h.RecentVisitsList)

	// Stickies. Plane's sticky service calls the detail path without a trailing
	// slash, so both spellings are registered.
	ws.GET("/stickies/{$}", h.StickyList)
	ws.POST("/stickies/{$}", h.StickyCreate)
	ws.GET("/stickies/{pk}/{$}", h.StickyDetail)
	ws.PATCH("/stickies/{pk}/{$}", h.StickyUpdate)
	ws.DELETE("/stickies/{pk}/{$}", h.StickyDestroy)
	ws.GET("/stickies/{pk}", h.StickyDetail)
	ws.PATCH("/stickies/{pk}", h.StickyUpdate)
	ws.DELETE("/stickies/{pk}", h.StickyDestroy)

	// Project-scoped saved views and view favourites.
	proj := workspaceGroup(e, app).
		Group("/projects/{project_id}").
		BindFunc(middleware.ResolveProject(app))

	proj.GET("/views/{$}", h.ProjectList)
	proj.POST("/views/{$}", h.ProjectCreate)
	proj.GET("/views/{pk}/{$}", h.ProjectDetail)
	proj.PUT("/views/{pk}/{$}", h.ProjectUpdate)
	proj.PATCH("/views/{pk}/{$}", h.ProjectUpdate)
	proj.DELETE("/views/{pk}/{$}", h.ProjectDestroy)
	// The issues a saved view selects (web `view.service.ts` getViewIssues).
	proj.GET("/views/{view_id}/issues/{$}", h.ProjectIssues)
	proj.POST("/project-views/{$}", h.ProjectViewsUpdate)
	proj.GET("/user-favorite-views/{$}", h.ViewFavoritesList)
	proj.POST("/user-favorite-views/{$}", h.ViewFavoriteCreate)
	proj.DELETE("/user-favorite-views/{view_id}/{$}", h.ViewFavoriteDestroy)

	return nil
}
