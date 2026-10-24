// Wave-2 endpoints for the issue domain: the work-item detail side panels.
// This file is deliberately separate from issues.go / wave1.go so the core CRUD
// path stays readable; all three share the package, the Handler type and its
// helpers.
//
//	GET    /api/workspaces/{slug}/projects/{pid}/issues/{issue_id}/history/
//	GET    /api/workspaces/{slug}/projects/{pid}/work-items/{wid}/description-versions/
//	GET    /api/workspaces/{slug}/projects/{pid}/work-items/{wid}/description-versions/{pk}/
//	GET/POST   /api/workspaces/{slug}/projects/{pid}/issues/{issue_id}/comments/
//	PATCH/DELETE /api/workspaces/{slug}/projects/{pid}/issues/{issue_id}/comments/{pk}/
//	GET/POST/DELETE /api/workspaces/{slug}/projects/{pid}/issues/{issue_id}/subscribe/
//	GET/POST   /api/workspaces/{slug}/projects/{pid}/issues/{issue_id}/reactions/
//	DELETE     /api/workspaces/{slug}/projects/{pid}/issues/{issue_id}/reactions/{reaction}/
//	GET/POST   /api/workspaces/{slug}/projects/{pid}/issues/{issue_id}/issue-relation/
//	DELETE     /api/workspaces/{slug}/projects/{pid}/issues/{issue_id}/issue-relation/{related}/
//	POST       /api/workspaces/{slug}/projects/{pid}/issues/{issue_id}/remove-relation/
//	GET/POST   /api/workspaces/{slug}/projects/{pid}/comments/{comment_id}/reactions/
//	DELETE     /api/workspaces/{slug}/projects/{pid}/comments/{comment_id}/reactions/{reaction}/
//	POST       /api/workspaces/{slug}/projects/{pid}/issue-dates/
//	GET/PATCH  /api/workspaces/{slug}/projects/{pid}/issue-display-properties/
//	GET/PATCH  /api/workspaces/{slug}/projects/{pid}/epics-user-properties/
//
// Contract: apps/api/plane/app/urls/issue.py and the views under
// apps/api/plane/app/views/issue/. `issue-display-properties/` and
// `epics-user-properties/` have no Django counterpart in the bundled api
// snapshot (they exist only in the web client); their shape is derived from
// ProjectUserPropertySerializer and is flagged inline.
package issues

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// ---------------------------------------------------------------------------
// shared lookups
// ---------------------------------------------------------------------------

// projectIssue resolves an issue UUID inside the resolved project. Unlike
// findIssue it does not reject soft-deleted/archived rows, because Django's
// sub-resource views read `Issue.objects` (the plain manager), not
// `Issue.issue_objects`. Returns nil when the issue is absent or foreign.
func (h *Handler) projectIssue(scope *middleware.Scope, uuid string) *core.Record {
	issue, _ := serializers.FindByUUID(h.App, "issues", uuid)
	if issue == nil || issue.GetString("project") != scope.Project.Id {
		return nil
	}
	return issue
}

// findRecord resolves a relation column to its record by collection name.
func (h *Handler) findRecord(collection, id string) *core.Record {
	if id == "" {
		return nil
	}
	record, _ := h.App.FindRecordById(collection, id)
	return record
}

// pathUUID returns the first non-empty route parameter among names. It exists
// because the same handler is reachable through more than one literal prefix
// (`issues/` and `work-items/`) with different parameter names.
func pathUUID(e *core.RequestEvent, names ...string) string {
	for _, name := range names {
		if v := e.Request.PathValue(name); v != "" {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// GET …/{serviceType}/{issue_id}/history/  (IssueActivityEndpoint)
// ---------------------------------------------------------------------------

// History serves GET …/issues/{issue_id}/history/ (IssueActivityEndpoint.get).
//
// It is a plain array, not a paginated envelope. `activity_type` selects the
// projection: "issue-property" returns property-change activities, "issue-
// comment" returns comments, anything else (including absent) returns the two
// lists merged and sorted by `created_at` — exactly Django's `chain(...)`.
func (h *Handler) History(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id", "pk"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}

	query := e.Request.URL.Query()
	createdAtGt := query.Get("created_at__gt")
	isMember := scope.ProjectMember != nil

	switch query.Get("activity_type") {
	case "issue-property":
		return e.JSON(http.StatusOK, h.issueActivities(issue, createdAtGt))
	case "issue-comment":
		return e.JSON(http.StatusOK, h.issueCommentList(issue, createdAtGt, isMember))
	}

	merged := make([]map[string]any, 0)
	merged = append(merged, h.issueActivities(issue, createdAtGt)...)
	merged = append(merged, h.issueCommentList(issue, createdAtGt, isMember)...)
	sort.SliceStable(merged, func(i, j int) bool {
		return asString(merged[i]["created_at"]) < asString(merged[j]["created_at"])
	})
	return e.JSON(http.StatusOK, merged)
}

// issueActivities returns the issue's property-change activities: the
// IssueActivity rows whose `field` is not one of the comment/vote/reaction/
// draft sentinels (Django's `~Q(field__in=[...])`).
func (h *Handler) issueActivities(issue *core.Record, createdAtGt string) []map[string]any {
	filter := "issue = {:issue} && field != \"comment\" && field != \"vote\" && field != \"reaction\" && field != \"draft\""
	params := dbx.Params{"issue": issue.Id}
	if createdAtGt != "" {
		filter += " && created_at > {:gt}"
		params["gt"] = createdAtGt
	}
	rows, err := h.App.FindRecordsByFilter("issue_activities", filter, "created_at", 0, 0, params)
	out := make([]map[string]any, 0)
	if err != nil {
		return out
	}
	for _, row := range rows {
		out = append(out, serializers.IssueActivity(h.App, row))
	}
	return out
}

// issueCommentList returns the issue's comments as IssueCommentSerializer
// objects.
func (h *Handler) issueCommentList(issue *core.Record, createdAtGt string, isMember bool) []map[string]any {
	filter := "issue = {:issue}"
	params := dbx.Params{"issue": issue.Id}
	if createdAtGt != "" {
		filter += " && created_at > {:gt}"
		params["gt"] = createdAtGt
	}
	rows, err := h.App.FindRecordsByFilter("issue_comments", filter, "created_at", 0, 0, params)
	out := make([]map[string]any, 0)
	if err != nil {
		return out
	}
	for _, row := range rows {
		out = append(out, h.commentJSON(row, isMember))
	}
	return out
}

// ---------------------------------------------------------------------------
// comments
// ---------------------------------------------------------------------------

// ListComments serves GET …/issues/{issue_id}/comments/.
//
// IssueCommentViewSet does not override `list`, and Plane configures no DRF
// DEFAULT_PAGINATION_CLASS, so the response is a plain array of
// IssueCommentSerializer objects.
func (h *Handler) ListComments(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	return e.JSON(http.StatusOK, h.issueCommentList(issue, e.Request.URL.Query().Get("created_at__gt"), scope.ProjectMember != nil))
}

// CreateComment serves POST …/issues/{issue_id}/comments/ (IssueCommentViewSet.create).
func (h *Handler) CreateComment(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	// A Guest may only comment when the project shares all features or they
	// created the issue (IssueCommentViewSet.create).
	if scope.ProjectRole == middleware.RoleGuest && !scope.Project.GetBool("guest_view_all_features") && !middleware.IsCreator(e, issue) {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "You are not allowed to comment on the issue"})
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}

	collection, err := h.App.FindCollectionByNameOrId("issue_comments")
	if err != nil {
		return err
	}
	comment := core.NewRecord(collection)
	comment.Set("uuid", serializers.NewUUID())
	comment.Set("project", scope.Project.Id)
	comment.Set("workspace", scope.Project.GetString("workspace"))
	comment.Set("issue", issue.Id)
	comment.Set("actor", user.Id)
	comment.Set("created_by", user.Id)
	h.applyCommentBody(comment, body)
	if err := h.App.Save(comment); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusCreated, h.commentJSON(comment, scope.ProjectMember != nil))
}

// UpdateComment serves PATCH …/issues/{issue_id}/comments/{pk}/.
// Permission: project ADMIN or the comment's creator.
func (h *Handler) UpdateComment(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	comment, err := h.findComment(scope, e.Request.PathValue("issue_id"), e.Request.PathValue("pk"))
	if err != nil {
		return err
	}
	if comment == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	if !middleware.IsCreator(e, comment) && !middleware.ProjectRoleAtLeast(e, middleware.RoleAdmin) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	if html := asString(body["comment_html"]); html != "" && html != comment.GetString("comment_html") {
		comment.Set("edited_at", serializers.Now())
	}
	h.applyCommentBody(comment, body)
	if user := middleware.CurrentUser(e); user != nil {
		comment.Set("updated_by", user.Id)
	}
	if err := h.App.Save(comment); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, h.commentJSON(comment, scope.ProjectMember != nil))
}

// DeleteComment serves DELETE …/issues/{issue_id}/comments/{pk}/.
// Permission: project ADMIN or the comment's creator. Django hard-deletes.
func (h *Handler) DeleteComment(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	comment, err := h.findComment(scope, e.Request.PathValue("issue_id"), e.Request.PathValue("pk"))
	if err != nil {
		return err
	}
	if comment == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	if !middleware.IsCreator(e, comment) && !middleware.ProjectRoleAtLeast(e, middleware.RoleAdmin) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	if err := h.App.Delete(comment); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// findComment resolves a comment UUID within the resolved project + issue.
func (h *Handler) findComment(scope *middleware.Scope, issueUUID, commentUUID string) (*core.Record, error) {
	comment, err := serializers.FindByUUID(h.App, "issue_comments", commentUUID)
	if err != nil || comment == nil {
		return nil, err
	}
	issue, _ := serializers.FindByUUID(h.App, "issues", issueUUID)
	if issue == nil || comment.GetString("issue") != issue.Id || comment.GetString("project") != scope.Project.Id {
		return nil, nil
	}
	return comment, nil
}

// applyCommentBody writes the writable comment fields, mirroring the
// IssueCommentSerializer. `comment_stripped` is derived from `comment_html`
// (Django's model save runs strip_tags).
func (h *Handler) applyCommentBody(comment *core.Record, body map[string]any) {
	for _, field := range []string{"comment_json", "attachments", "access", "external_source", "external_id"} {
		if v, ok := body[field]; ok {
			comment.Set(field, v)
		}
	}
	if v, ok := body["comment_html"]; ok {
		comment.Set("comment_html", v)
	}
	comment.Set("comment_stripped", stripTags(asString(comment.GetString("comment_html"))))
}

// commentJSON mirrors IssueCommentSerializer (fields="__all__" plus the four
// nested read-only projections, `comment_reactions` and `is_member`).
func (h *Handler) commentJSON(r *core.Record, isMember bool) map[string]any {
	return map[string]any{
		"id":               serializers.UUID(r),
		"comment_stripped": serializers.Text(r, "comment_stripped"),
		"comment_json":     serializers.JSONOrDefault(r, "comment_json", map[string]any{}),
		"comment_html":     serializers.Text(r, "comment_html"),
		"description":      serializers.RelationUUID(h.App, r, "description"),
		"attachments":      serializers.JSONOrDefault(r, "attachments", []any{}),
		"issue":            serializers.RelationUUID(h.App, r, "issue"),
		"actor":            serializers.RelationUUID(h.App, r, "actor"),
		"access":           serializers.Text(r, "access"),
		"external_source":  serializers.NullableText(r, "external_source"),
		"external_id":      serializers.NullableText(r, "external_id"),
		"edited_at":        serializers.NullableDateTime(r, "edited_at"),
		// The self-relation `parent` is deferred in the PocketBase schema
		// (§6.4 D12); Django would emit the FK UUID. Always null here.
		"parent":            nil,
		"created_at":        serializers.DateTime(r, "created_at"),
		"updated_at":        serializers.DateTime(r, "updated_at"),
		"created_by":        serializers.RelationUUID(h.App, r, "created_by"),
		"updated_by":        serializers.RelationUUID(h.App, r, "updated_by"),
		"deleted_at":        serializers.NullableDateTime(r, "deleted_at"),
		"project":           serializers.RelationUUID(h.App, r, "project"),
		"workspace":         serializers.RelationUUID(h.App, r, "workspace"),
		"actor_detail":      serializers.UserLite(h.App, h.findRecord("users", r.GetString("actor"))),
		"issue_detail":      serializers.IssueFlat(h.findRecord("issues", r.GetString("issue"))),
		"project_detail":    serializers.ProjectLite(h.findRecord("projects", r.GetString("project"))),
		"workspace_detail":  serializers.WorkspaceLite(h.findRecord("workspaces", r.GetString("workspace"))),
		"comment_reactions": h.commentReactions(r),
		"is_member":         isMember,
	}
}

// commentReactions resolves the comment's CommentReaction rows.
func (h *Handler) commentReactions(comment *core.Record) []map[string]any {
	rows, err := h.App.FindRecordsByFilter(
		"comment_reactions",
		"comment = {:comment} && deleted_at = ''",
		"created_at", 0, 0,
		dbx.Params{"comment": comment.Id},
	)
	out := make([]map[string]any, 0)
	if err != nil {
		return out
	}
	for _, row := range rows {
		out = append(out, h.commentReactionJSON(row))
	}
	return out
}

// commentReactionJSON mirrors CommentReactionSerializer.
func (h *Handler) commentReactionJSON(r *core.Record) map[string]any {
	displayName := ""
	if actor := h.findRecord("users", r.GetString("actor")); actor != nil {
		displayName = actor.GetString("display_name")
	}
	return map[string]any{
		"id":           serializers.UUID(r),
		"actor":        serializers.RelationUUID(h.App, r, "actor"),
		"comment":      serializers.RelationUUID(h.App, r, "comment"),
		"reaction":     serializers.Text(r, "reaction"),
		"display_name": displayName,
		"deleted_at":   serializers.NullableDateTime(r, "deleted_at"),
		"workspace":    serializers.RelationUUID(h.App, r, "workspace"),
		"project":      serializers.RelationUUID(h.App, r, "project"),
		"created_at":   serializers.DateTime(r, "created_at"),
		"updated_at":   serializers.DateTime(r, "updated_at"),
		"created_by":   serializers.RelationUUID(h.App, r, "created_by"),
		"updated_by":   serializers.RelationUUID(h.App, r, "updated_by"),
	}
}

// ---------------------------------------------------------------------------
// subscribe
// ---------------------------------------------------------------------------

// SubscriptionStatus serves GET …/issues/{issue_id}/subscribe/
// (IssueSubscriberViewSet.subscription_status) — `{"subscribed": bool}` for the
// calling user.
func (h *Handler) SubscriptionStatus(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	subscribed := false
	if row, _ := h.findSubscriber(issue, user); row != nil {
		subscribed = true
	}
	return e.JSON(http.StatusOK, map[string]any{"subscribed": subscribed})
}

// Subscribe serves POST …/issues/{issue_id}/subscribe/ (IssueSubscriberViewSet.subscribe).
func (h *Handler) Subscribe(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	if row, _ := h.findSubscriber(issue, user); row != nil {
		return e.JSON(http.StatusBadRequest, map[string]any{"message": "User already subscribed to the issue."})
	}

	collection, err := h.App.FindCollectionByNameOrId("issue_subscribers")
	if err != nil {
		return err
	}
	row := core.NewRecord(collection)
	row.Set("uuid", serializers.NewUUID())
	row.Set("issue", issue.Id)
	row.Set("subscriber", user.Id)
	row.Set("project", scope.Project.Id)
	row.Set("workspace", scope.Project.GetString("workspace"))
	row.Set("created_by", user.Id)
	if err := h.App.Save(row); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusCreated, h.subscriberJSON(row))
}

// Unsubscribe serves DELETE …/issues/{issue_id}/subscribe/ (IssueSubscriberViewSet.unsubscribe).
func (h *Handler) Unsubscribe(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	row, err := h.findSubscriber(issue, user)
	if err != nil {
		return err
	}
	if row == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	if err := h.App.Delete(row); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// findSubscriber returns the live subscription row for (issue, user).
func (h *Handler) findSubscriber(issue, user *core.Record) (*core.Record, error) {
	row, err := h.App.FindFirstRecordByFilter(
		"issue_subscribers",
		"issue = {:issue} && subscriber = {:subscriber} && deleted_at = ''",
		dbx.Params{"issue": issue.Id, "subscriber": user.Id},
	)
	if err != nil || row == nil {
		return nil, nil
	}
	return row, nil
}

// subscriberJSON mirrors IssueSubscriberSerializer (fields="__all__").
func (h *Handler) subscriberJSON(r *core.Record) map[string]any {
	return map[string]any{
		"id":         serializers.UUID(r),
		"issue":      serializers.RelationUUID(h.App, r, "issue"),
		"subscriber": serializers.RelationUUID(h.App, r, "subscriber"),
		"project":    serializers.RelationUUID(h.App, r, "project"),
		"workspace":  serializers.RelationUUID(h.App, r, "workspace"),
		"created_at": serializers.DateTime(r, "created_at"),
		"updated_at": serializers.DateTime(r, "updated_at"),
		"created_by": serializers.RelationUUID(h.App, r, "created_by"),
		"updated_by": serializers.RelationUUID(h.App, r, "updated_by"),
		"deleted_at": serializers.NullableDateTime(r, "deleted_at"),
	}
}

// ---------------------------------------------------------------------------
// issue reactions
// ---------------------------------------------------------------------------

// ListReactions serves GET …/issues/{issue_id}/reactions/.
func (h *Handler) ListReactions(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	rows, err := h.App.FindRecordsByFilter(
		"issue_reactions",
		"issue = {:issue} && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"issue": issue.Id},
	)
	out := make([]map[string]any, 0)
	if err != nil {
		return e.JSON(http.StatusOK, out)
	}
	for _, row := range rows {
		out = append(out, h.issueReactionJSON(row))
	}
	return e.JSON(http.StatusOK, out)
}

// CreateReaction serves POST …/issues/{issue_id}/reactions/
// (IssueReactionViewSet.create).
func (h *Handler) CreateReaction(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	reaction := asString(body["reaction"])
	if reaction == "" {
		return serializers.Validation(e, "reaction", "This field is required.")
	}
	if existing, _ := h.findReaction("issue_reactions", "issue", issue.Id, user.Id, reaction); existing != nil {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Reaction already exists for the user"})
	}

	collection, err := h.App.FindCollectionByNameOrId("issue_reactions")
	if err != nil {
		return err
	}
	row := core.NewRecord(collection)
	row.Set("uuid", serializers.NewUUID())
	row.Set("issue", issue.Id)
	row.Set("actor", user.Id)
	row.Set("reaction", reaction)
	row.Set("project", scope.Project.Id)
	row.Set("workspace", scope.Project.GetString("workspace"))
	row.Set("created_by", user.Id)
	if err := h.App.Save(row); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusCreated, h.issueReactionJSON(row))
}

// DeleteReaction serves DELETE …/issues/{issue_id}/reactions/{reaction}/
// (IssueReactionViewSet.destroy). The path segment is the reaction code, not a
// row id; the row is identified by (issue, actor=caller, reaction).
func (h *Handler) DeleteReaction(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	row, err := h.findReaction("issue_reactions", "issue", issue.Id, user.Id, e.Request.PathValue("reaction"))
	if err != nil {
		return err
	}
	if row == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	if err := h.App.Delete(row); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// findReaction returns the live reaction row for (parentColumn=parentID, actor,
// reaction) in the given collection.
func (h *Handler) findReaction(collection, parentColumn, parentID, actorID, reaction string) (*core.Record, error) {
	row, err := h.App.FindFirstRecordByFilter(
		collection,
		parentColumn+" = {:parent} && actor = {:actor} && reaction = {:reaction} && deleted_at = ''",
		dbx.Params{"parent": parentID, "actor": actorID, "reaction": reaction},
	)
	if err != nil || row == nil {
		return nil, nil
	}
	return row, nil
}

// issueReactionJSON mirrors IssueReactionSerializer (fields="__all__" plus the
// nested `actor_detail`).
func (h *Handler) issueReactionJSON(r *core.Record) map[string]any {
	return map[string]any{
		"id":           serializers.UUID(r),
		"actor":        serializers.RelationUUID(h.App, r, "actor"),
		"issue":        serializers.RelationUUID(h.App, r, "issue"),
		"reaction":     serializers.Text(r, "reaction"),
		"project":      serializers.RelationUUID(h.App, r, "project"),
		"workspace":    serializers.RelationUUID(h.App, r, "workspace"),
		"created_at":   serializers.DateTime(r, "created_at"),
		"updated_at":   serializers.DateTime(r, "updated_at"),
		"created_by":   serializers.RelationUUID(h.App, r, "created_by"),
		"updated_by":   serializers.RelationUUID(h.App, r, "updated_by"),
		"deleted_at":   serializers.NullableDateTime(r, "deleted_at"),
		"actor_detail": serializers.UserLite(h.App, h.findRecord("users", r.GetString("actor"))),
	}
}

// ---------------------------------------------------------------------------
// comment reactions
// ---------------------------------------------------------------------------

// ListCommentReactions serves GET …/comments/{comment_id}/reactions/.
func (h *Handler) ListCommentReactions(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	comment := h.findCommentByID(scope, e.Request.PathValue("comment_id"))
	if comment == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	rows, err := h.App.FindRecordsByFilter(
		"comment_reactions",
		"comment = {:comment} && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"comment": comment.Id},
	)
	out := make([]map[string]any, 0)
	if err != nil {
		return e.JSON(http.StatusOK, out)
	}
	for _, row := range rows {
		out = append(out, h.commentReactionJSON(row))
	}
	return e.JSON(http.StatusOK, out)
}

// CreateCommentReaction serves POST …/comments/{comment_id}/reactions/
// (CommentReactionViewSet.create).
func (h *Handler) CreateCommentReaction(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	comment := h.findCommentByID(scope, e.Request.PathValue("comment_id"))
	if comment == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	reaction := asString(body["reaction"])
	if reaction == "" {
		return serializers.Validation(e, "reaction", "This field is required.")
	}
	if existing, _ := h.findReaction("comment_reactions", "comment", comment.Id, user.Id, reaction); existing != nil {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Reaction already exists for the user"})
	}

	collection, err := h.App.FindCollectionByNameOrId("comment_reactions")
	if err != nil {
		return err
	}
	row := core.NewRecord(collection)
	row.Set("uuid", serializers.NewUUID())
	row.Set("comment", comment.Id)
	row.Set("actor", user.Id)
	row.Set("reaction", reaction)
	row.Set("project", scope.Project.Id)
	row.Set("workspace", scope.Project.GetString("workspace"))
	row.Set("created_by", user.Id)
	if err := h.App.Save(row); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusCreated, h.commentReactionJSON(row))
}

// DeleteCommentReaction serves DELETE …/comments/{comment_id}/reactions/{reaction}/
// (CommentReactionViewSet.destroy).
func (h *Handler) DeleteCommentReaction(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	comment := h.findCommentByID(scope, e.Request.PathValue("comment_id"))
	if comment == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	row, err := h.findReaction("comment_reactions", "comment", comment.Id, user.Id, e.Request.PathValue("reaction"))
	if err != nil {
		return err
	}
	if row == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	if err := h.App.Delete(row); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// findCommentByID resolves a comment UUID within the resolved project.
func (h *Handler) findCommentByID(scope *middleware.Scope, commentUUID string) *core.Record {
	comment, _ := serializers.FindByUUID(h.App, "issue_comments", commentUUID)
	if comment == nil || comment.GetString("project") != scope.Project.Id {
		return nil
	}
	return comment
}

// ---------------------------------------------------------------------------
// issue relations
// ---------------------------------------------------------------------------

// relationFieldOrder is the projection IssueRelationViewSet.list/create use for
// each related issue.
var relationFieldOrder = []string{
	"id", "name", "state_id", "sort_order", "priority", "sequence_id",
	"project_id", "label_ids", "assignee_ids", "created_at", "updated_at",
	"created_by", "updated_by", "relation_type",
}

// ListRelations serves GET …/issues/{issue_id}/issue-relation/
// (IssueRelationViewSet.list) — a map keyed by relation type.
func (h *Handler) ListRelations(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}

	// One read of the whole relation set touching this issue; the eight buckets
	// below are derived from it exactly like Django's queryset filters.
	rows, _ := h.App.FindRecordsByFilter(
		"issue_relations",
		"deleted_at = '' && (issue = {:issue} || related_issue = {:issue})",
		"-created_at", 0, 0,
		dbx.Params{"issue": issue.Id},
	)

	// Collect the related issue ids per bucket, preserving first-seen order.
	buckets := map[string][]string{
		"blocking": {}, "blocked_by": {}, "duplicate": {}, "relates_to": {},
		"start_after": {}, "start_before": {}, "finish_after": {}, "finish_before": {},
	}
	add := func(bucket, id string) {
		for _, existing := range buckets[bucket] {
			if existing == id {
				return
			}
		}
		buckets[bucket] = append(buckets[bucket], id)
	}
	for _, r := range rows {
		rowIssue := r.GetString("issue")
		rowRelated := r.GetString("related_issue")
		switch r.GetString("relation_type") {
		case "blocked_by":
			if rowRelated == issue.Id {
				add("blocking", rowIssue)
			}
			if rowIssue == issue.Id {
				add("blocked_by", rowRelated)
			}
		case "duplicate":
			if rowIssue == issue.Id {
				add("duplicate", rowRelated)
			}
			if rowRelated == issue.Id {
				add("duplicate", rowIssue)
			}
		case "relates_to":
			if rowIssue == issue.Id {
				add("relates_to", rowRelated)
			}
			if rowRelated == issue.Id {
				add("relates_to", rowIssue)
			}
		case "start_before":
			if rowRelated == issue.Id {
				add("start_after", rowIssue)
			}
			if rowIssue == issue.Id {
				add("start_before", rowRelated)
			}
		case "finish_before":
			if rowRelated == issue.Id {
				add("finish_after", rowIssue)
			}
			if rowIssue == issue.Id {
				add("finish_before", rowRelated)
			}
		}
	}

	response := make(map[string]any, len(buckets))
	for bucket, ids := range buckets {
		items := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			related := h.findRecord("issues", id)
			if related == nil || !h.isIssueObject(related, scope.Project) {
				continue
			}
			items = append(items, h.relationIssueJSON(related, bucket))
		}
		response[bucket] = items
	}
	return e.JSON(http.StatusOK, response)
}

// CreateRelations serves POST …/issues/{issue_id}/issue-relation/
// (IssueRelationViewSet.create). Body: `{relation_type, issues:[uuid,...]}`.
//
// IssueRelationViewSet is guarded by ProjectEntityPermission, whose non-safe
// branch is a plain `ProjectMember.role__in=[ADMIN, MEMBER]` lookup — so unlike
// the @allow_permission rows there is **no** workspace-Admin bypass here.
func (h *Handler) CreateRelations(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	relationType := asString(body["relation_type"])
	if relationType == "" {
		return e.JSON(http.StatusBadRequest, map[string]any{"message": "Issue relation type is required"})
	}
	actualType := getActualRelation(relationType)
	// For these three the created row stores the related issue in `issue` and
	// the current issue in `related_issue` (Django's swap).
	swap := relationType == "blocking" || relationType == "start_after" || relationType == "finish_after"

	collection, err := h.App.FindCollectionByNameOrId("issue_relations")
	if err != nil {
		return err
	}
	created := make([]map[string]any, 0)
	for _, relatedUUID := range uuidList(body, "issues") {
		related := h.projectIssue(scope, relatedUUID)
		if related == nil || !h.isIssueObject(related, scope.Project) {
			continue
		}
		left, right := issue.Id, related.Id
		if swap {
			left, right = related.Id, issue.Id
		}
		// `bulk_create(ignore_conflicts=True)`: skip an existing pair.
		if existing, _ := h.App.FindFirstRecordByFilter(
			"issue_relations",
			"issue = {:issue} && related_issue = {:related} && deleted_at = ''",
			dbx.Params{"issue": left, "related": right},
		); existing != nil {
			continue
		}
		row := core.NewRecord(collection)
		row.Set("uuid", serializers.NewUUID())
		row.Set("issue", left)
		row.Set("related_issue", right)
		row.Set("relation_type", actualType)
		row.Set("project", scope.Project.Id)
		row.Set("workspace", scope.Project.GetString("workspace"))
		row.Set("created_by", user.Id)
		row.Set("updated_by", user.Id)
		if err := h.App.Save(row); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		created = append(created, h.relationIssueJSON(related, actualType))
	}
	return e.JSON(http.StatusCreated, created)
}

// DeleteRelation serves DELETE …/issues/{issue_id}/issue-relation/{related}/
// (client-only route — no Django counterpart). `{related}` is the related
// issue's UUID, which is what IssueRelationSerializer exposes as `id`.
//
// No Django route exists for this method, so the guard mirrors the sibling
// create/remove_relation paths: ProjectEntityPermission's non-safe branch.
func (h *Handler) DeleteRelation(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	related := h.projectIssue(scope, e.Request.PathValue("related_id"))
	if related == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	rows, _ := h.App.FindRecordsByFilter(
		"issue_relations",
		"deleted_at = '' && ((issue = {:a} && related_issue = {:b}) || (issue = {:b} && related_issue = {:a}))",
		"", 0, 0,
		dbx.Params{"a": issue.Id, "b": related.Id},
	)
	for _, row := range rows {
		if err := h.App.Delete(row); err != nil {
			return err
		}
	}
	return e.NoContent(http.StatusNoContent)
}

// RemoveRelation serves POST …/issues/{issue_id}/remove-relation/
// (IssueRelationViewSet.remove_relation). Body: `{related_issue}`.
// Same ProjectEntityPermission non-safe branch as create — no ws-Admin bypass.
func (h *Handler) RemoveRelation(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	related := h.projectIssue(scope, asString(body["related_issue"]))
	if related == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}

	// Django's filter is workspace-scoped only (relations may cross projects),
	// and deletes just the first match.
	row, err := h.App.FindFirstRecordByFilter(
		"issue_relations",
		"deleted_at = '' && ((issue = {:a} && related_issue = {:b}) || (issue = {:b} && related_issue = {:a}))",
		dbx.Params{"a": issue.Id, "b": related.Id},
	)
	if err != nil || row == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	if err := h.App.Delete(row); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// relationIssueJSON mirrors the `fields` projection IssueRelationViewSet uses
// for a related issue, with the bucket's relation_type attached.
func (h *Handler) relationIssueJSON(issue *core.Record, relationType string) map[string]any {
	priority := issue.GetString("priority")
	if priority == "" {
		priority = "none"
	}
	return map[string]any{
		"id":            serializers.UUID(issue),
		"name":          serializers.Text(issue, "name"),
		"state_id":      serializers.RelationUUID(h.App, issue, "state"),
		"sort_order":    issue.GetFloat("sort_order"),
		"priority":      priority,
		"sequence_id":   issue.GetInt("sequence_id"),
		"project_id":    serializers.RelationUUID(h.App, issue, "project"),
		"label_ids":     issueLabelIDsLocal(h.App, issue),
		"assignee_ids":  issueAssigneeIDsLocal(h.App, issue),
		"created_at":    serializers.DateTime(issue, "created_at"),
		"updated_at":    serializers.DateTime(issue, "updated_at"),
		"created_by":    serializers.RelationUUID(h.App, issue, "created_by"),
		"updated_by":    serializers.RelationUUID(h.App, issue, "updated_by"),
		"relation_type": relationType,
	}
}

// getActualRelation ports plane.utils.issue_relation_mapper.get_actual_relation.
func getActualRelation(relationType string) string {
	switch relationType {
	case "start_after", "finish_after", "blocking":
		return map[string]string{"start_after": "start_before", "finish_after": "finish_before", "blocking": "blocked_by"}[relationType]
	case "implements":
		return "implemented_by"
	default:
		return relationType
	}
}

// ---------------------------------------------------------------------------
// issue-dates
// ---------------------------------------------------------------------------

// BulkUpdateDates serves POST …/issue-dates/ (IssueBulkUpdateDateEndpoint.post).
//
// NOTE: the Wave-2 brief listed this as GET, but both the bundled Django view
// and web/services/issue/issue.service.ts:247 use POST with a `{updates}`
// body — the brief is corrected here.
func (h *Handler) BulkUpdateDates(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	updates, _ := body["updates"].([]any)
	for _, raw := range updates {
		update, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		issue := h.projectIssue(scope, asString(update["id"]))
		if issue == nil {
			continue
		}
		start := asString(update["start_date"])
		target := asString(update["target_date"])
		effectiveStart := start
		if effectiveStart == "" {
			effectiveStart = dateOnlyString(issue, "start_date")
		}
		effectiveTarget := target
		if effectiveTarget == "" {
			effectiveTarget = dateOnlyString(issue, "target_date")
		}
		if effectiveStart != "" && effectiveTarget != "" && effectiveStart > effectiveTarget {
			return e.JSON(http.StatusBadRequest, map[string]any{"message": "Start date cannot exceed target date"})
		}
		if start != "" {
			issue.Set("start_date", start)
		}
		if target != "" {
			issue.Set("target_date", target)
		}
		if err := h.App.Save(issue); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
	}
	return e.JSON(http.StatusOK, map[string]any{"message": "Issues updated successfully"})
}

// dateOnlyString renders an issue date column as "YYYY-MM-DD", or "" when unset.
func dateOnlyString(r *core.Record, field string) string {
	if v, ok := serializers.DateOnly(r, field).(string); ok {
		return v
	}
	return ""
}

// ---------------------------------------------------------------------------
// project user properties (issue-display-properties / epics-user-properties)
// ---------------------------------------------------------------------------

// DisplayProperties serves GET …/issue-display-properties/.
//
// NOTE: no Django view exists for this path; the web client calls it
// (issue.service.ts:205) with the same get-or-create semantics as
// ProjectUserDisplayPropertyEndpoint.get. The body is a
// ProjectUserPropertySerializer projection (fields="__all__").
func (h *Handler) DisplayProperties(e *core.RequestEvent) error {
	return h.projectUserPropertyGet(e)
}

// UpdateDisplayProperties serves POST/PATCH …/issue-display-properties/.
//
// The web client POSTs `{properties: <IIssueDisplayProperties>}`
// (issue.service.ts:217) while a PATCH mirrors ProjectUserDisplayPropertyEndpoint
// and carries the ProjectUserProperty fields directly. Both are accepted.
func (h *Handler) UpdateDisplayProperties(e *core.RequestEvent) error {
	return h.projectUserPropertyUpdate(e)
}

// EpicUserProperties serves GET …/epics-user-properties/.
//
// NOTE: no Django view exists for this path; it is the epic-filter counterpart
// of the project user-properties endpoint (issue_filter.service.ts:39).
func (h *Handler) EpicUserProperties(e *core.RequestEvent) error {
	return h.projectUserPropertyGet(e)
}

// UpdateEpicUserProperties serves PATCH …/epics-user-properties/
// (issue_filter.service.ts:50).
func (h *Handler) UpdateEpicUserProperties(e *core.RequestEvent) error {
	return h.projectUserPropertyUpdate(e)
}

// projectUserPropertyGet is ProjectUserDisplayPropertyEndpoint.get: it
// get-or-creates the caller's ProjectUserProperty row for the project.
func (h *Handler) projectUserPropertyGet(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	property, err := h.getOrCreateProjectUserProperty(scope, user)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, h.projectUserPropertyJSON(property))
}

// projectUserPropertyUpdate is ProjectUserDisplayPropertyEndpoint.patch.
func (h *Handler) projectUserPropertyUpdate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	property, err := h.getOrCreateProjectUserProperty(scope, user)
	if err != nil {
		return err
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	// The issue-display-properties client wraps the payload in `properties`.
	if wrapped, ok := body["properties"]; ok {
		property.Set("display_properties", wrapped)
	}
	for _, field := range []string{"filters", "display_filters", "display_properties", "rich_filters", "preferences", "sort_order"} {
		if v, ok := body[field]; ok {
			property.Set(field, v)
		}
	}
	property.Set("updated_by", user.Id)
	if err := h.App.Save(property); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, h.projectUserPropertyJSON(property))
}

// getOrCreateProjectUserProperty returns the caller's row, creating it with
// Django's model defaults when absent.
func (h *Handler) getOrCreateProjectUserProperty(scope *middleware.Scope, user *core.Record) (*core.Record, error) {
	existing, err := h.App.FindFirstRecordByFilter(
		"project_user_properties",
		"user = {:user} && project = {:project} && deleted_at = ''",
		dbx.Params{"user": user.Id, "project": scope.Project.Id},
	)
	if err == nil && existing != nil {
		return existing, nil
	}

	collection, err := h.App.FindCollectionByNameOrId("project_user_properties")
	if err != nil {
		return nil, err
	}
	property := core.NewRecord(collection)
	property.Set("uuid", serializers.NewUUID())
	property.Set("user", user.Id)
	property.Set("project", scope.Project.Id)
	property.Set("workspace", scope.Project.GetString("workspace"))
	property.Set("created_by", user.Id)
	property.Set("filters", defaultIssueFilters())
	property.Set("display_filters", defaultDisplayFilters())
	property.Set("display_properties", defaultDisplayProperties())
	property.Set("rich_filters", map[string]any{})
	property.Set("preferences", defaultPreferences())
	property.Set("sort_order", float64(65535))
	if err := h.App.Save(property); err != nil {
		return nil, err
	}
	return property, nil
}

// projectUserPropertyJSON mirrors ProjectUserPropertySerializer
// (fields="__all__", read-only user/workspace/project).
func (h *Handler) projectUserPropertyJSON(r *core.Record) map[string]any {
	return map[string]any{
		"id":                 serializers.UUID(r),
		"user":               serializers.RelationUUID(h.App, r, "user"),
		"project":            serializers.RelationUUID(h.App, r, "project"),
		"workspace":          serializers.RelationUUID(h.App, r, "workspace"),
		"filters":            serializers.JSONOrDefault(r, "filters", map[string]any{}),
		"display_filters":    serializers.JSONOrDefault(r, "display_filters", map[string]any{}),
		"display_properties": serializers.JSONOrDefault(r, "display_properties", map[string]any{}),
		"rich_filters":       serializers.JSONOrDefault(r, "rich_filters", map[string]any{}),
		"preferences":        serializers.JSONOrDefault(r, "preferences", map[string]any{}),
		"sort_order":         r.GetFloat("sort_order"),
		"created_at":         serializers.DateTime(r, "created_at"),
		"updated_at":         serializers.DateTime(r, "updated_at"),
		"created_by":         serializers.RelationUUID(h.App, r, "created_by"),
		"updated_by":         serializers.RelationUUID(h.App, r, "updated_by"),
		"deleted_at":         serializers.NullableDateTime(r, "deleted_at"),
	}
}

// Django's ProjectUserProperty field defaults (db/models/issue.py + project.py).
func defaultIssueFilters() map[string]any {
	return map[string]any{
		"priority": nil, "state": nil, "state_group": nil, "assignees": nil,
		"created_by": nil, "labels": nil, "start_date": nil, "target_date": nil,
		"subscriber": nil,
	}
}

func defaultDisplayFilters() map[string]any {
	return map[string]any{
		"group_by": nil, "order_by": "-created_at", "type": nil, "sub_issue": true,
		"show_empty_groups": true, "layout": "list", "calendar_date_range": "",
	}
}

func defaultDisplayProperties() map[string]any {
	return map[string]any{
		"assignee": true, "attachment_count": true, "created_on": true, "due_date": true,
		"estimate": true, "key": true, "labels": true, "link": true, "priority": true,
		"start_date": true, "state": true, "sub_issue_count": true, "updated_on": true,
	}
}

func defaultPreferences() map[string]any {
	return map[string]any{
		"pages":      map[string]any{"block_display": true},
		"navigation": map[string]any{"default_tab": "work_items", "hide_in_more_menu": []any{}},
	}
}

// ---------------------------------------------------------------------------
// description versions
// ---------------------------------------------------------------------------

// versionEnvelope is the plane.utils.global_paginator.paginate body — distinct
// from serializers.Envelope (no grouped_by/count/extra_stats, and a `cursor`
// key instead of `next_cursor` being the only forward pointer).
type versionEnvelope struct {
	PrevCursor      string `json:"prev_cursor"`
	Cursor          string `json:"cursor"`
	NextCursor      any    `json:"next_cursor"`
	PrevPageResults bool   `json:"prev_page_results"`
	NextPageResults bool   `json:"next_page_results"`
	PageCount       int    `json:"page_count"`
	TotalResults    int    `json:"total_results"`
	TotalPages      int    `json:"total_pages"`
	Results         []any  `json:"results"`
}

// DescriptionVersions serves GET …/{serviceType}/{work_item_id}/description-versions/
// (WorkItemDescriptionVersionEndpoint.get, no pk).
//
// NOTE: Django registers this under the literal `work-items/` prefix, not
// `{serviceType}`; both `work-items/` and `issues/` are registered so the
// brief's "issues variant" and the real client both resolve.
func (h *Handler) DescriptionVersions(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	issue := h.projectIssue(scope, pathUUID(e, "work_item_id", "issue_id"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	if scope.ProjectRole == middleware.RoleGuest && !scope.Project.GetBool("guest_view_all_features") && !middleware.IsCreator(e, issue) {
		return e.JSON(http.StatusForbidden, map[string]any{"error": "You are not allowed to view this issue"})
	}

	rows, err := h.App.FindRecordsByFilter(
		"issue_description_versions",
		"issue = {:issue} && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"issue": issue.Id},
	)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, h.paginateVersions(rows, e.Request.URL.Query().Get("cursor")))
}

// DescriptionVersionDetail serves GET …/{serviceType}/{work_item_id}/description-versions/{pk}/
// (WorkItemDescriptionVersionEndpoint.get with pk).
func (h *Handler) DescriptionVersionDetail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	issue := h.projectIssue(scope, pathUUID(e, "work_item_id", "issue_id"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	version, _ := serializers.FindByUUID(h.App, "issue_description_versions", e.Request.PathValue("pk"))
	if version == nil || version.GetString("issue") != issue.Id || version.GetString("project") != scope.Project.Id {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	return e.JSON(http.StatusOK, h.descriptionVersionDetail(version))
}

// descriptionVersionSummary is the row shape inside the list envelope
// (IssueDescriptionVersion `required_fields`).
func (h *Handler) descriptionVersionSummary(r *core.Record) map[string]any {
	return map[string]any{
		"id":            serializers.UUID(r),
		"workspace":     serializers.RelationUUID(h.App, r, "workspace"),
		"project":       serializers.RelationUUID(h.App, r, "project"),
		"issue":         serializers.RelationUUID(h.App, r, "issue"),
		"last_saved_at": serializers.DateTime(r, "last_saved_at"),
		"owned_by":      serializers.RelationUUID(h.App, r, "owned_by"),
		"created_at":    serializers.DateTime(r, "created_at"),
		"updated_at":    serializers.DateTime(r, "updated_at"),
		"created_by":    serializers.RelationUUID(h.App, r, "created_by"),
		"updated_by":    serializers.RelationUUID(h.App, r, "updated_by"),
	}
}

// descriptionVersionDetail mirrors IssueDescriptionVersionDetailSerializer.
func (h *Handler) descriptionVersionDetail(r *core.Record) map[string]any {
	return map[string]any{
		"id":                   serializers.UUID(r),
		"workspace":            serializers.RelationUUID(h.App, r, "workspace"),
		"project":              serializers.RelationUUID(h.App, r, "project"),
		"issue":                serializers.RelationUUID(h.App, r, "issue"),
		"description_binary":   nil,
		"description_html":     serializers.Text(r, "description_html"),
		"description_stripped": serializers.NullableText(r, "description_stripped"),
		"description_json":     serializers.JSONOrDefault(r, "description_json", map[string]any{}),
		"last_saved_at":        serializers.DateTime(r, "last_saved_at"),
		"owned_by":             serializers.RelationUUID(h.App, r, "owned_by"),
		"created_at":           serializers.DateTime(r, "created_at"),
		"updated_at":           serializers.DateTime(r, "updated_at"),
		"created_by":           serializers.RelationUUID(h.App, r, "created_by"),
		"updated_by":           serializers.RelationUUID(h.App, r, "updated_by"),
	}
}

// paginateVersions ports plane.utils.global_paginator.paginate over an
// already-materialised slice.
func (h *Handler) paginateVersions(rows []*core.Record, cursor string) versionEnvelope {
	pageSize := 1000
	page := 0
	if cursor != "" {
		bits := strings.Split(cursor, ":")
		if len(bits) == 3 {
			if v, err := strconv.Atoi(bits[0]); err == nil && v > 0 {
				pageSize = v
			}
			if p, err := strconv.Atoi(bits[1]); err == nil {
				page = p
			}
		}
	}
	if pageSize > 1000 {
		pageSize = 1000
	}
	total := len(rows)
	totalPages := 0
	if pageSize > 0 {
		totalPages = int(math.Ceil(float64(total) / float64(pageSize)))
	}
	start := 0
	if page > 0 {
		start = page * pageSize
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	results := make([]any, 0)
	if start < total && start >= 0 {
		for _, row := range rows[start:end] {
			results = append(results, h.descriptionVersionSummary(row))
		}
	}

	var nextCursor any
	if end < total {
		nextCursor = fmt.Sprintf("%d:%d:0", pageSize, page+1)
	}
	return versionEnvelope{
		PrevCursor:      fmt.Sprintf("%d:%d:0", pageSize, page-1),
		Cursor:          fmt.Sprintf("%d:%d:0", pageSize, page),
		NextCursor:      nextCursor,
		PrevPageResults: page > 0,
		NextPageResults: end < total,
		PageCount:       len(results),
		TotalResults:    total,
		TotalPages:      totalPages,
		Results:         results,
	}
}

// ---------------------------------------------------------------------------
// local label/assignee resolution for relation projections
// ---------------------------------------------------------------------------

// issueLabelIDsLocal mirrors serializers.issueLabelIDs, which is unexported.
func issueLabelIDsLocal(app core.App, issue *core.Record) []string {
	rows, err := app.FindRecordsByFilter(
		"issue_labels",
		"issue = {:issue} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"issue": issue.Id},
	)
	out := make([]string, 0)
	if err != nil {
		return out
	}
	seen := map[string]struct{}{}
	for _, row := range rows {
		label, _ := app.FindRecordById("labels", row.GetString("label"))
		if label == nil {
			continue
		}
		id := serializers.UUID(label)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// issueAssigneeIDsLocal mirrors serializers.issueAssigneeIDs, which is
// unexported.
func issueAssigneeIDsLocal(app core.App, issue *core.Record) []string {
	rows, err := app.FindRecordsByFilter(
		"issue_assignees",
		"issue = {:issue} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"issue": issue.Id},
	)
	out := make([]string, 0)
	if err != nil {
		return out
	}
	seen := map[string]struct{}{}
	for _, row := range rows {
		user, _ := app.FindRecordById("users", row.GetString("assignee"))
		if user == nil {
			continue
		}
		id := serializers.UUID(user)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
