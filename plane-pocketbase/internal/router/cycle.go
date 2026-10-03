package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/cycle"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
)

// RegisterCycle wires the cycle domain
// (`/api/workspaces/{slug}/projects/{project_id}/cycles/…`).
// Contract: spec/api-spec.yaml, group `cycle`; Django views in
// apps/api/plane/app/views/cycle/**.
//
// Every project route runs through ResolveProject so the handler always has an
// authorized project scope; `{pk}`/`{cycle_id}` are Plane UUIDs.
func RegisterCycle(e *core.ServeEvent, app core.App) error {
	h := cycle.New(app)

	// Workspace-wide cycle list (WorkspaceCyclesEndpoint).
	workspaceGroup(e, app).GET("/cycles/{$}", h.WorkspaceList)

	proj := workspaceGroup(e, app).
		Group("/projects/{project_id}").
		BindFunc(middleware.ResolveProject(app))

	// Cycle CRUD.
	proj.GET("/cycles/{$}", h.List)
	proj.POST("/cycles/{$}", h.Create)
	// `{$}` makes `date-check` an exact match; without it the trailing-slash
	// pattern is a subtree and collides with `…/cycles/{cycle_id}/cycle-issues/`
	// (both would match `/cycles/date-check/cycle-issues/`).
	proj.POST("/cycles/date-check/{$}", h.DateCheck)
	proj.GET("/cycles/{pk}/{$}", h.Detail)
	proj.PUT("/cycles/{pk}/{$}", h.Update)
	proj.PATCH("/cycles/{pk}/{$}", h.Update)
	proj.DELETE("/cycles/{pk}/{$}", h.Delete)

	// Favourites.
	proj.GET("/user-favorite-cycles/{$}", h.FavoritesList)
	proj.POST("/user-favorite-cycles/{$}", h.FavoriteCreate)
	proj.DELETE("/user-favorite-cycles/{cycle_id}/{$}", h.FavoriteDestroy)

	// Cycle issues.
	proj.GET("/cycles/{cycle_id}/cycle-issues/{$}", h.IssuesList)
	proj.POST("/cycles/{cycle_id}/cycle-issues/{$}", h.IssuesCreate)
	proj.GET("/cycles/{cycle_id}/cycle-issues/{issue_id}/{$}", h.IssueDetail)
	proj.PUT("/cycles/{cycle_id}/cycle-issues/{issue_id}/{$}", h.IssueUpdate)
	proj.PATCH("/cycles/{cycle_id}/cycle-issues/{issue_id}/{$}", h.IssueUpdate)
	proj.DELETE("/cycles/{cycle_id}/cycle-issues/{issue_id}/{$}", h.IssueDelete)

	// Rollover, per-user properties, progress and analytics.
	proj.POST("/cycles/{cycle_id}/transfer-issues/{$}", h.TransferIssues)
	proj.GET("/cycles/{cycle_id}/user-properties/{$}", h.UserPropertiesGet)
	proj.PATCH("/cycles/{cycle_id}/user-properties/{$}", h.UserPropertiesPatch)
	proj.GET("/cycles/{cycle_id}/progress/{$}", h.Progress)
	proj.GET("/cycles/{cycle_id}/analytics/{$}", h.Analytics)

	// Archive / unarchive. The `archived-cycles` collection paths are read-only
	// in Django (the POST/DELETE actions there require a `cycle_id` kwarg that
	// the URL does not supply), so only GET is registered.
	proj.GET("/cycles/{cycle_id}/archive/{$}", h.ArchiveGet)
	proj.POST("/cycles/{cycle_id}/archive/{$}", h.Archive)
	proj.DELETE("/cycles/{cycle_id}/archive/{$}", h.Unarchive)
	proj.GET("/archived-cycles/{$}", h.ArchivedList)
	proj.GET("/archived-cycles/{pk}/{$}", h.ArchivedDetail)

	return nil
}
