package dashboard

import (
	"strconv"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// widgetStats dispatches on `?widget_key=` and returns the matching
// TWidgetStatsResponse variant (packages/types/src/dashboard.ts:159). Each
// variant is a DIFFERENT JSON type (object / array / array-of-strings), so the
// switch is load-bearing, not cosmetic.
//
// The widget data is scoped to the caller: "my" issues in the workspace means
// issues the caller created OR is assigned to. This is a DERIVED definition —
// there is no Django view to mirror.
func (h *Handler) widgetStats(e *core.RequestEvent, userID, workspaceID string) any {
	switch e.Request.URL.Query().Get("widget_key") {
	case "overview_stats":
		return h.overviewStats(userID, workspaceID)
	case "assigned_issues":
		return h.issueListWidget(userID, workspaceID, true)
	case "created_issues":
		return h.issueListWidget(userID, workspaceID, false)
	case "issues_by_state_groups":
		return h.issuesByStateGroups(userID, workspaceID)
	case "issues_by_priority":
		return h.issuesByPriority(userID, workspaceID)
	case "recent_activity":
		return h.recentActivity(workspaceID)
	case "recent_projects":
		return h.recentProjects(workspaceID)
	case "recent_collaborators":
		return h.recentCollaborators(workspaceID)
	default:
		return map[string]any{}
	}
}

// overviewStats → TOverviewStatsWidgetResponse.
func (h *Handler) overviewStats(userID, workspaceID string) map[string]any {
	params := dbx.Params{"ws": workspaceID, "u": userID}
	assigned := h.scalarCount(`
		SELECT COUNT(DISTINCT i.id) FROM issues i
		JOIN issue_assignees ia ON ia.issue = i.id AND ia.deleted_at = ''
		WHERE i.workspace = {:ws} AND i.deleted_at = '' AND ia.assignee = {:u}`, params)
	created := h.scalarCount(`
		SELECT COUNT(*) FROM issues
		WHERE workspace = {:ws} AND deleted_at = '' AND created_by = {:u}`, params)
	total := h.scalarCount(`
		SELECT COUNT(DISTINCT i.id) FROM issues i
		LEFT JOIN issue_assignees ia ON ia.issue = i.id AND ia.deleted_at = ''
		WHERE i.workspace = {:ws} AND i.deleted_at = '' AND (i.created_by = {:u} OR ia.assignee = {:u})`, params)
	completed := h.scalarCount(`
		SELECT COUNT(DISTINCT i.id) FROM issues i
		LEFT JOIN issue_assignees ia ON ia.issue = i.id AND ia.deleted_at = ''
		LEFT JOIN states s ON s.id = i.state
		WHERE i.workspace = {:ws} AND i.deleted_at = '' AND (i.created_by = {:u} OR ia.assignee = {:u})
			AND s."group" = 'completed'`, params)
	pending := total - completed
	if pending < 0 {
		pending = 0
	}
	return map[string]any{
		"assigned_issues_count":  assigned,
		"completed_issues_count": completed,
		"created_issues_count":   created,
		"pending_issues_count":   pending,
	}
}

// issueListWidget → TAssignedIssuesWidgetResponse / TCreatedIssuesWidgetResponse
// ({issues, count}). `assigned` selects the assignee relation, otherwise the
// creator.
func (h *Handler) issueListWidget(userID, workspaceID string, assigned bool) map[string]any {
	params := dbx.Params{"ws": workspaceID, "u": userID}
	var query string
	if assigned {
		query = `
			SELECT DISTINCT i.id AS id FROM issues i
			JOIN issue_assignees ia ON ia.issue = i.id AND ia.deleted_at = ''
			WHERE i.workspace = {:ws} AND i.deleted_at = '' AND ia.assignee = {:u}
			ORDER BY i.created_at DESC LIMIT 50`
	} else {
		query = `
			SELECT id AS id FROM issues
			WHERE workspace = {:ws} AND deleted_at = '' AND created_by = {:u}
			ORDER BY created_at DESC LIMIT 50`
	}
	records := h.recordsByQuery("issues", query, params)
	issues := make([]any, 0, len(records))
	for _, r := range records {
		item := serializers.Issue(h.App, r)
		if item != nil {
			item["issue_relation"] = []any{}
			issues = append(issues, item)
		}
	}
	return map[string]any{"issues": issues, "count": len(issues)}
}

// issuesByStateGroups → TIssuesByStateGroupsWidgetResponse[] ({count, state}).
// Every state group is emitted (including empty ones) so the chart has a stable
// axis.
func (h *Handler) issuesByStateGroups(userID, workspaceID string) []any {
	counts := map[string]int{}
	for _, row := range h.rows(`
		SELECT s."group" AS grp, COUNT(DISTINCT i.id) AS n FROM issues i
		LEFT JOIN issue_assignees ia ON ia.issue = i.id AND ia.deleted_at = ''
		LEFT JOIN states s ON s.id = i.state
		WHERE i.workspace = {:ws} AND i.deleted_at = '' AND (i.created_by = {:u} OR ia.assignee = {:u})
		GROUP BY s."group"`, dbx.Params{"ws": workspaceID, "u": userID}) {
		if k := row["grp"].String; k != "" {
			counts[k] = atoiSafe(row["n"].String)
		}
	}
	out := make([]any, 0, len(stateGroups))
	for _, group := range stateGroups {
		out = append(out, map[string]any{"count": counts[group], "state": group})
	}
	return out
}

// issuesByPriority → TIssuesByPriorityWidgetResponse[] ({count, priority}).
func (h *Handler) issuesByPriority(userID, workspaceID string) []any {
	counts := map[string]int{}
	for _, row := range h.rows(`
		SELECT COALESCE(NULLIF(i.priority, ''), 'none') AS pri, COUNT(DISTINCT i.id) AS n FROM issues i
		LEFT JOIN issue_assignees ia ON ia.issue = i.id AND ia.deleted_at = ''
		WHERE i.workspace = {:ws} AND i.deleted_at = '' AND (i.created_by = {:u} OR ia.assignee = {:u})
		GROUP BY pri`, dbx.Params{"ws": workspaceID, "u": userID}) {
		if k := row["pri"].String; k != "" {
			counts[k] = atoiSafe(row["n"].String)
		}
	}
	out := make([]any, 0, len(priorities))
	for _, p := range priorities {
		out = append(out, map[string]any{"count": counts[p], "priority": p})
	}
	return out
}

// recentActivity → TRecentActivityWidgetResponse[] (IIssueActivity[]).
func (h *Handler) recentActivity(workspaceID string) []any {
	records, err := h.App.FindRecordsByFilter(
		"issue_activities",
		"workspace = {:ws} && deleted_at = ''",
		"-created_at", 20, 0,
		dbx.Params{"ws": workspaceID},
	)
	out := make([]any, 0, len(records))
	if err != nil {
		return out
	}
	for _, r := range records {
		out = append(out, serializers.IssueActivity(h.App, r))
	}
	return out
}

// recentProjects → TRecentProjectsWidgetResponse (string[] of project UUIDs).
func (h *Handler) recentProjects(workspaceID string) []any {
	records, err := h.App.FindRecordsByFilter(
		"projects",
		"workspace = {:ws} && deleted_at = ''",
		"-created_at", 20, 0,
		dbx.Params{"ws": workspaceID},
	)
	out := make([]any, 0)
	if err != nil {
		return out
	}
	for _, r := range records {
		out = append(out, serializers.UUID(r))
	}
	return out
}

// recentCollaborators → TRecentCollaboratorsWidgetResponse[]
// ({active_issue_count, user_id}).
func (h *Handler) recentCollaborators(workspaceID string) []any {
	rows := h.rows(`
		SELECT u.uuid AS user_id, COUNT(DISTINCT i.id) AS n
		FROM workspace_members wm
		JOIN users u ON u.id = wm.member
		LEFT JOIN issue_assignees ia ON ia.assignee = wm.member AND ia.deleted_at = ''
		LEFT JOIN issues i ON i.id = ia.issue AND i.workspace = {:ws} AND i.deleted_at = ''
			AND (i.completed_at IS NULL OR i.completed_at = '')
		WHERE wm.workspace = {:ws} AND wm.is_active = true AND wm.deleted_at = ''
		GROUP BY u.id
		ORDER BY n DESC
		LIMIT 20`, dbx.Params{"ws": workspaceID})
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		userID := row["user_id"].String
		if userID == "" {
			continue
		}
		out = append(out, map[string]any{
			"active_issue_count": atoiSafe(row["n"].String),
			"user_id":            userID,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// query helpers
// ---------------------------------------------------------------------------

// scalarCount runs a single-value COUNT query.
func (h *Handler) scalarCount(query string, params dbx.Params) int {
	var n int
	if err := h.App.DB().NewQuery(query).Bind(params).Row(&n); err != nil {
		return 0
	}
	return n
}

// recordsByQuery resolves a single-column `id` query into records.
func (h *Handler) recordsByQuery(collection, query string, params dbx.Params) []*core.Record {
	rows := h.rows(query, params)
	out := make([]*core.Record, 0, len(rows))
	for _, row := range rows {
		id := row["id"].String
		if id == "" {
			continue
		}
		if record, err := h.App.FindRecordById(collection, id); err == nil && record != nil {
			out = append(out, record)
		}
	}
	return out
}

// rows runs a query and returns the rows as dbx.NullStringMaps (the only
// map-shaped scan target dbx supports).
func (h *Handler) rows(query string, params dbx.Params) []dbx.NullStringMap {
	var out []dbx.NullStringMap
	if err := h.App.DB().NewQuery(query).Bind(params).All(&out); err != nil {
		return nil
	}
	return out
}

// atoiSafe parses a decimal string, returning 0 on failure.
func atoiSafe(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

var stateGroups = []string{"backlog", "unstarted", "started", "completed", "cancelled", "triage"}
var priorities = []string{"urgent", "high", "medium", "low", "none"}
