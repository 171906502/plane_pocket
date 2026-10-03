package search

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// validAnalyticsFields mirrors plane/utils/analytics_plot.VALID_ANALYTICS_FIELDS.
var validAnalyticsFields = map[string]bool{
	"state_id": true, "state__group": true, "labels__id": true, "assignees__id": true,
	"estimate_point__value": true, "issue_cycle__cycle_id": true, "issue_module__module_id": true,
	"priority": true, "start_date": true, "target_date": true, "created_at": true, "completed_at": true,
}

// validYAxis mirrors VALID_YAXIS.
var validYAxis = map[string]bool{"issue_count": true, "estimate": true}

// ---------------------------------------------------------------------------
// /analytics/
// ---------------------------------------------------------------------------

// Analytics serves GET /api/workspaces/{slug}/analytics/ (AnalyticsEndpoint).
// Workspace ADMIN/MEMBER only.
func (h *Handler) Analytics(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if scope.WorkspaceRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	query := e.Request.URL.Query()
	xAxis := query.Get("x_axis")
	yAxis := query.Get("y_axis")
	segment := query.Get("segment")
	if xAxis == "" || yAxis == "" || !validAnalyticsFields[xAxis] || !validYAxis[yAxis] {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "x-axis and y-axis dimensions are required and the values should be valid"})
	}
	if segment != "" && (!validAnalyticsFields[segment] || xAxis == segment) {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Both segment and x axis cannot be same and segment should be valid"})
	}

	issues := h.workspaceIssues(scope.Workspace.Id, nil)
	extras := map[string]any{
		"state_details":    h.stateDetails(issues, xAxis, segment),
		"assignee_details": h.assigneeDetails(issues, xAxis, segment),
		"label_details":    h.labelDetails(issues, xAxis, segment),
		"cycle_details":    h.cycleDetails(issues, xAxis, segment),
		"module_details":   h.moduleDetails(issues, xAxis, segment),
	}
	return e.JSON(http.StatusOK, map[string]any{
		"total":        len(issues),
		"distribution": h.distribution(issues, xAxis, yAxis, segment),
		"extras":       extras,
	})
}

// ---------------------------------------------------------------------------
// /analytic-view/
// ---------------------------------------------------------------------------

// AnalyticViewList serves GET /api/workspaces/{slug}/analytic-view/.
func (h *Handler) AnalyticViewList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if scope.WorkspaceRole < middleware.RoleAdmin {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	records, err := h.App.FindRecordsByFilter("analytic_views", "workspace = {:ws}", "-created_at", 0, 0, dbx.Params{"ws": scope.Workspace.Id})
	if err != nil {
		return err
	}
	out := make([]any, 0, len(records))
	for _, r := range records {
		out = append(out, serializers.AnalyticView(h.App, r))
	}
	return e.JSON(http.StatusOK, out)
}

// AnalyticViewCreate serves POST /api/workspaces/{slug}/analytic-view/.
func (h *Handler) AnalyticViewCreate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if scope.WorkspaceRole < middleware.RoleAdmin {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	body, _ := bindBody(e)
	name := strings.TrimSpace(asString(body["name"]))
	if name == "" {
		return serializers.Validation(e, "name", "This field is required.")
	}
	collection, err := h.App.FindCollectionByNameOrId("analytic_views")
	if err != nil {
		return err
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("workspace", scope.Workspace.Id)
	record.Set("name", name)
	if v, ok := body["description"]; ok {
		record.Set("description", v)
	}
	if v, ok := body["query"]; ok {
		record.Set("query", v)
	}
	if v, ok := body["query_dict"]; ok {
		record.Set("query_dict", v)
	}
	if user != nil {
		record.Set("created_by", user.Id)
	}
	if err := h.App.Save(record); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusCreated, serializers.AnalyticView(h.App, record))
}

// AnalyticViewDetail serves GET /api/workspaces/{slug}/analytic-view/{pk}/.
func (h *Handler) AnalyticViewDetail(e *core.RequestEvent) error {
	record, err := h.scopedAnalyticView(e)
	if err != nil || record == nil {
		return err
	}
	return e.JSON(http.StatusOK, serializers.AnalyticView(h.App, record))
}

// AnalyticViewUpdate serves PATCH /api/workspaces/{slug}/analytic-view/{pk}/.
func (h *Handler) AnalyticViewUpdate(e *core.RequestEvent) error {
	record, err := h.scopedAnalyticView(e)
	if err != nil || record == nil {
		return err
	}
	body, _ := bindBody(e)
	for _, field := range []string{"name", "description", "query", "query_dict"} {
		if v, ok := body[field]; ok {
			record.Set(field, v)
		}
	}
	user := middleware.CurrentUser(e)
	if user != nil {
		record.Set("updated_by", user.Id)
	}
	if err := h.App.Save(record); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.AnalyticView(h.App, record))
}

// AnalyticViewDelete serves DELETE /api/workspaces/{slug}/analytic-view/{pk}/.
func (h *Handler) AnalyticViewDelete(e *core.RequestEvent) error {
	record, err := h.scopedAnalyticView(e)
	if err != nil || record == nil {
		return err
	}
	if err := h.App.Delete(record); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// SavedAnalyticView serves GET
// /api/workspaces/{slug}/saved-analytic-view/{analytic_id}/ (SavedAnalyticEndpoint).
func (h *Handler) SavedAnalyticView(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if scope.WorkspaceRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	view, err := serializers.FindByUUIDOrID(h.App, "analytic_views", e.Request.PathValue("analytic_id"))
	if err != nil || view == nil || view.GetString("workspace") != scope.Workspace.Id {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	queryDict := jsonMap(view.Get("query_dict"))
	xAxis := asString(queryDict["x_axis"])
	yAxis := asString(queryDict["y_axis"])
	if xAxis == "" || yAxis == "" || !validAnalyticsFields[xAxis] || !validYAxis[yAxis] {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "x-axis and y-axis dimensions are required and the values should be valid"})
	}
	segment := e.Request.URL.Query().Get("segment")
	if segment != "" && (!validAnalyticsFields[segment] || xAxis == segment) {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Both segment and x axis cannot be same and segment should be valid"})
	}
	issues := h.workspaceIssues(scope.Workspace.Id, nil)
	return e.JSON(http.StatusOK, map[string]any{
		"total":        len(issues),
		"distribution": h.distribution(issues, xAxis, yAxis, segment),
	})
}

// ---------------------------------------------------------------------------
// /default-analytics/ and /project-stats/
// ---------------------------------------------------------------------------

// DefaultAnalytics serves GET /api/workspaces/{slug}/default-analytics/.
func (h *Handler) DefaultAnalytics(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	issues := h.workspaceIssues(scope.Workspace.Id, nil)
	openGroups := map[string]bool{"backlog": true, "unstarted": true, "started": true}

	totalClassified := h.groupCounts(issues, "state__group")
	openIssues := make([]*core.Record, 0)
	for _, issue := range issues {
		if openGroups[h.issueStateGroup(issue)] {
			openIssues = append(openIssues, issue)
		}
	}
	year := time.Now().UTC().Year()
	monthCounts := map[int]int{}
	for _, issue := range issues {
		if completed := issue.GetDateTime("completed_at"); !completed.IsZero() && completed.Time().Year() == year {
			monthCounts[int(completed.Time().Month())]++
		}
	}
	monthWise := make([]map[string]any, 0, len(monthCounts))
	months := make([]int, 0, len(monthCounts))
	for month := range monthCounts {
		months = append(months, month)
	}
	sort.Ints(months)
	for _, month := range months {
		monthWise = append(monthWise, map[string]any{"month": month, "count": monthCounts[month]})
	}

	var openEstimate, totalEstimate float64
	for _, issue := range issues {
		totalEstimate += issue.GetFloat("point")
	}
	for _, issue := range openIssues {
		openEstimate += issue.GetFloat("point")
	}

	return e.JSON(http.StatusOK, map[string]any{
		"total_issues":               len(issues),
		"total_issues_classified":    totalClassified,
		"open_issues":                len(openIssues),
		"open_issues_classified":     h.groupCounts(openIssues, "state__group"),
		"issue_completed_month_wise": monthWise,
		"most_issue_created_user":    h.topUsers(issues, "created_by", 5),
		"most_issue_closed_user":     h.topUsers(h.completedIssues(issues), "assignee", 5),
		"pending_issue_user":         h.topUsers(h.pendingIssues(issues), "assignee", 0),
		"open_estimate_sum":          openEstimate,
		"total_estimate_sum":         totalEstimate,
	})
}

// ProjectStats serves GET /api/workspaces/{slug}/project-stats/.
func (h *Handler) ProjectStats(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	query := e.Request.URL.Query()
	requested := map[string]bool{}
	for _, field := range strings.Split(query.Get("fields"), ",") {
		requested[strings.TrimSpace(field)] = true
	}
	valid := map[string]bool{"total_issues": true, "completed_issues": true, "total_members": true, "total_cycles": true, "total_modules": true}
	if len(requested) == 0 {
		requested = valid
	}

	filter := "workspace = {:ws} && deleted_at = ''"
	params := dbx.Params{"ws": scope.Workspace.Id}
	if ids := query.Get("project_ids"); ids != "" {
		clause, p := projectInFilter("uuid", splitTrim(ids))
		if clause != "" {
			filter += " && " + clause
			for k, v := range p {
				params[k] = v
			}
		}
	}
	projects, err := h.App.FindRecordsByFilter("projects", filter, "", 0, 0, params)
	if err != nil {
		return err
	}
	out := make([]any, 0, len(projects))
	for _, project := range projects {
		row := map[string]any{"id": serializers.UUID(project)}
		issues := h.workspaceIssues(scope.Workspace.Id, []string{project.Id})
		if requested["total_issues"] {
			row["total_issues"] = len(issues)
		}
		if requested["completed_issues"] {
			count := 0
			for _, issue := range issues {
				if group := h.issueStateGroup(issue); group == "completed" || group == "cancelled" {
					count++
				}
			}
			row["completed_issues"] = count
		}
		if requested["total_cycles"] {
			row["total_cycles"] = h.countRows("cycles", "project = {:p} && deleted_at = ''", dbx.Params{"p": project.Id})
		}
		if requested["total_modules"] {
			row["total_modules"] = h.countRows("modules", "project = {:p} && deleted_at = ''", dbx.Params{"p": project.Id})
		}
		if requested["total_members"] {
			row["total_members"] = h.countRows("project_members", "project = {:p} && is_active = true", dbx.Params{"p": project.Id})
		}
		out = append(out, row)
	}
	return e.JSON(http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// advance analytics
// ---------------------------------------------------------------------------

// AdvanceAnalytics serves GET /api/workspaces/{slug}/advance-analytics/.
func (h *Handler) AdvanceAnalytics(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if scope.WorkspaceRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	tab := e.Request.URL.Query().Get("tab")
	if tab == "" {
		tab = "overview"
	}
	issues := h.workspaceIssues(scope.Workspace.Id, nil)
	groups := h.groupCounts(issues, "state__group")
	if tab == "work-items" {
		return e.JSON(http.StatusOK, map[string]any{
			"total_work_items":      countOf(len(issues)),
			"started_work_items":    countOf(groups["started"]),
			"backlog_work_items":    countOf(groups["backlog"]),
			"un_started_work_items": countOf(groups["unstarted"]),
			"completed_work_items":  countOf(groups["completed"]),
		})
	}
	if tab != "overview" {
		return e.JSON(http.StatusBadRequest, map[string]any{"message": "Invalid tab"})
	}
	members, _ := h.App.FindRecordsByFilter("workspace_members", "workspace = {:ws} && is_active = true", "", 0, 0, dbx.Params{"ws": scope.Workspace.Id})
	return e.JSON(http.StatusOK, map[string]any{
		"total_users":      countOf(len(members)),
		"total_admins":     countOf(h.memberRoleCount(members, middleware.RoleAdmin)),
		"total_members":    countOf(h.memberRoleCount(members, middleware.RoleMember)),
		"total_guests":     countOf(h.memberRoleCount(members, middleware.RoleGuest)),
		"total_projects":   countOf(h.countRows("projects", "workspace = {:ws} && deleted_at = ''", dbx.Params{"ws": scope.Workspace.Id})),
		"total_work_items": countOf(len(issues)),
		"total_cycles":     countOf(h.countRows("cycles", "workspace = {:ws} && deleted_at = ''", dbx.Params{"ws": scope.Workspace.Id})),
		"total_intake":     countOf(h.countRows("intake_issues", "workspace = {:ws}", dbx.Params{"ws": scope.Workspace.Id})),
	})
}

// AdvanceAnalyticsStats serves GET /api/workspaces/{slug}/advance-analytics-stats/.
func (h *Handler) AdvanceAnalyticsStats(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if scope.WorkspaceRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	return e.JSON(http.StatusOK, h.projectWorkItemStats(scope.Workspace.Id, nil))
}

// AdvanceAnalyticsCharts serves GET /api/workspaces/{slug}/advance-analytics-charts/.
func (h *Handler) AdvanceAnalyticsCharts(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if scope.WorkspaceRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	query := e.Request.URL.Query()
	switch query.Get("type") {
	case "work-items":
		return e.JSON(http.StatusOK, h.workItemCompletionChart(scope.Workspace.Id, nil))
	case "custom-work-items":
		issues := h.workspaceIssues(scope.Workspace.Id, nil)
		return e.JSON(http.StatusOK, h.customChart(issues, query.Get("x_axis")))
	default:
		return e.JSON(http.StatusOK, h.projectChart(scope.Workspace.Id))
	}
}

// ProjectAdvanceAnalytics serves GET
// /api/workspaces/{slug}/projects/{project_id}/advance-analytics/.
func (h *Handler) ProjectAdvanceAnalytics(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	issues := h.workspaceIssues(scope.Workspace.Id, []string{scope.Project.Id})
	groups := h.groupCounts(issues, "state__group")
	return e.JSON(http.StatusOK, map[string]any{
		"total_work_items":      countOf(len(issues)),
		"started_work_items":    countOf(groups["started"]),
		"backlog_work_items":    countOf(groups["backlog"]),
		"un_started_work_items": countOf(groups["unstarted"]),
		"completed_work_items":  countOf(groups["completed"]),
	})
}

// ProjectAdvanceAnalyticsStats serves GET
// /api/workspaces/{slug}/projects/{project_id}/advance-analytics-stats/.
func (h *Handler) ProjectAdvanceAnalyticsStats(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	issues := h.workspaceIssues(scope.Workspace.Id, []string{scope.Project.Id})
	assignees := map[string]map[string]any{}
	for _, issue := range issues {
		for _, assigneeID := range h.issueAssignees(issue) {
			row := assignees[assigneeID]
			if row == nil {
				user := h.userRecord(assigneeID)
				row = map[string]any{
					"display_name":          userName(user),
					"assignee_id":           serializers.UUID(user),
					"avatar_url":            h.avatarURL(user),
					"cancelled_work_items":  0,
					"completed_work_items":  0,
					"backlog_work_items":    0,
					"un_started_work_items": 0,
					"started_work_items":    0,
				}
				assignees[assigneeID] = row
			}
			group := h.issueStateGroup(issue)
			switch group {
			case "cancelled":
				row["cancelled_work_items"] = row["cancelled_work_items"].(int) + 1
			case "completed":
				row["completed_work_items"] = row["completed_work_items"].(int) + 1
			case "backlog":
				row["backlog_work_items"] = row["backlog_work_items"].(int) + 1
			case "unstarted":
				row["un_started_work_items"] = row["un_started_work_items"].(int) + 1
			case "started":
				row["started_work_items"] = row["started_work_items"].(int) + 1
			}
		}
	}
	out := make([]any, 0, len(assignees))
	for _, row := range assignees {
		out = append(out, row)
	}
	return e.JSON(http.StatusOK, out)
}

// ProjectAdvanceAnalyticsCharts serves GET
// /api/workspaces/{slug}/projects/{project_id}/advance-analytics-charts/.
func (h *Handler) ProjectAdvanceAnalyticsCharts(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	query := e.Request.URL.Query()
	switch query.Get("type") {
	case "custom-work-items":
		issues := h.workspaceIssues(scope.Workspace.Id, []string{scope.Project.Id})
		return e.JSON(http.StatusOK, h.customChart(issues, query.Get("x_axis")))
	default:
		return e.JSON(http.StatusOK, h.workItemCompletionChart(scope.Workspace.Id, []string{scope.Project.Id}))
	}
}

// ---------------------------------------------------------------------------
// analytics helpers
// ---------------------------------------------------------------------------

// workspaceIssues returns the non-draft, non-archived issues for a workspace,
// optionally limited to a set of project ids.
func (h *Handler) workspaceIssues(wsID string, projectIDs []string) []*core.Record {
	filter := "workspace = {:ws} && is_draft = false && archived_at = '' && deleted_at = ''"
	params := dbx.Params{"ws": wsID}
	if len(projectIDs) > 0 {
		clause, p := projectInFilter("project", projectIDs)
		if clause == "" {
			return nil
		}
		filter += " && " + clause
		for k, v := range p {
			params[k] = v
		}
	}
	records, err := h.App.FindRecordsByFilter("issues", filter, "-created_at", 0, 0, params)
	if err != nil {
		return nil
	}
	return records
}

// distribution groups issues along xAxis (optionally segmented) and counts them
// (y_axis=issue_count) or sums estimate points (y_axis=estimate). The shape is
// Plane's `{str(dimension): [{dimension, count|estimate, ...}]}`.
func (h *Handler) distribution(issues []*core.Record, xAxis, yAxis, segment string) map[string]any {
	buckets := map[string]map[string]any{}
	for _, issue := range issues {
		keys := h.axisValues(issue, xAxis)
		for _, key := range keys {
			entry := buckets[key]
			if entry == nil {
				entry = map[string]any{"dimension": key, "count": 0, "estimate": float64(0)}
				buckets[key] = entry
			}
			entry["count"] = entry["count"].(int) + 1
			entry["estimate"] = entry["estimate"].(float64) + issue.GetFloat("point")
		}
	}

	field := "count"
	if yAxis == "estimate" {
		field = "estimate"
	}
	result := map[string]any{}
	for key, entry := range buckets {
		result[key] = []any{map[string]any{"dimension": key, field: entry[field]}}
	}
	return result
}

// axisValues maps one issue onto the values of an x-axis dimension (multi-valued
// axes such as labels/assignees yield several).
func (h *Handler) axisValues(issue *core.Record, axis string) []string {
	switch axis {
	case "state_id":
		if state := issue.GetString("state"); state != "" {
			if record, _ := h.App.FindRecordById("states", state); record != nil {
				return []string{serializers.UUID(record)}
			}
		}
		return []string{"null"}
	case "state__group":
		return []string{h.issueStateGroup(issue)}
	case "priority":
		return []string{issue.GetString("priority")}
	case "labels__id":
		ids := h.issueLabels(issue)
		if len(ids) == 0 {
			return []string{"null"}
		}
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			if label, _ := h.App.FindRecordById("labels", id); label != nil {
				out = append(out, serializers.UUID(label))
			}
		}
		return out
	case "assignees__id":
		ids := h.issueAssignees(issue)
		if len(ids) == 0 {
			return []string{"null"}
		}
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			if user := h.userRecord(id); user != nil {
				out = append(out, serializers.UUID(user))
			}
		}
		return out
	case "estimate_point__value":
		if point := issue.Get("estimate_point"); point != nil && point != "" {
			return []string{strconv.FormatFloat(issue.GetFloat("point"), 'f', -1, 64)}
		}
		return []string{"null"}
	case "issue_cycle__cycle_id":
		ids := h.linkedCycleIDs(issue)
		if len(ids) == 0 {
			return []string{"null"}
		}
		return ids
	case "issue_module__module_id":
		ids := h.linkedModuleIDs(issue)
		if len(ids) == 0 {
			return []string{"null"}
		}
		return ids
	case "start_date", "target_date", "completed_at", "created_at":
		dt := issue.GetDateTime(axis)
		if dt.IsZero() {
			return []string{"null"}
		}
		return []string{dt.Time().UTC().Format("2006-1")}
	}
	return []string{"null"}
}

// stateDetails / assigneeDetails / labelDetails / cycleDetails / moduleDetails
// mirror the AnalyticsEndpoint `extras` blocks (populated only when the axis or
// segment references the dimension).
func (h *Handler) stateDetails(issues []*core.Record, axes ...string) any {
	if !usesAxis(axes, "state_id") {
		return map[string]any{}
	}
	seen := map[string]bool{}
	out := []any{}
	for _, issue := range issues {
		if state, _ := h.App.FindRecordById("states", issue.GetString("state")); state != nil && !seen[state.Id] {
			seen[state.Id] = true
			out = append(out, map[string]any{"state_id": serializers.UUID(state), "state__name": state.GetString("name"), "state__color": state.GetString("color")})
		}
	}
	return out
}

func (h *Handler) assigneeDetails(issues []*core.Record, axes ...string) any {
	if !usesAxis(axes, "assignees__id") {
		return map[string]any{}
	}
	seen := map[string]bool{}
	out := []any{}
	for _, issue := range issues {
		for _, id := range h.issueAssignees(issue) {
			if seen[id] {
				continue
			}
			seen[id] = true
			user := h.userRecord(id)
			out = append(out, map[string]any{
				"assignees__avatar_url":   h.avatarURL(user),
				"assignees__display_name": userName(user),
				"assignees__first_name":   firstName(user),
				"assignees__last_name":    lastName(user),
				"assignees__id":           serializers.UUID(user),
			})
		}
	}
	return out
}

func (h *Handler) labelDetails(issues []*core.Record, axes ...string) any {
	if !usesAxis(axes, "labels__id") {
		return map[string]any{}
	}
	seen := map[string]bool{}
	out := []any{}
	for _, issue := range issues {
		for _, id := range h.issueLabels(issue) {
			if seen[id] {
				continue
			}
			seen[id] = true
			label, _ := h.App.FindRecordById("labels", id)
			if label == nil {
				continue
			}
			out = append(out, map[string]any{"labels__id": serializers.UUID(label), "labels__color": label.GetString("color"), "labels__name": label.GetString("name")})
		}
	}
	return out
}

func (h *Handler) cycleDetails(issues []*core.Record, axes ...string) any {
	if !usesAxis(axes, "issue_cycle__cycle_id") {
		return map[string]any{}
	}
	seen := map[string]bool{}
	out := []any{}
	for _, issue := range issues {
		for _, id := range h.linkedCycleIDs(issue) {
			if seen[id] {
				continue
			}
			seen[id] = true
			cycle, _ := h.App.FindRecordById("cycles", id)
			if cycle == nil {
				continue
			}
			out = append(out, map[string]any{"issue_cycle__cycle_id": serializers.UUID(cycle), "issue_cycle__cycle__name": cycle.GetString("name")})
		}
	}
	return out
}

func (h *Handler) moduleDetails(issues []*core.Record, axes ...string) any {
	if !usesAxis(axes, "issue_module__module_id") {
		return map[string]any{}
	}
	seen := map[string]bool{}
	out := []any{}
	for _, issue := range issues {
		for _, id := range h.linkedModuleIDs(issue) {
			if seen[id] {
				continue
			}
			seen[id] = true
			module, _ := h.App.FindRecordById("modules", id)
			if module == nil {
				continue
			}
			out = append(out, map[string]any{"issue_module__module_id": serializers.UUID(module), "issue_module__module__name": module.GetString("name")})
		}
	}
	return out
}

// projectWorkItemStats returns the per-project state-group counts used by
// AdvanceAnalyticsStats.
func (h *Handler) projectWorkItemStats(wsID string, projectIDs []string) []any {
	issues := h.workspaceIssues(wsID, projectIDs)
	byProject := map[string]map[string]int{}
	order := []string{}
	for _, issue := range issues {
		pid := issue.GetString("project")
		if byProject[pid] == nil {
			byProject[pid] = map[string]int{}
			order = append(order, pid)
		}
		byProject[pid][h.issueStateGroup(issue)]++
	}
	out := make([]any, 0, len(order))
	for _, pid := range order {
		project, _ := h.App.FindRecordById("projects", pid)
		counts := byProject[pid]
		out = append(out, map[string]any{
			"project_id":            serializers.UUID(project),
			"project__name":         projectName(project),
			"cancelled_work_items":  counts["cancelled"],
			"completed_work_items":  counts["completed"],
			"backlog_work_items":    counts["backlog"],
			"un_started_work_items": counts["unstarted"],
			"started_work_items":    counts["started"],
		})
	}
	return out
}

// projectChart mirrors AdvanceAnalyticsChartEndpoint.project_chart.
func (h *Handler) projectChart(wsID string) []any {
	data := []struct {
		key   string
		count int
	}{
		{"work_items", len(h.workspaceIssues(wsID, nil))},
		{"cycles", h.countRows("cycles", "workspace = {:ws} && deleted_at = ''", dbx.Params{"ws": wsID})},
		{"modules", h.countRows("modules", "workspace = {:ws} && deleted_at = ''", dbx.Params{"ws": wsID})},
		{"intake", h.countRows("intake_issues", "workspace = {:ws}", dbx.Params{"ws": wsID})},
		{"members", h.countRows("workspace_members", "workspace = {:ws} && is_active = true", dbx.Params{"ws": wsID})},
		{"pages", h.countRows("pages", "workspace = {:ws} && deleted_at = ''", dbx.Params{"ws": wsID})},
		{"views", h.countRows("issue_views", "workspace = {:ws} && deleted_at = ''", dbx.Params{"ws": wsID})},
	}
	out := make([]any, 0, len(data))
	for _, item := range data {
		out = append(out, map[string]any{"key": item.key, "name": titleize(item.key), "count": item.count})
	}
	return out
}

// workItemCompletionChart mirrors the monthly completion chart.
func (h *Handler) workItemCompletionChart(wsID string, projectIDs []string) map[string]any {
	issues := h.workspaceIssues(wsID, projectIDs)
	created := map[string]int{}
	completed := map[string]int{}
	start := time.Now().UTC()
	for _, issue := range issues {
		if dt := issue.GetDateTime("created_at"); !dt.IsZero() {
			month := dt.Time().UTC().Format("2006-01")
			created[month]++
			if t := dt.Time().UTC(); t.Before(start) {
				start = t
			}
		}
		if dt := issue.GetDateTime("completed_at"); !dt.IsZero() {
			completed[dt.Time().UTC().Format("2006-01")]++
		}
	}
	data := []any{}
	current := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
	last := time.Now().UTC()
	last = time.Date(last.Year(), last.Month(), 1, 0, 0, 0, 0, time.UTC)
	for !current.After(last) {
		key := current.Format("2006-01")
		date := current.Format("2006-01-02")
		data = append(data, map[string]any{
			"key":              date,
			"name":             date,
			"count":            created[key],
			"completed_issues": completed[key],
			"created_issues":   created[key],
		})
		current = current.AddDate(0, 1, 0)
	}
	return map[string]any{
		"data":   data,
		"schema": map[string]any{"completed_issues": "completed_issues", "created_issues": "created_issues"},
	}
}

// customChart groups issues by the requested x-axis (build_analytics_chart).
func (h *Handler) customChart(issues []*core.Record, xAxis string) []any {
	if xAxis == "" {
		xAxis = "PRIORITY"
	}
	axis := map[string]string{
		"PRIORITY": "priority", "STATE_GROUPS": "state__group", "STATES": "state_id",
		"CREATED_AT": "created_at", "COMPLETED_AT": "completed_at",
	}[xAxis]
	if axis == "" {
		axis = "priority"
	}
	counts := map[string]int{}
	order := []string{}
	for _, issue := range issues {
		for _, key := range h.axisValues(issue, axis) {
			if _, ok := counts[key]; !ok {
				order = append(order, key)
			}
			counts[key]++
		}
	}
	sort.Strings(order)
	out := make([]any, 0, len(order))
	for _, key := range order {
		out = append(out, map[string]any{"key": key, "name": key, "count": counts[key]})
	}
	return out
}

// scopedAnalyticView resolves an analytic view in the resolved workspace.
func (h *Handler) scopedAnalyticView(e *core.RequestEvent) (*core.Record, error) {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return nil, serializers.NotFound(e, "")
	}
	if scope.WorkspaceRole < middleware.RoleAdmin {
		return nil, serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	record, err := serializers.FindByUUIDOrID(h.App, "analytic_views", e.Request.PathValue("pk"))
	if err != nil {
		return nil, err
	}
	if record == nil || record.GetString("workspace") != scope.Workspace.Id {
		return nil, serializers.NotFound(e, "The required object does not exist.")
	}
	return record, nil
}

// groupCounts counts issues by state group.
func (h *Handler) groupCounts(issues []*core.Record, _ string) map[string]int {
	out := map[string]int{}
	for _, issue := range issues {
		out[h.issueStateGroup(issue)]++
	}
	return out
}

// topUsers ranks users by the number of issues they created or are assigned to.
func (h *Handler) topUsers(issues []*core.Record, mode string, limit int) []any {
	counts := map[string]int{}
	for _, issue := range issues {
		if mode == "created_by" {
			if id := issue.GetString("created_by"); id != "" {
				counts[id]++
			}
			continue
		}
		for _, id := range h.issueAssignees(issue) {
			counts[id]++
		}
	}
	ids := make([]string, 0, len(counts))
	for id := range counts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if counts[ids[i]] != counts[ids[j]] {
			return counts[ids[i]] > counts[ids[j]]
		}
		return ids[i] < ids[j]
	})
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		user := h.userRecord(id)
		out = append(out, map[string]any{
			"first_name":   firstName(user),
			"last_name":    lastName(user),
			"display_name": userName(user),
			"id":           serializers.UUID(user),
			"avatar_url":   h.avatarURL(user),
			"count":        counts[id],
		})
	}
	return out
}

// completedIssues / pendingIssues filter for the DefaultAnalytics user rankings.
func (h *Handler) completedIssues(issues []*core.Record) []*core.Record {
	out := []*core.Record{}
	for _, issue := range issues {
		if !issue.GetDateTime("completed_at").IsZero() {
			out = append(out, issue)
		}
	}
	return out
}

func (h *Handler) pendingIssues(issues []*core.Record) []*core.Record {
	out := []*core.Record{}
	for _, issue := range issues {
		if issue.GetDateTime("completed_at").IsZero() {
			out = append(out, issue)
		}
	}
	return out
}

// issueStateGroup reads an issue's state group.
func (h *Handler) issueStateGroup(issue *core.Record) string {
	if issue == nil {
		return ""
	}
	state, _ := h.App.FindRecordById("states", issue.GetString("state"))
	if state == nil {
		return ""
	}
	return state.GetString("group")
}

// issueAssignees returns the internal ids of an issue's active assignees.
func (h *Handler) issueAssignees(issue *core.Record) []string {
	rows, err := h.App.FindRecordsByFilter("issue_assignees", "issue = {:issue} && deleted_at = ''", "", 0, 0, dbx.Params{"issue": issue.Id})
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if id := row.GetString("assignee"); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// issueLabels returns the internal ids of an issue's active labels.
func (h *Handler) issueLabels(issue *core.Record) []string {
	rows, err := h.App.FindRecordsByFilter("issue_labels", "issue = {:issue} && deleted_at = ''", "", 0, 0, dbx.Params{"issue": issue.Id})
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if id := row.GetString("label"); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// linkedCycleIDs returns the internal ids of cycles an issue belongs to.
func (h *Handler) linkedCycleIDs(issue *core.Record) []string {
	rows, err := h.App.FindRecordsByFilter("cycle_issues", "issue = {:issue} && deleted_at = ''", "", 0, 0, dbx.Params{"issue": issue.Id})
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if id := row.GetString("cycle"); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// linkedModuleIDs returns the internal ids of modules an issue belongs to.
func (h *Handler) linkedModuleIDs(issue *core.Record) []string {
	rows, err := h.App.FindRecordsByFilter("module_issues", "issue = {:issue} && deleted_at = ''", "", 0, 0, dbx.Params{"issue": issue.Id})
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if id := row.GetString("module"); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// countRows counts records matching a filter.
func (h *Handler) countRows(collection, filter string, params dbx.Params) int {
	records, err := h.App.FindRecordsByFilter(collection, filter, "", 0, 0, params)
	if err != nil {
		return 0
	}
	return len(records)
}

// memberRoleCount counts memberships at the given role.
func (h *Handler) memberRoleCount(members []*core.Record, role int) int {
	count := 0
	for _, member := range members {
		if member.GetInt("role") == role {
			count++
		}
	}
	return count
}

// countOf wraps an int in Plane's {"count": n} envelope.
func countOf(n int) map[string]any {
	return map[string]any{"count": n}
}

// usesAxis reports whether any of the given axes is wanted.
func usesAxis(axes []string, wanted string) bool {
	for _, axis := range axes {
		if axis == wanted {
			return true
		}
	}
	return false
}

// titleize turns "work_items" into "Work Items".
func titleize(key string) string {
	parts := strings.Split(key, "_")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

// splitTrim splits a CSV and trims each element.
func splitTrim(raw string) []string {
	out := []string{}
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// userName / firstName / lastName tolerate a nil user record.
func userName(user *core.Record) any {
	if user == nil {
		return nil
	}
	return user.GetString("display_name")
}

func firstName(user *core.Record) any {
	if user == nil {
		return nil
	}
	return user.GetString("first_name")
}

func lastName(user *core.Record) any {
	if user == nil {
		return nil
	}
	return user.GetString("last_name")
}
