package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/asset"
	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/publicapi"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
)

// RegisterPublicAPI wires the open API v1 (`/api/v1/…`), authenticated with
// an `X-Api-Key` header against api_tokens rather than a session cookie.
// Contract: spec/api-spec.yaml, group `publicapi`; Django source
// apps/api/plane/api/**. The domain reuses the app-side asset handlers where
// the v1 contract mirrors them.
func RegisterPublicAPI(e *core.ServeEvent, app core.App) error {
	h := publicapi.New(app)
	a := asset.New(app)
	api := e.Router.Group("/api/v1").BindFunc(middleware.APIKey(app))

	// Schema (docs).
	api.GET("/schema/{$}", h.Schema)
	api.GET("/schema/swagger-ui/{$}", h.SchemaUI)
	api.GET("/schema/redoc/{$}", h.SchemaUI)

	// Users.
	api.GET("/users/me/{$}", h.Me)

	// Assets (mirrors the v2 asset handlers). NOTE: the spec's
	// `user-assets/{asset_id}/server/` variant is NOT registered — Go's mux
	// cannot disambiguate it from `user-assets/server/` (neither pattern is
	// more specific), so only the bare `server/` form is kept.
	api.POST("/assets/user-assets/{$}", a.UserUpload)
	api.PATCH("/assets/user-assets/{asset_id}/{$}", a.UserAssetPatch)
	api.DELETE("/assets/user-assets/{asset_id}/{$}", a.UserAssetDelete)
	api.POST("/assets/user-assets/server/{$}", a.UserUpload)

	ws := api.Group("/workspaces/{slug}")

	// Workspace-level.
	ws.GET("/members/{$}", h.Members)
	ws.GET("/members-lite/{$}", h.MembersLite)
	ws.GET("/projects-lite/{$}", h.ProjectsLite)
	ws.GET("/projects/{$}", h.Projects)
	ws.POST("/projects/{$}", h.Projects)
	ws.GET("/issues/search/{$}", h.Search)
	ws.GET("/work-items/search/{$}", h.Search)
	ws.GET("/work-items/{identifier}/{$}", h.WorkItemByIdentifier)
	ws.GET("/issues/{identifier}/{$}", h.WorkItemByIdentifier)
	ws.GET("/stickies/{$}", h.Stickies)
	ws.POST("/stickies/{$}", h.Stickies)
	ws.GET("/stickies/{pk}/{$}", h.StickyDetail)
	ws.PUT("/stickies/{pk}/{$}", h.StickyDetail)
	ws.PATCH("/stickies/{pk}/{$}", h.StickyDetail)
	ws.DELETE("/stickies/{pk}/{$}", h.StickyDetail)
	ws.GET("/invitations/{$}", h.Invitations)
	ws.POST("/invitations/{$}", h.Invitations)
	ws.GET("/invitations/{pk}/{$}", h.InvitationDetail)
	ws.PUT("/invitations/{pk}/{$}", h.InvitationDetail)
	ws.PATCH("/invitations/{pk}/{$}", h.InvitationDetail)
	ws.DELETE("/invitations/{pk}/{$}", h.InvitationDetail)
	ws.GET("/assets/{$}", a.LegacyWorkspaceList)
	ws.POST("/assets/{$}", a.LegacyWorkspaceCreate)
	ws.GET("/assets/{asset_id}/{$}", a.WorkspaceAssetGet)
	ws.PATCH("/assets/{asset_id}/{$}", a.WorkspaceAssetPatch)
	ws.DELETE("/assets/{asset_id}/{$}", a.WorkspaceAssetDelete)

	// Project-scoped. `{$}` anchors the project detail to its exact path; a
	// bare "/" would make it a subtree pattern answering every unregistered
	// project sub-path with the project object instead of 404.
	proj := ws.Group("/projects/{project_id}")
	proj.GET("/{$}", h.ProjectDetail)
	proj.PATCH("/{$}", h.ProjectDetail)
	proj.DELETE("/{$}", h.ProjectDetail)
	proj.GET("/projects/{pk}/{$}", h.ProjectDetail)
	proj.PATCH("/projects/{pk}/{$}", h.ProjectDetail)
	proj.DELETE("/projects/{pk}/{$}", h.ProjectDetail)
	proj.GET("/summary/{$}", h.Summary)
	proj.GET("/states/{$}", h.States)
	proj.POST("/states/{$}", h.States)
	proj.GET("/states/{state_id}/{$}", h.StateDetail)
	proj.PATCH("/states/{state_id}/{$}", h.StateDetail)
	proj.DELETE("/states/{state_id}/{$}", h.StateDetail)
	proj.GET("/labels/{$}", h.Labels)
	proj.POST("/labels/{$}", h.Labels)
	proj.GET("/labels/{pk}/{$}", h.LabelDetail)
	proj.PATCH("/labels/{pk}/{$}", h.LabelDetail)
	proj.DELETE("/labels/{pk}/{$}", h.LabelDetail)
	proj.GET("/cycles/{$}", h.Cycles)
	proj.POST("/cycles/{$}", h.Cycles)
	proj.GET("/cycles/{pk}/{$}", h.CycleDetail)
	proj.PATCH("/cycles/{pk}/{$}", h.CycleDetail)
	proj.DELETE("/cycles/{pk}/{$}", h.CycleDetail)
	proj.GET("/cycles/{cycle_id}/cycle-issues/{$}", h.CycleIssues)
	proj.POST("/cycles/{cycle_id}/cycle-issues/{$}", h.CycleIssues)
	proj.GET("/cycles/{cycle_id}/cycle-issues/{issue_id}/{$}", h.CycleIssueDetail)
	proj.DELETE("/cycles/{cycle_id}/cycle-issues/{issue_id}/{$}", h.CycleIssueDetail)
	proj.POST("/cycles/{cycle_id}/transfer-issues/{$}", h.CycleTransferIssues)
	proj.POST("/cycles/{cycle_id}/archive/{$}", h.CycleArchive)
	proj.DELETE("/cycles/{cycle_id}/archive/{$}", h.CycleArchive)
	proj.GET("/archived-cycles/{$}", h.ArchivedCycles)
	proj.POST("/archived-cycles/{cycle_id}/unarchive/{$}", h.CycleUnarchive)
	proj.GET("/cycles-lite/{$}", h.CyclesLite)
	proj.GET("/modules/{$}", h.Modules)
	proj.POST("/modules/{$}", h.Modules)
	proj.GET("/modules/{pk}/{$}", h.ModuleDetail)
	proj.PATCH("/modules/{pk}/{$}", h.ModuleDetail)
	proj.DELETE("/modules/{pk}/{$}", h.ModuleDetail)
	proj.GET("/modules/{module_id}/module-issues/{$}", h.ModuleIssues)
	proj.POST("/modules/{module_id}/module-issues/{$}", h.ModuleIssues)
	proj.GET("/modules/{module_id}/module-issues/{issue_id}/{$}", h.ModuleIssueDetail)
	proj.DELETE("/modules/{module_id}/module-issues/{issue_id}/{$}", h.ModuleIssueDetail)
	proj.GET("/modules-lite/{$}", h.ModulesLite)
	proj.GET("/project-members/{$}", h.ProjectMembers)
	proj.POST("/project-members/{$}", h.ProjectMembers)
	proj.GET("/project-members/{pk}/{$}", h.ProjectMemberDetail)
	proj.PATCH("/project-members/{pk}/{$}", h.ProjectMemberDetail)
	proj.DELETE("/project-members/{pk}/{$}", h.ProjectMemberDetail)
	proj.GET("/members/{$}", h.ProjectMembers)
	proj.POST("/members/{$}", h.ProjectMembers)
	proj.GET("/members/{pk}/{$}", h.ProjectMemberDetail)
	proj.PATCH("/members/{pk}/{$}", h.ProjectMemberDetail)
	proj.DELETE("/members/{pk}/{$}", h.ProjectMemberDetail)
	proj.GET("/project-members-lite/{$}", h.ProjectMembersLite)
	proj.GET("/estimates/{$}", h.Estimates)
	proj.POST("/estimates/{$}", h.Estimates)
	proj.PATCH("/estimates/{$}", h.Estimates)
	proj.DELETE("/estimates/{$}", h.Estimates)
	proj.GET("/estimates/{estimate_id}/estimate-points/{$}", h.EstimatePoints)
	proj.POST("/estimates/{estimate_id}/estimate-points/{$}", h.EstimatePoints)
	proj.PATCH("/estimates/{estimate_id}/estimate-points/{estimate_point_id}/{$}", h.EstimatePointDetail)
	proj.DELETE("/estimates/{estimate_id}/estimate-points/{estimate_point_id}/{$}", h.EstimatePointDetail)
	proj.GET("/intake-issues/{$}", h.IntakeIssues)
	proj.POST("/intake-issues/{$}", h.IntakeIssues)
	proj.GET("/intake-issues/{issue_id}/{$}", h.IntakeIssueDetail)
	proj.PATCH("/intake-issues/{issue_id}/{$}", h.IntakeIssueDetail)
	proj.DELETE("/intake-issues/{issue_id}/{$}", h.IntakeIssueDetail)

	// Work items (UUID-addressed).
	proj.GET("/work-items/{$}", h.WorkItems)
	proj.POST("/work-items/{$}", h.WorkItems)
	proj.GET("/issues/{$}", h.WorkItems)
	proj.POST("/issues/{$}", h.WorkItems)
	for _, base := range []string{"work-items", "issues"} {
		// `{$}` keeps the work-item detail to its exact path. Without it the
		// trailing slash makes it a subtree pattern, so any unregistered
		// `/work-items/{id}/…` sub-path would answer with the work item.
		proj.GET("/"+base+"/{pk}/{$}", h.WorkItemDetail)
		proj.PUT("/"+base+"/{pk}/{$}", h.WorkItemDetail)
		proj.PATCH("/"+base+"/{pk}/{$}", h.WorkItemDetail)
		proj.DELETE("/"+base+"/{pk}/{$}", h.WorkItemDetail)
		proj.GET("/"+base+"/{issue_id}/comments/{$}", h.Comments)
		proj.POST("/"+base+"/{issue_id}/comments/{$}", h.Comments)
		proj.GET("/"+base+"/{issue_id}/comments/{pk}/{$}", h.CommentDetail)
		proj.PATCH("/"+base+"/{issue_id}/comments/{pk}/{$}", h.CommentDetail)
		proj.DELETE("/"+base+"/{issue_id}/comments/{pk}/{$}", h.CommentDetail)
		proj.GET("/"+base+"/{issue_id}/links/{$}", h.Links)
		proj.POST("/"+base+"/{issue_id}/links/{$}", h.Links)
		proj.GET("/"+base+"/{issue_id}/links/{pk}/{$}", h.LinkDetail)
		proj.PATCH("/"+base+"/{issue_id}/links/{pk}/{$}", h.LinkDetail)
		proj.DELETE("/"+base+"/{issue_id}/links/{pk}/{$}", h.LinkDetail)
		proj.GET("/"+base+"/{issue_id}/activities/{$}", h.Activities)
		proj.GET("/"+base+"/{issue_id}/activities/{pk}/{$}", h.ActivityDetail)
		proj.GET("/"+base+"/{issue_id}/relations/{$}", h.Relations)
		proj.POST("/"+base+"/{issue_id}/relations/{$}", h.Relations)
		proj.GET("/"+base+"/{issue_id}/attachments/{$}", h.IssueAttachments)
		proj.POST("/"+base+"/{issue_id}/attachments/{$}", h.IssueAttachments)
		proj.GET("/"+base+"/{issue_id}/attachments/{pk}/{$}", h.IssueAttachmentDetail)
		proj.PATCH("/"+base+"/{issue_id}/attachments/{pk}/{$}", h.IssueAttachmentDetail)
		proj.DELETE("/"+base+"/{issue_id}/attachments/{pk}/{$}", h.IssueAttachmentDetail)
	}

	return nil
}
