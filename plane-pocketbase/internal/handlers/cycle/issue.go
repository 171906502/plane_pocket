package cycle

import (
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/filters"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// IssuesList serves GET …/cycles/{cycle_id}/cycle-issues/ (CycleIssueViewSet.list).
// It returns the cursor-paginated issue envelope (`TIssuesResponse`).
//
// Grouping (`group_by`/`sub_group_by`) is not implemented: the flat envelope is
// returned regardless, which is what the endpoint produces when no grouping is
// requested. ADMIN/MEMBER only, matching the Django decorator.
func (h *Handler) IssuesList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	cycle, err := h.findCycle(scope, e.Request.PathValue("cycle_id"))
	if err != nil {
		return err
	}
	if cycle == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Cycle not found"})
	}

	ids := h.bridgeIssueIDs("cycle_issues", "cycle", cycle.Id)
	params := dbx.Params{}
	filter := "deleted_at = '' && archived_at = '' && is_draft = false && " + orIDFilter("id", "i", ids, params)
	// Django's CycleIssueViewSet filters `state_id`/`priority` with
	// MultipleChoiceFilter, so the query string carries comma-separated values
	// (e.g. `?priority=urgent,high` from the active-cycle urgent/high board).
	if raw := e.Request.URL.Query().Get("state_id"); raw != "" {
		stateIDs := make([]string, 0)
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if state, _ := serializers.FindByUUID(h.App, "states", part); state != nil {
				stateIDs = append(stateIDs, state.Id)
			}
		}
		if len(stateIDs) > 0 {
			filter += " && " + orIDFilter("state", "st", stateIDs, params)
		}
	}
	if raw := e.Request.URL.Query().Get("priority"); raw != "" {
		if clause := orValues("priority", raw, params); clause != "" {
			filter += " && " + clause
		}
	}
	// Rich filters: the shared issue-filter helper serialises the expression
	// tree into the `filters` query param for every issue board.
	filter = filters.Apply(h.App, filter, params, scope.Project.Id, e.Request.URL.Query().Get("filters"))

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

// IssuesCreate serves POST …/cycles/{cycle_id}/cycle-issues/ (body
// `{issues: [<uuid>…]}`). Issues already linked to another cycle in the project
// are re-pointed at this cycle; the rest are linked afresh.
func (h *Handler) IssuesCreate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	cycle, err := h.findCycle(scope, e.Request.PathValue("cycle_id"))
	if err != nil {
		return err
	}
	if cycle == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Cycle not found"})
	}
	if end := cycle.GetDateTime("end_date").Time(); !end.IsZero() && end.Before(serializers.Now()) {
		return e.JSON(http.StatusBadRequest, map[string]any{
			"error": "The Cycle has already been completed so no new issues can be added",
		})
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	requested := uuidList(body, "issues")
	if len(requested) == 0 {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Issues are required"})
	}

	collection, err := h.App.FindCollectionByNameOrId("cycle_issues")
	if err != nil {
		return err
	}
	err = h.App.RunInTransaction(func(txApp core.App) error {
		for _, issueUUID := range requested {
			issue, _ := serializers.FindByUUID(txApp, "issues", issueUUID)
			if issue == nil || issue.GetString("project") != scope.Project.Id || issue.GetString("deleted_at") != "" {
				continue
			}
			existing, _ := txApp.FindFirstRecordByFilter(
				"cycle_issues",
				"issue = {:issue} && project = {:project} && deleted_at = ''",
				dbx.Params{"issue": issue.Id, "project": scope.Project.Id},
			)
			if existing != nil {
				if existing.GetString("cycle") == cycle.Id {
					continue
				}
				existing.Set("cycle", cycle.Id)
				existing.Set("updated_by", user.Id)
				if err := txApp.Save(existing); err != nil {
					return err
				}
				continue
			}
			link := core.NewRecord(collection)
			link.Set("uuid", serializers.NewUUID())
			link.Set("cycle", cycle.Id)
			link.Set("issue", issue.Id)
			link.Set("project", scope.Project.Id)
			link.Set("workspace", scope.Workspace.Id)
			link.Set("created_by", user.Id)
			if err := txApp.Save(link); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusCreated, map[string]any{"message": "success"})
}

// IssueDetail serves GET …/cycles/{cycle_id}/cycle-issues/{issue_id}/.
func (h *Handler) IssueDetail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	link, issue, err := h.findCycleIssue(scope, e.Request.PathValue("cycle_id"), e.Request.PathValue("issue_id"))
	if err != nil {
		return err
	}
	if link == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	return e.JSON(http.StatusOK, h.serializeCycleIssue(link, issue))
}

// IssueUpdate serves PUT/PATCH …/cycles/{cycle_id}/cycle-issues/{issue_id}/.
// The bridge row carries no editable field (workspace/project/cycle are
// read-only in CycleIssueSerializer), so this is a validated no-op that echoes
// the current link, matching DRF's partial_update semantics.
func (h *Handler) IssueUpdate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	link, issue, err := h.findCycleIssue(scope, e.Request.PathValue("cycle_id"), e.Request.PathValue("issue_id"))
	if err != nil {
		return err
	}
	if link == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	return e.JSON(http.StatusOK, h.serializeCycleIssue(link, issue))
}

// IssueDelete serves DELETE …/cycles/{cycle_id}/cycle-issues/{issue_id}/:
// soft-delete the bridge row.
func (h *Handler) IssueDelete(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	link, _, err := h.findCycleIssue(scope, e.Request.PathValue("cycle_id"), e.Request.PathValue("issue_id"))
	if err != nil {
		return err
	}
	if link == nil {
		return e.NoContent(http.StatusNoContent)
	}
	link.Set("deleted_at", serializers.Now())
	if err := h.App.Save(link); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// findCycleIssue resolves the bridge row (and its issue) for a
// (cycle_id, issue_id) pair, scoped to the project.
func (h *Handler) findCycleIssue(scope *middleware.Scope, cycleUUID, issueUUID string) (*core.Record, *core.Record, error) {
	cycle, err := h.findCycle(scope, cycleUUID)
	if err != nil || cycle == nil {
		return nil, nil, err
	}
	issue, _ := serializers.FindByUUID(h.App, "issues", issueUUID)
	if issue == nil || issue.GetString("project") != scope.Project.Id || issue.GetString("deleted_at") != "" {
		return nil, nil, nil
	}
	link, _ := h.App.FindFirstRecordByFilter(
		"cycle_issues",
		"cycle = {:cycle} && issue = {:issue} && deleted_at = ''",
		dbx.Params{"cycle": cycle.Id, "issue": issue.Id},
	)
	if link == nil {
		return nil, nil, nil
	}
	return link, issue, nil
}

// serializeCycleIssue builds the CycleIssueSerializer shape
// (`CycleIssueResponse` on the frontend).
func (h *Handler) serializeCycleIssue(link, issue *core.Record) map[string]any {
	out := map[string]any{
		"id":               serializers.UUID(link),
		"created_at":       serializers.DateTime(link, "created_at"),
		"updated_at":       serializers.DateTime(link, "updated_at"),
		"created_by":       serializers.RelationUUID(h.App, link, "created_by"),
		"updated_by":       serializers.RelationUUID(h.App, link, "updated_by"),
		"project":          serializers.RelationUUID(h.App, link, "project"),
		"workspace":        serializers.RelationUUID(h.App, link, "workspace"),
		"cycle":            serializers.RelationUUID(h.App, link, "cycle"),
		"issue":            serializers.RelationUUID(h.App, link, "issue"),
		"sub_issues_count": 0,
	}
	if issue != nil {
		out["issue_detail"] = serializers.Issue(h.App, issue)
	}
	return out
}

// bridgeIssueIDs returns the PocketBase ids of the issues linked through a
// bridge table, newest bridge row first.
func (h *Handler) bridgeIssueIDs(bridge, fk, ownerID string) []string {
	rows, err := h.App.FindRecordsByFilter(
		bridge,
		fk+" = {:owner} && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"owner": ownerID},
	)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if issue := row.GetString("issue"); issue != "" {
			ids = append(ids, issue)
		}
	}
	return ids
}

// orderBy maps Plane's `order_by` values onto PocketBase sort expressions
// (same mapping the issue handler uses).
func orderBy(param string) string {
	switch param {
	case "created_at":
		return "created_at"
	case "-created_at", "":
		return "-created_at"
	case "updated_at":
		return "updated_at"
	case "-updated_at":
		return "-updated_at"
	case "sort_order":
		return "sort_order"
	case "-sort_order":
		return "-sort_order"
	case "sequence_id":
		return "sequence_id"
	case "-sequence_id":
		return "-sequence_id"
	case "priority":
		return "priority"
	case "-priority":
		return "-priority"
	default:
		return "-created_at"
	}
}

// uuidList reads a list-valued string field from a request body.
func uuidList(body map[string]any, key string) []string {
	raw, ok := body[key]
	if !ok {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
