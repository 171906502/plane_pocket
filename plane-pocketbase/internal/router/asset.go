package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/asset"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
)

// RegisterAsset wires the file/asset upload domain
// (`/api/assets/v2/workspaces/{slug}/…`, `/api/workspaces/{slug}/file-assets/…`).
// Contract: spec/api-spec.yaml, group `asset` (plus the `/api/assets/v2/…`
// issue-attachment endpoints, which the spec groups under `issue` but which live
// under the /api/assets/v2/ prefix this file owns).
//
// The `asset_url` values the serializers emit resolve to the streaming handlers
// registered here — see the URL-scheme note in internal/handlers/asset/asset.go.
func RegisterAsset(e *core.ServeEvent, app core.App) error {
	h := asset.New(app)

	// Workspace-scoped /api/assets/v2 routes. ResolveWorkspace makes an unknown
	// slug or a non-member a 404.
	v2 := e.Router.Group("/api/assets/v2")
	ws := v2.Group("/workspaces/{slug}").BindFunc(middleware.ResolveWorkspace(app))

	// `{$}` anchors both uploads to the exact collection path; a bare "/" would
	// make them subtree patterns matching every unregistered asset sub-path.
	ws.POST("/{$}", h.WorkspaceUpload)
	ws.GET("/{asset_id}/{$}", h.WorkspaceAssetGet)
	ws.PATCH("/{asset_id}/{$}", h.WorkspaceAssetPatch)
	ws.DELETE("/{asset_id}/{$}", h.WorkspaceAssetDelete)
	ws.GET("/check/{asset_id}/{$}", h.Check)
	ws.POST("/restore/{asset_id}/{$}", h.Restore)
	ws.GET("/download/{asset_id}/{$}", h.Download)
	ws.POST("/duplicate-assets/{asset_id}/{$}", h.Duplicate)

	// Project-scoped v2 routes.
	proj := ws.Group("/projects/{project_id}").BindFunc(middleware.ResolveProject(app))
	proj.POST("/{$}", h.ProjectUpload)
	proj.GET("/{pk}/{$}", h.ProjectAssetGet)
	proj.PATCH("/{pk}/{$}", h.ProjectAssetPatch)
	proj.DELETE("/{pk}/{$}", h.ProjectAssetDelete)
	proj.GET("/download/{asset_id}/{$}", h.ProjectDownload)
	// `{entity_id}/bulk/` and `issues/{issue_id}/attachments/` are both POST
	// subtree patterns under the project; the stdlib mux cannot rank them
	// (neither is a subset of the other), so they share one wildcard route that
	// dispatches on the path. See ProjectSubresource.
	proj.POST("/{entity_id}/{action}/{$}", h.ProjectSubresource)

	// Issue attachments (the asset_url shape ISSUE_ATTACHMENT resolves to).
	att := proj.Group("/issues/{issue_id}")
	att.GET("/attachments/{$}", h.AttachmentList)
	// The upload half of the attachment flow. `AttachmentUpload` already existed
	// but had no route: it was reachable only through ProjectSubresource, which is
	// registered as `/{entity_id}/{action}/{$}` — two segments after
	// `projects/{project_id}` — while this path is three
	// (`issues/{issue_id}/attachments/`). So the POST always fell through to a
	// routing 404 and the handler was dead code, making it impossible to attach a
	// file to a work item (verified: POST → "The requested resource wasn't found.").
	// Three segments cannot overlap the two-segment dispatcher, so it registers
	// cleanly.
	att.POST("/attachments/{$}", h.AttachmentUpload)
	att.GET("/attachments/{pk}/{$}", h.AttachmentGet)
	att.PATCH("/attachments/{pk}/{$}", h.AttachmentPatch)
	att.DELETE("/attachments/{pk}/{$}", h.AttachmentDelete)

	// Static serving (avatars, covers, logos) — AllowAny, as in Django.
	v2.GET("/static/{asset_id}/{$}", h.StaticServe)
	// Our upload receiver, standing in for the S3 presigned POST.
	v2.POST("/upload/{asset_id}/{$}", h.Upload)
	// User assets (no workspace scope).
	v2.POST("/user-assets/{$}", h.UserUpload)
	v2.PATCH("/user-assets/{asset_id}/{$}", h.UserAssetPatch)
	v2.DELETE("/user-assets/{asset_id}/{$}", h.UserAssetDelete)

	// Legacy file-assets routes.
	legacy := workspaceGroup(e, app)
	legacy.GET("/file-assets/{$}", h.LegacyWorkspaceList)
	legacy.POST("/file-assets/{$}", h.LegacyWorkspaceCreate)
	legacy.DELETE("/file-assets/{$}", h.LegacyWorkspaceDelete)

	// The legacy `asset.file-assets.detail` / `…detail.2` paths
	// (`/api/workspaces/file-assets/{workspace_id}/{asset_key}/` and its
	// `restore/` variant).
	//
	// `restore/` is six segments, so it cannot overlap the five-segment
	// `{slug}/workspace-members/me/` route and registers normally. Its five-
	// segment sibling cannot be registered at all — see LegacyFallback — so it is
	// served through the catch-all registered below.
	e.Router.POST("/api/workspaces/file-assets/{workspace_id}/{asset_key}/restore/{$}", h.LegacyWorkspaceRestore)

	// Take over the router's catch-all so the unregisterable five-segment
	// file-asset path can still be served. PocketBase's BuildMux installs its own
	// `"/"` 404 route only when one is absent (tools/router/router.go:68), so
	// this replaces that route with an identical-behaviour one plus a narrow
	// dispatch for the file-asset prefix. Every other unmatched path still gets
	// the framework's exact 404.
	e.Router.Any("/", h.LegacyFallback)

	e.Router.GET("/api/users/file-assets/{$}", h.LegacyUserList)
	e.Router.POST("/api/users/file-assets/{$}", h.LegacyUserCreate)
	e.Router.DELETE("/api/users/file-assets/{$}", h.LegacyUserDelete)
	e.Router.GET("/api/users/file-assets/{asset_key}/{$}", h.LegacyUserDetail)
	e.Router.DELETE("/api/users/file-assets/{asset_key}/{$}", h.LegacyUserDetailDelete)

	return nil
}
