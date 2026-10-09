package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/page"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
)

// RegisterPage wires the page/wiki domain
// (`/api/workspaces/{slug}/projects/{project_id}/pages/…`).
// Contract: spec/api-spec.yaml, group `page`; Django views in
// apps/api/plane/app/views/page/**.
//
// Every route runs through ResolveProject so the handler always has an
// authorized project scope; `{page_id}`/`{pk}` are Plane UUIDs.
func RegisterPage(e *core.ServeEvent, app core.App) error {
	h := page.New(app)

	proj := workspaceGroup(e, app).
		Group("/projects/{project_id}").
		BindFunc(middleware.ResolveProject(app))

	// Summary + collection.
	proj.GET("/pages-summary/{$}", h.Summary)
	proj.GET("/pages/{$}", h.List)
	proj.POST("/pages/{$}", h.Create)

	// Detail CRUD.
	proj.GET("/pages/{page_id}/{$}", h.Detail)
	proj.PATCH("/pages/{page_id}/{$}", h.Update)
	proj.DELETE("/pages/{page_id}/{$}", h.Delete)

	// Favourites. `favorite-pages/` (no page id) is the LIST; the two rows with
	// a `{page_id}` are the add/remove pair. `{$}` keeps each an exact match.
	proj.GET("/favorite-pages/{$}", h.FavoriteList)
	proj.POST("/favorite-pages/{page_id}/{$}", h.FavoriteCreate)
	proj.DELETE("/favorite-pages/{page_id}/{$}", h.FavoriteDestroy)

	// Archived pages (the "Archived" tab).
	proj.GET("/archived-pages/{$}", h.ArchivedList)

	// Archive / unarchive.
	proj.POST("/pages/{page_id}/archive/{$}", h.Archive)
	proj.DELETE("/pages/{page_id}/archive/{$}", h.Unarchive)

	// Lock / unlock.
	proj.POST("/pages/{page_id}/lock/{$}", h.Lock)
	proj.DELETE("/pages/{page_id}/lock/{$}", h.Unlock)

	// Access.
	proj.POST("/pages/{page_id}/access/{$}", h.Access)

	// Move a page to another project in the same workspace.
	proj.POST("/pages/{page_id}/move/{$}", h.Move)

	// Collaborative-editor payload (Yjs binary / HTML / JSON).
	proj.GET("/pages/{page_id}/description/{$}", h.DescriptionGet)
	proj.PATCH("/pages/{page_id}/description/{$}", h.DescriptionPatch)

	// Versions.
	proj.GET("/pages/{page_id}/versions/{$}", h.VersionsList)
	proj.GET("/pages/{page_id}/versions/{pk}/{$}", h.VersionDetail)
	proj.POST("/pages/{page_id}/versions/{version_id}/restore/{$}", h.RestoreVersion)

	// Duplicate.
	proj.POST("/pages/{page_id}/duplicate/{$}", h.Duplicate)

	return nil
}
