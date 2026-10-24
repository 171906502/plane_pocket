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
	// v2 paginated list (IssuePaginatedViewSet) — same envelope, route kept so
	// the client's sync path resolves.
	proj.GET("/v2/issues/{$}", h.ListV2)
	// Plain, non-paginated array — distinct from the paginated collection above.
	proj.GET("/issues/list/{$}", h.PlainList)
	proj.GET("/issues/{pk}/{$}", h.Detail)
	proj.PATCH("/issues/{pk}/{$}", h.Update)
	proj.DELETE("/issues/{pk}/{$}", h.Delete)
	// List + per-issue relation expansion (IssueDetailEndpoint). The workspace
	// level has its own /issues-detail/; this is the project-scoped one the
	// client switches to when `expand=issue_relation`.
	proj.GET("/issues-detail/{$}", h.ListDetail)

	// Bulk mutations and the archive/deleted/meta views.
	proj.POST("/bulk-archive-issues/{$}", h.BulkArchive)
	proj.DELETE("/bulk-delete-issues/{$}", h.BulkDelete)
	proj.POST("/bulk-operation-issues/{$}", h.BulkOperation)
	proj.POST("/bulk-subscribe-issues/{$}", h.BulkSubscribe)
	proj.GET("/deleted-issues/{$}", h.DeletedIssues)
	proj.GET("/archived-issues/{$}", h.ArchivedIssues)
	proj.GET("/issues/{pk}/meta/{$}", h.Meta)

	// Wave 2 — the work-item detail side panels. Every path ends in a literal
	// segment or `{$}` so it stays an exact ServeMux match; a trailing "/"
	// would make it a subtree pattern and swallow unrelated sub-paths.
	//
	// The frontend has a generic `{serviceType}` branch for `issues` and
	// `epics`, but the bundled Django URLs/spec define no `/epics/` endpoints,
	// and the empty-database product flow cannot create epics (see
	// docs/08-IMPLEMENTATION-PLAN.md §11.1). Do not add aliases here: that would
	// invent an upstream contract. Relations and comment reactions use literal
	// `issues`/`comments` paths, matching their frontend services.
	proj.GET("/issues/{issue_id}/history/{$}", h.History)

	// Description versions live under the literal `work-items/` prefix in
	// Django and the web client; `issues/` is registered as an alias for the
	// brief's `{serviceType}` shape.
	proj.GET("/work-items/{work_item_id}/description-versions/{$}", h.DescriptionVersions)
	proj.GET("/work-items/{work_item_id}/description-versions/{pk}/{$}", h.DescriptionVersionDetail)
	proj.GET("/issues/{issue_id}/description-versions/{$}", h.DescriptionVersions)
	proj.GET("/issues/{issue_id}/description-versions/{pk}/{$}", h.DescriptionVersionDetail)

	proj.GET("/issues/{issue_id}/comments/{$}", h.ListComments)
	proj.POST("/issues/{issue_id}/comments/{$}", h.CreateComment)
	proj.PATCH("/issues/{issue_id}/comments/{pk}/{$}", h.UpdateComment)
	proj.DELETE("/issues/{issue_id}/comments/{pk}/{$}", h.DeleteComment)

	proj.GET("/issues/{issue_id}/subscribe/{$}", h.SubscriptionStatus)
	proj.POST("/issues/{issue_id}/subscribe/{$}", h.Subscribe)
	proj.DELETE("/issues/{issue_id}/subscribe/{$}", h.Unsubscribe)

	proj.GET("/issues/{issue_id}/reactions/{$}", h.ListReactions)
	proj.POST("/issues/{issue_id}/reactions/{$}", h.CreateReaction)
	proj.DELETE("/issues/{issue_id}/reactions/{reaction}/{$}", h.DeleteReaction)

	proj.GET("/issues/{issue_id}/issue-relation/{$}", h.ListRelations)
	proj.POST("/issues/{issue_id}/issue-relation/{$}", h.CreateRelations)
	proj.DELETE("/issues/{issue_id}/issue-relation/{related_id}/{$}", h.DeleteRelation)
	proj.POST("/issues/{issue_id}/remove-relation/{$}", h.RemoveRelation)

	// Sub-issue tree + single-issue archive (method-level audit gaps; see
	// internal/handlers/issues/wave3.go for the Django contract notes).
	proj.GET("/issues/{issue_id}/sub-issues/{$}", h.SubIssues)
	proj.POST("/issues/{issue_id}/sub-issues/{$}", h.AddSubIssues)

	proj.GET("/issues/{issue_id}/issue-links/{$}", h.ListIssueLinks)
	proj.POST("/issues/{issue_id}/issue-links/{$}", h.CreateIssueLink)
	proj.GET("/issues/{issue_id}/issue-links/{pk}/{$}", h.IssueLinkDetail)
	proj.PATCH("/issues/{issue_id}/issue-links/{pk}/{$}", h.IssueLinkDetail)
	proj.PUT("/issues/{issue_id}/issue-links/{pk}/{$}", h.IssueLinkDetail)
	proj.DELETE("/issues/{issue_id}/issue-links/{pk}/{$}", h.IssueLinkDetail)

	proj.GET("/issues/{pk}/archive/{$}", h.ArchivedIssueDetail)
	proj.POST("/issues/{pk}/archive/{$}", h.ArchiveIssue)
	proj.DELETE("/issues/{pk}/archive/{$}", h.UnarchiveIssue)

	proj.GET("/comments/{comment_id}/reactions/{$}", h.ListCommentReactions)
	proj.POST("/comments/{comment_id}/reactions/{$}", h.CreateCommentReaction)
	proj.DELETE("/comments/{comment_id}/reactions/{reaction}/{$}", h.DeleteCommentReaction)

	// issue-dates is POST in both the Django view and the web client (the brief
	// said GET — corrected). issue-display-properties is GET+POST in the client
	// and GET+PATCH in the brief; both are registered.
	proj.POST("/issue-dates/{$}", h.BulkUpdateDates)
	proj.GET("/issue-display-properties/{$}", h.DisplayProperties)
	proj.POST("/issue-display-properties/{$}", h.UpdateDisplayProperties)
	proj.PATCH("/issue-display-properties/{$}", h.UpdateDisplayProperties)

	proj.GET("/epics-user-properties/{$}", h.EpicUserProperties)
	proj.PATCH("/epics-user-properties/{$}", h.UpdateEpicUserProperties)

	// Workspace-level: the profile page's Assigned / Created / Subscribed tabs
	// (WorkspaceUserProfileIssuesEndpoint). It is not project-scoped, so it
	// hangs off the workspace group rather than `proj`.
	ws := workspaceGroup(e, app)
	ws.GET("/user-issues/{user_id}/{$}", h.UserIssues)
	// Deep link by human reference ("ENG-42"; IssueDetailIdentifierEndpoint).
	// Workspace-scoped — the identifier names the project, not a {project_id}.
	ws.GET("/work-items/{ref}/{$}", h.DetailByIdentifier)
}
