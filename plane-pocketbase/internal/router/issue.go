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

	// Wave 2 — the work-item detail side panels. Every path ends in a literal
	// segment or `{$}` so it stays an exact ServeMux match; a trailing "/"
	// would make it a subtree pattern and swallow unrelated sub-paths.
	//
	// `{serviceType}` is spelled out as literal `issues/` routes (Wave 3 adds
	// the `epics/` aliases pointing at the same handlers). The relation and
	// comment-reaction routes use literal `issues`/`comments` because that is
	// what their frontend services call.
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
}
