// Wave-1 endpoints for the issue domain. This file is deliberately separate
// from issues.go so the core CRUD path stays readable; the two share the
// package, the Handler type and its helpers.
//
//	GET    /api/workspaces/{slug}/user-issues/{user_id}/   (WorkspaceUserProfileIssuesEndpoint)
//	POST   /api/workspaces/{slug}/projects/{project_id}/bulk-archive-issues/
//	DELETE /api/workspaces/{slug}/projects/{project_id}/bulk-delete-issues/
//	POST   /api/workspaces/{slug}/projects/{project_id}/bulk-operation-issues/
//	POST   /api/workspaces/{slug}/projects/{project_id}/bulk-subscribe-issues/
//	GET    /api/workspaces/{slug}/projects/{project_id}/deleted-issues/
//	GET    /api/workspaces/{slug}/projects/{project_id}/archived-issues/
//	GET    /api/workspaces/{slug}/projects/{project_id}/issues/{pk}/meta/
//
// Contract: apps/api/plane/app/urls/{workspace,issue}.py and the corresponding
// views under apps/api/plane/app/views/. The bulk-operation and bulk-subscribe
// routes have no Django counterpart in the bundled api snapshot (they exist
// only in the web client), so their response shapes are inferred from the
// frontend service and are flagged in the code below.
package issues

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// UserIssues serves GET /api/workspaces/{slug}/user-issues/{user_id}/
// (WorkspaceUserProfileIssuesEndpoint.get) — the work-item list behind the
// profile page's Assigned / Created / Subscribed tabs.
//
// Permission: WorkspaceViewerPermission — any active workspace member.
// ResolveWorkspace has already rejected non-members with a 404, so reaching
// this handler implies the caller is a viewer.
//
// With no `group_by` Django returns the same flat cursor envelope as
// IssueViewSet.list, so the envelope and Issue projection below mirror List in
// issues.go exactly.
func (h *Handler) UserIssues(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.NotFound(e, "")
	}

	perPage, cursor := serializers.ListParams(e, serializers.MaxPerPage)

	// Django resolves `{user_id}` with `<uuid:user_id>` but never asserts the
	// target is a member: an unknown user simply matches nothing. Mirror that
	// by returning an empty page rather than a 404.
	target, _ := serializers.FindByUUID(h.App, "users", e.Request.PathValue("user_id"))
	projectIDs := h.visibleProjectIDs(scope, user)
	if target == nil || len(projectIDs) == 0 {
		return e.JSON(http.StatusOK, emptyIssueEnvelope(perPage))
	}

	filter, params := h.userIssuesFilter(e.Request.URL.Query(), target, projectIDs)
	envelope, err := serializers.Paginate(h.App, serializers.ListQuery{
		Collection: "issues",
		Filter:     filter,
		Params:     params,
		Sort:       orderBy(e.Request.URL.Query().Get("order_by")),
		PerPage:    perPage,
		Cursor:     cursor,
	}, func(issue *core.Record) any {
		return serializers.Issue(h.App, issue)
	})
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, envelope)
}

// userIssuesFilter reproduces the WorkspaceUserProfileIssuesEndpoint queryset:
//
//	Issue.issue_objects.filter(
//	    id__in=Issue.issue_objects.filter(
//	        Q(assignees__in=[user_id]) | Q(created_by_id=user_id)
//	        | Q(issue_subscribers__subscriber_id=user_id),
//	        workspace__slug=slug,
//	    ),
//	    workspace__slug=slug,
//	    project__project_projectmember__member=request.user,
//	    project__project_projectmember__is_active=True,
//	)
//
// The `id__in` sub-select is the "target user is assignee OR creator OR
// subscriber" restriction; it is expressed here with PocketBase's back-relation
// syntax (`issue_assignees_via_issue.assignee` joins through issue_assignees).
// The project membership restriction is precomputed into an explicit id list
// (`visibleProjectIDs`) because PocketBase cannot traverse project -> members
// from the issues collection.
func (h *Handler) userIssuesFilter(query url.Values, target *core.Record, projectIDs []string) (string, dbx.Params) {
	params := dbx.Params{"target": target.Id}

	clauses := []string{
		"deleted_at = ''",
		"archived_at = ''",
		"is_draft = false",
		`state.group != "triage"`,
		"project.archived_at = ''",
		"(created_by = {:target} || issue_assignees_via_issue.assignee = {:target} || issue_subscribers_via_issue.subscriber = {:target})",
	}

	projectClauses := make([]string, 0, len(projectIDs))
	for i, id := range projectIDs {
		key := "proj" + strconv.Itoa(i)
		projectClauses = append(projectClauses, "project = {:"+key+"}")
		params[key] = id
	}
	clauses = append(clauses, "("+strings.Join(projectClauses, " || ")+")")

	// filter_sub_issue_toggle: the profile list shows top-level work items by
	// default (`sub_issue` unset or "false" -> parent__isnull=True).
	if sub := query.Get("sub_issue"); sub == "" || sub == "false" {
		clauses = append(clauses, "parent = ''")
	}

	if clause := h.userIssuesFilters(query, params); clause != "" {
		clauses = append(clauses, clause)
	}

	return strings.Join(clauses, " && "), params
}

// userIssuesFilters ports the subset of plane.utils.issue_filters that the
// profile tabs and their rich filters send. Each recognised key is ANDed onto
// the base query; unknown keys are ignored (Django's filter dict simply would
// not carry them either).
func (h *Handler) userIssuesFilters(query url.Values, params dbx.Params) string {
	var groups []string

	// assignees=<uuid>,<uuid> (live issue_assignees rows only).
	if c := h.uuidColumnClause(query.Get("assignees"), "issue_assignees_via_issue.assignee", "users", "asn", params); c != "" {
		groups = append(groups, c, "issue_assignees_via_issue.deleted_at = ''")
	}
	// created_by=<uuid>,<uuid> — direct relation on issues.
	if c := h.uuidColumnClause(query.Get("created_by"), "created_by", "users", "cb", params); c != "" {
		groups = append(groups, c)
	}
	// subscriber=<uuid>,<uuid> (live issue_subscribers rows only).
	if c := h.uuidColumnClause(query.Get("subscriber"), "issue_subscribers_via_issue.subscriber", "users", "sub", params); c != "" {
		groups = append(groups, c, "issue_subscribers_via_issue.deleted_at = ''")
	}
	// state=<uuid>,<uuid>.
	if c := h.uuidColumnClause(query.Get("state"), "state", "states", "st", params); c != "" {
		groups = append(groups, c)
	}
	// state_group / priority are enumerations, matched literally.
	if c := enumColumnClause(query.Get("state_group"), "state.group", "sg", params); c != "" {
		groups = append(groups, c)
	}
	if c := enumColumnClause(query.Get("priority"), "priority", "pr", params); c != "" {
		groups = append(groups, c)
	}

	if len(groups) == 0 {
		return ""
	}
	return "(" + strings.Join(groups, " && ") + ")"
}

// uuidColumnClause turns a comma-separated list of Plane UUIDs into a
// `column = {:k} || column = {:k} ...` disjunction, resolving each UUID to the
// PocketBase id the relation column actually stores. "None" adds an
// `column = ”` leg (Plane's "unassigned/unlabelled" sentinel); "null" and
// empty entries are dropped, matching filter_valid_uuids.
func (h *Handler) uuidColumnClause(raw, column, collection, prefix string, params dbx.Params) string {
	if raw == "" {
		return ""
	}
	clauses := make([]string, 0)
	empty := false
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" || item == "null" {
			continue
		}
		if item == "None" {
			empty = true
			continue
		}
		record, _ := serializers.FindByUUID(h.App, collection, item)
		if record == nil {
			continue
		}
		key := prefix + strconv.Itoa(len(clauses))
		clauses = append(clauses, column+" = {:"+key+"}")
		params[key] = record.Id
	}
	if empty {
		clauses = append(clauses, column+" = ''")
	}
	if len(clauses) == 0 {
		return ""
	}
	return "(" + strings.Join(clauses, " || ") + ")"
}

// enumColumnClause turns a comma-separated list of literal values (priority,
// state group) into a disjunction.
func enumColumnClause(raw, column, prefix string, params dbx.Params) string {
	if raw == "" {
		return ""
	}
	clauses := make([]string, 0)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" || item == "null" {
			continue
		}
		key := prefix + strconv.Itoa(len(clauses))
		clauses = append(clauses, column+" = {:"+key+"}")
		params[key] = item
	}
	if len(clauses) == 0 {
		return ""
	}
	return "(" + strings.Join(clauses, " || ") + ")"
}

// visibleProjectIDs returns the PocketBase ids of the workspace's projects the
// requesting user is an active member of and that are not deleted. This is the
// explicit form of Django's `project__project_projectmember__member=request.user,
// project__project_projectmember__is_active=True`.
func (h *Handler) visibleProjectIDs(scope *middleware.Scope, user *core.Record) []string {
	members, err := h.App.FindRecordsByFilter(
		"project_members",
		"member = {:member} && workspace = {:workspace} && is_active = true && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"member": user.Id, "workspace": scope.Workspace.Id},
	)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(members))
	seen := make(map[string]struct{}, len(members))
	for _, m := range members {
		id := m.GetString("project")
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		project, _ := h.App.FindRecordById("projects", id)
		if project == nil || project.GetString("deleted_at") != "" {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

// emptyIssueEnvelope is the cursor envelope Plane expects for an empty page.
// Its cursors match what serializers.Paginate emits for page 0, so the
// frontend's "load more" logic behaves identically to a real empty result.
func emptyIssueEnvelope(perPage int) *serializers.Envelope {
	return &serializers.Envelope{
		NextCursor: serializers.Cursor{Value: float64(perPage), Offset: 1}.String(),
		PrevCursor: serializers.Cursor{Value: float64(perPage), Offset: -1, IsPrev: true}.String(),
		Results:    []any{},
	}
}

// BulkArchive serves POST /api/workspaces/{slug}/projects/{project_id}/bulk-archive-issues/
// (BulkArchiveIssuesEndpoint.post).
//
// Every issue must be in the completed or cancelled state group; otherwise the
// whole request is rejected with Plane's INVALID_ARCHIVE_STATE_GROUP error code
// (4091). Unlike the list endpoints this uses `Issue.objects`, not
// `Issue.issue_objects`, so already-archived issues are still candidates.
func (h *Handler) BulkArchive(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	ids := uuidList(body, "issue_ids")
	if len(ids) == 0 {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Issue IDs are required"})
	}

	issues := h.projectIssues(scope, ids, false)
	for _, issue := range issues {
		if group := h.stateGroup(issue); group != "completed" && group != "cancelled" {
			return e.JSON(http.StatusBadRequest, map[string]any{
				"error_code":    4091,
				"error_message": "INVALID_ARCHIVE_STATE_GROUP",
			})
		}
	}

	now := serializers.Now()
	for _, issue := range issues {
		issue.Set("archived_at", now)
		if err := h.App.Save(issue); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
	}
	return e.JSON(http.StatusOK, map[string]any{"archived_at": now.UTC().Format(serializers.DRFDateOnlyFormat)})
}

// BulkDelete serves DELETE /api/workspaces/{slug}/projects/{project_id}/bulk-delete-issues/
// (BulkDeleteIssuesEndpoint.delete). The body carries `issue_ids`.
//
// Permission: project ADMIN only. Django soft-deletes the related cycle_issues
// and module_issues rows first, then the issues themselves, and reports how
// many issues were removed.
func (h *Handler) BulkDelete(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleAdmin) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	ids := uuidList(body, "issue_ids")
	if len(ids) == 0 {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Issue IDs are required"})
	}

	// `Issue.issue_objects` — excludes soft-deleted, archived, draft, triage
	// and issues under archived projects.
	issues := h.projectIssues(scope, ids, true)
	for _, issue := range issues {
		h.softDeleteRelated("cycle_issues", issue.Id)
		h.softDeleteRelated("module_issues", issue.Id)
	}
	now := serializers.Now()
	for _, issue := range issues {
		issue.Set("deleted_at", now)
		if err := h.App.Save(issue); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
	}
	return e.JSON(http.StatusOK, map[string]any{
		"message": fmt.Sprintf("%d issues were deleted", len(issues)),
	})
}

// BulkOperation serves POST /api/workspaces/{slug}/projects/{project_id}/bulk-operation-issues/
// with a `{issue_ids, properties}` body.
//
// NOTE: this route has no counterpart in the bundled Django source (it exists
// only in the web client's issue.service.ts:339). The property names are taken
// from the TBulkOperationsPayload type and the frontend store, which merges
// array properties (labels/assignees) into the existing values rather than
// replacing them — so the server side is additive for those two keys.
func (h *Handler) BulkOperation(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	user := middleware.CurrentUser(e)

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	ids := uuidList(body, "issue_ids")
	properties, _ := body["properties"].(map[string]any)
	if len(ids) == 0 || len(properties) == 0 {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Issue IDs and properties are required"})
	}

	issues := h.projectIssues(scope, ids, true)
	for _, issue := range issues {
		if err := h.applyBulkProperties(scope, user, issue, properties); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
	}
	return e.JSON(http.StatusOK, map[string]any{"message": "Issues updated successfully"})
}

// applyBulkProperties writes one issue's bulk-update properties. Scalar fields
// are set directly; `assignee_ids`/`label_ids` are added (not replaced) to
// match the frontend store's merge behaviour. `cycle_id` and `module_ids`
// target collections outside this slice and are ignored.
func (h *Handler) applyBulkProperties(scope *middleware.Scope, user, issue *core.Record, properties map[string]any) error {
	if stateUUID := asString(properties["state_id"]); stateUUID != "" {
		state, _ := serializers.FindByUUID(h.App, "states", stateUUID)
		if state == nil || state.GetString("project") != scope.Project.Id {
			return errInvalidState
		}
		issue.Set("state", state.Id)
	}
	if raw, ok := properties["estimate_point"]; ok {
		// Same project-scoping rule as the single-issue PATCH: a point from
		// another project is rejected, not silently dropped.
		estimatePointID, err := resolveEstimatePoint(h.App, scope.Project.Id, raw)
		if err != nil {
			return err
		}
		issue.Set("estimate_point", estimatePointID)
	}
	for _, field := range []string{"priority", "start_date", "target_date"} {
		if v, ok := properties[field]; ok {
			issue.Set(field, v)
		}
	}
	if user != nil {
		issue.Set("updated_by", user.Id)
	}
	if err := h.App.Save(issue); err != nil {
		return err
	}

	if hasAnyKey(properties, "assignee_ids", "assignees") {
		if err := h.addAssignees(scope.Project, issue, uuidList(properties, "assignee_ids", "assignees"), user); err != nil {
			return err
		}
	}
	if hasAnyKey(properties, "label_ids", "labels") {
		if err := h.addLabels(scope.Project, issue, uuidList(properties, "label_ids", "labels"), user); err != nil {
			return err
		}
	}
	return nil
}

// BulkSubscribe serves POST /api/workspaces/{slug}/projects/{project_id}/bulk-subscribe-issues/
// with a `{issue_ids}` body: it subscribes the caller to every named issue.
//
// NOTE: like BulkOperation this route has no counterpart in the bundled Django
// source (only web/services/issue/issue.service.ts:410 calls it) and its
// response body is not consumed by the client, so the shape below is inferred.
func (h *Handler) BulkSubscribe(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	ids := uuidList(body, "issue_ids")
	if len(ids) == 0 {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Issue IDs are required"})
	}

	collection, err := h.App.FindCollectionByNameOrId("issue_subscribers")
	if err != nil {
		return err
	}
	subscribed := make([]string, 0, len(ids))
	for _, issue := range h.projectIssues(scope, ids, true) {
		existing, _ := h.App.FindFirstRecordByFilter(
			"issue_subscribers",
			"issue = {:issue} && subscriber = {:subscriber} && deleted_at = ''",
			dbx.Params{"issue": issue.Id, "subscriber": user.Id},
		)
		if existing != nil {
			continue
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
		subscribed = append(subscribed, serializers.UUID(issue))
	}
	return e.JSON(http.StatusOK, map[string]any{
		"message":   "Subscribed to issues successfully",
		"issue_ids": subscribed,
	})
}

// DeletedIssues serves GET /api/workspaces/{slug}/projects/{project_id}/deleted-issues/
// (DeletedIssuesListViewSet.get) — a plain array of issue UUIDs that are either
// archived or soft-deleted. It reads `Issue.all_objects`, so soft-deleted rows
// are included on purpose.
func (h *Handler) DeletedIssues(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}

	filter := "project = {:project} && (archived_at != '' || deleted_at != '')"
	params := dbx.Params{"project": scope.Project.Id}
	if updatedAt := e.Request.URL.Query().Get("updated_at__gt"); updatedAt != "" {
		filter += " && updated_at > {:updated}"
		params["updated"] = updatedAt
	}

	records, err := h.App.FindRecordsByFilter("issues", filter, "", 0, 0, params)
	if err != nil {
		return err
	}
	out := make([]string, 0, len(records))
	for _, r := range records {
		out = append(out, serializers.UUID(r))
	}
	return e.JSON(http.StatusOK, out)
}

// ArchivedIssues serves GET /api/workspaces/{slug}/projects/{project_id}/archived-issues/
// (IssueArchiveViewSet.list).
//
// The queryset is `Issue.objects` (soft-deleted rows excluded, archived rows
// required); the Django `type` filter is a no-op here because the `type` column
// is outside this slice. `show_sub_issues` defaults to "true".
func (h *Handler) ArchivedIssues(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}

	filter := "project = {:project} && deleted_at = '' && archived_at != ''"
	params := dbx.Params{"project": scope.Project.Id}
	if show := e.Request.URL.Query().Get("show_sub_issues"); show != "" && show != "true" {
		filter += " && parent = ''"
	}

	perPage, cursor := serializers.ListParams(e, serializers.MaxPerPage)
	envelope, err := serializers.Paginate(h.App, serializers.ListQuery{
		Collection: "issues",
		Filter:     filter,
		Params:     params,
		Sort:       orderBy(e.Request.URL.Query().Get("order_by")),
		PerPage:    perPage,
		Cursor:     cursor,
	}, func(issue *core.Record) any {
		return serializers.Issue(h.App, issue)
	})
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, envelope)
}

// Meta serves GET /api/workspaces/{slug}/projects/{project_id}/issues/{pk}/meta/
// (IssueMetaEndpoint.get) — the two identifiers the web app needs to rebuild an
// issue URL from an id.
func (h *Handler) Meta(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	issue, err := h.findIssue(scope, e.Request.PathValue("pk"))
	if err != nil {
		return err
	}
	// Django reads `Issue.issue_objects.get(...)`, so an archived, draft or
	// triage issue is a 404 here even though Detail would still return it.
	if issue == nil || !h.isIssueObject(issue, scope.Project) {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	return e.JSON(http.StatusOK, map[string]any{
		"sequence_id":        issue.GetInt("sequence_id"),
		"project_identifier": scope.Project.GetString("identifier"),
	})
}

// projectIssues resolves Plane UUIDs to issue records inside the resolved
// project, skipping soft-deleted rows. When issueObjectsOnly is set the
// `Issue.issue_objects` predicate is also applied.
func (h *Handler) projectIssues(scope *middleware.Scope, uuids []string, issueObjectsOnly bool) []*core.Record {
	out := make([]*core.Record, 0, len(uuids))
	for _, uuid := range uuids {
		issue, _ := serializers.FindByUUID(h.App, "issues", uuid)
		if issue == nil || issue.GetString("project") != scope.Project.Id {
			continue
		}
		if issue.GetString("deleted_at") != "" {
			continue
		}
		if issueObjectsOnly && !h.isIssueObject(issue, scope.Project) {
			continue
		}
		out = append(out, issue)
	}
	return out
}

// isIssueObject mirrors IssueManager.get_queryset: not soft-deleted, not
// archived, not a draft, not under an archived project, and not in the Triage
// state group. A null state is kept (Django's exclude leaves null relations).
func (h *Handler) isIssueObject(issue, project *core.Record) bool {
	if issue.GetString("deleted_at") != "" || issue.GetString("archived_at") != "" || issue.GetBool("is_draft") {
		return false
	}
	if project.GetString("archived_at") != "" {
		return false
	}
	state, _ := h.App.FindRecordById("states", issue.GetString("state"))
	if state != nil && state.GetString("group") == "triage" {
		return false
	}
	return true
}

// stateGroup returns the issue's state group, or "" when the state is unset.
func (h *Handler) stateGroup(issue *core.Record) string {
	state, _ := h.App.FindRecordById("states", issue.GetString("state"))
	if state == nil {
		return ""
	}
	return state.GetString("group")
}

// softDeleteRelated sets `deleted_at` on every live row of `collection` whose
// `issue` column points at issueID. PocketBase has no cascade for Plane's
// soft-delete semantics, so the caller drives it explicitly.
func (h *Handler) softDeleteRelated(collection, issueID string) {
	rows, err := h.App.FindRecordsByFilter(
		collection,
		"issue = {:issue} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"issue": issueID},
	)
	if err != nil {
		return
	}
	now := serializers.Now()
	for _, row := range rows {
		row.Set("deleted_at", now)
		_ = h.App.Save(row)
	}
}

// addAssignees adds the given users to an issue without removing existing
// assignees (bulk operations are additive on the client side).
func (h *Handler) addAssignees(project, issue *core.Record, ids []string, user *core.Record) error {
	existing, err := h.App.FindRecordsByFilter(
		"issue_assignees",
		"issue = {:issue} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"issue": issue.Id},
	)
	if err != nil {
		return err
	}
	have := make(map[string]struct{}, len(existing))
	for _, row := range existing {
		have[row.GetString("assignee")] = struct{}{}
	}

	collection, err := h.App.FindCollectionByNameOrId("issue_assignees")
	if err != nil {
		return err
	}
	for _, userUUID := range ids {
		member, _ := serializers.FindByUUID(h.App, "users", userUUID)
		if member == nil {
			continue
		}
		if _, ok := have[member.Id]; ok {
			continue
		}
		if !h.isEligibleAssignee(h.App, project, member.Id) {
			continue
		}
		row := core.NewRecord(collection)
		row.Set("uuid", serializers.NewUUID())
		row.Set("issue", issue.Id)
		row.Set("assignee", member.Id)
		row.Set("project", project.Id)
		row.Set("workspace", project.GetString("workspace"))
		if user != nil {
			row.Set("created_by", user.Id)
		}
		if err := h.App.Save(row); err != nil {
			return err
		}
		have[member.Id] = struct{}{}
	}
	return nil
}

// addLabels adds the given labels to an issue without removing existing ones.
func (h *Handler) addLabels(project, issue *core.Record, ids []string, user *core.Record) error {
	existing, err := h.App.FindRecordsByFilter(
		"issue_labels",
		"issue = {:issue} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"issue": issue.Id},
	)
	if err != nil {
		return err
	}
	have := make(map[string]struct{}, len(existing))
	for _, row := range existing {
		have[row.GetString("label")] = struct{}{}
	}

	collection, err := h.App.FindCollectionByNameOrId("issue_labels")
	if err != nil {
		return err
	}
	for _, labelUUID := range ids {
		label, _ := serializers.FindByUUID(h.App, "labels", labelUUID)
		if label == nil || label.GetString("project") != project.Id {
			continue
		}
		if _, ok := have[label.Id]; ok {
			continue
		}
		row := core.NewRecord(collection)
		row.Set("uuid", serializers.NewUUID())
		row.Set("issue", issue.Id)
		row.Set("label", label.Id)
		row.Set("project", project.Id)
		row.Set("workspace", project.GetString("workspace"))
		if user != nil {
			row.Set("created_by", user.Id)
		}
		if err := h.App.Save(row); err != nil {
			return err
		}
		have[label.Id] = struct{}{}
	}
	return nil
}
