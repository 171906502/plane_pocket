package serializers

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// Cycle mirrors Django's CycleSerializer (plane/app/serializers/cycle.py) plus
// the extra keys the viewset's `.values(...)` projections emit (`workspace_id`,
// `project_id`, `created_by`, `version`, `assignee_ids`, `sub_issues`).
//
// `owned_by_id` — not `owned_by` — is the wire field name: Plane's frontend
// `ICycle` type reads `owned_by_id`, and the Django `.values()` projections
// select `owned_by_id`.
//
// `status` is computed, never stored (CURRENT/UPCOMING/COMPLETED/DRAFT), and
// `is_favorite` is per-caller, so it is passed in rather than derived here.
func Cycle(app core.App, r *core.Record, isFavorite bool) map[string]any {
	if r == nil {
		return nil
	}
	counts := bridgeIssueCounts(app, "cycle_issues", "cycle", r.Id)
	return map[string]any{
		"id":                UUID(r),
		"workspace_id":      RelationUUID(app, r, "workspace"),
		"project_id":        RelationUUID(app, r, "project"),
		"name":              Text(r, "name"),
		"description":       Text(r, "description"),
		"start_date":        NullableDateTime(r, "start_date"),
		"end_date":          NullableDateTime(r, "end_date"),
		"owned_by_id":       RelationUUID(app, r, "owned_by"),
		"view_props":        JSONValue(r, "view_props"),
		"sort_order":        sortOrderOrDefault(r),
		"external_source":   NullableText(r, "external_source"),
		"external_id":       NullableText(r, "external_id"),
		"progress_snapshot": JSONValue(r, "progress_snapshot"),
		"logo_props":        JSONValue(r, "logo_props"),
		"is_favorite":       isFavorite,
		"total_issues":      counts.Total,
		"cancelled_issues":  counts.Cancelled,
		"completed_issues":  counts.Completed,
		"started_issues":    counts.Started,
		"unstarted_issues":  counts.Unstarted,
		"backlog_issues":    counts.Backlog,
		"assignee_ids":      cycleAssigneeIDs(app, r.Id),
		"status":            cycleStatus(r),
		"sub_issues":        cycleSubIssues(app, r.Id),
		"version":           versionOrDefault(r),
		"created_at":        DateTime(r, "created_at"),
		"updated_at":        DateTime(r, "updated_at"),
		"created_by":        RelationUUID(app, r, "created_by"),
		"updated_by":        RelationUUID(app, r, "updated_by"),
		"archived_at":       NullableDateTime(r, "archived_at"),
	}
}

// CycleUserProperties mirrors CycleUserPropertiesSerializer (`fields = "__all__"`).
// DRF serializes the FK names verbatim (`cycle`, `user`, `project`, `workspace`),
// so both spellings are emitted: the DRF ones plus the `_id` aliases used
// elsewhere in this compatibility layer.
func CycleUserProperties(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	cycle := RelationUUID(app, r, "cycle")
	user := RelationUUID(app, r, "user")
	project := RelationUUID(app, r, "project")
	workspace := RelationUUID(app, r, "workspace")
	return map[string]any{
		"id":                 UUID(r),
		"cycle":              cycle,
		"cycle_id":           cycle,
		"user":               user,
		"user_id":            user,
		"project":            project,
		"project_id":         project,
		"workspace":          workspace,
		"workspace_id":       workspace,
		"filters":            JSONValue(r, "filters"),
		"display_filters":    JSONValue(r, "display_filters"),
		"display_properties": JSONValue(r, "display_properties"),
		"rich_filters":       JSONValue(r, "rich_filters"),
		"created_at":         DateTime(r, "created_at"),
		"updated_at":         DateTime(r, "updated_at"),
		"created_by":         RelationUUID(app, r, "created_by"),
		"updated_by":         RelationUUID(app, r, "updated_by"),
	}
}

// issueCounts is the state-group breakdown shared by cycles and modules.
type issueCounts struct {
	Total     int
	Completed int
	Cancelled int
	Started   int
	Unstarted int
	Backlog   int
}

// bridgeIssueCounts counts the issues linked to a cycle/module through its
// bridge table, broken down by the issue's state group.
//
// `bridge`/`fk` are internal literals (never user input): the bridge table name
// and the column that points at the owning cycle/module. The predicate mirrors
// Django's annotated Count filters exactly — archived, draft and soft-deleted
// issues, and soft-deleted bridge rows, are all excluded.
func bridgeIssueCounts(app core.App, bridge, fk, id string) issueCounts {
	var row struct {
		Total     int `db:"total"`
		Completed int `db:"completed"`
		Cancelled int `db:"cancelled"`
		Started   int `db:"started"`
		Unstarted int `db:"unstarted"`
		Backlog   int `db:"backlog"`
	}
	query := `
		SELECT
			COUNT(DISTINCT i.id) AS total,
			COALESCE(SUM(CASE WHEN s."group" = 'completed' THEN 1 ELSE 0 END), 0) AS completed,
			COALESCE(SUM(CASE WHEN s."group" = 'cancelled' THEN 1 ELSE 0 END), 0) AS cancelled,
			COALESCE(SUM(CASE WHEN s."group" = 'started' THEN 1 ELSE 0 END), 0) AS started,
			COALESCE(SUM(CASE WHEN s."group" = 'unstarted' THEN 1 ELSE 0 END), 0) AS unstarted,
			COALESCE(SUM(CASE WHEN s."group" = 'backlog' THEN 1 ELSE 0 END), 0) AS backlog
		FROM ` + bridge + ` b
		JOIN issues i ON i.id = b.issue
		LEFT JOIN states s ON s.id = i.state
		WHERE b.` + fk + ` = {:id}
			AND b.deleted_at = ''
			AND i.deleted_at = ''
			AND i.archived_at = ''
			AND i.is_draft = 0`
	if err := app.DB().NewQuery(query).Bind(dbx.Params{"id": id}).One(&row); err != nil {
		return issueCounts{}
	}
	return issueCounts{
		Total:     row.Total,
		Completed: row.Completed,
		Cancelled: row.Cancelled,
		Started:   row.Started,
		Unstarted: row.Unstarted,
		Backlog:   row.Backlog,
	}
}

// EstimatePoints is the state-group breakdown of a cycle/module's estimate
// points, mirroring Django's annotated `*_estimate_points` fields
// (CycleProgressEndpoint, apps/api/plane/app/views/cycle/base.py:664-711;
// ModuleViewSet, apps/api/plane/app/views/module/base.py:145-277).
type EstimatePoints struct {
	Backlog   float64
	Unstarted float64
	Started   float64
	Cancelled float64
	Completed float64
	Total     float64
}

// BridgeEstimatePoints sums `estimate_point.value` (cast to float, as Django's
// `Cast("estimate_point__value", FloatField())` does) over the issues linked to
// a cycle/module through its bridge table, broken down by the issue's state
// group. It is the real replacement for the hardcoded zeros the L-2 task card
// records (docs/11-TASKLIST.md §3 L-2).
//
// Three contract details, all taken verbatim from the upstream queries:
//
//   - Only points whose estimate is of type "points" count
//     (`estimate_point__estimate__type="points"`): a "categories" estimate
//     (t-shirt sizes) contributes nothing to the aggregates, upstream and here.
//   - The issue population is exactly bridgeIssueCounts' — soft-deleted bridge
//     rows and soft-deleted/archived/draft issues are excluded — so the point
//     sums always cover the same issues the *_issues counts report.
//   - `value` is a text column (Django CharField); CAST yields 0.0 for
//     non-numeric values, matching SQLite's CAST semantics.
func BridgeEstimatePoints(app core.App, bridge, fk, id string) EstimatePoints {
	var row struct {
		Backlog   float64 `db:"backlog"`
		Unstarted float64 `db:"unstarted"`
		Started   float64 `db:"started"`
		Cancelled float64 `db:"cancelled"`
		Completed float64 `db:"completed"`
		Total     float64 `db:"total"`
	}
	query := `
		SELECT
			COALESCE(SUM(CASE WHEN s."group" = 'backlog' THEN CAST(ep.value AS REAL) ELSE 0 END), 0) AS backlog,
			COALESCE(SUM(CASE WHEN s."group" = 'unstarted' THEN CAST(ep.value AS REAL) ELSE 0 END), 0) AS unstarted,
			COALESCE(SUM(CASE WHEN s."group" = 'started' THEN CAST(ep.value AS REAL) ELSE 0 END), 0) AS started,
			COALESCE(SUM(CASE WHEN s."group" = 'cancelled' THEN CAST(ep.value AS REAL) ELSE 0 END), 0) AS cancelled,
			COALESCE(SUM(CASE WHEN s."group" = 'completed' THEN CAST(ep.value AS REAL) ELSE 0 END), 0) AS completed,
			COALESCE(SUM(CAST(ep.value AS REAL)), 0) AS total
		FROM ` + bridge + ` b
		JOIN issues i ON i.id = b.issue
		LEFT JOIN states s ON s.id = i.state
		JOIN estimate_points ep ON ep.id = i.estimate_point
		JOIN estimates e ON e.id = ep.estimate AND e.type = 'points'
		WHERE b.` + fk + ` = {:id}
			AND b.deleted_at = ''
			AND i.deleted_at = ''
			AND i.archived_at = ''
			AND i.is_draft = 0`
	if err := app.DB().NewQuery(query).Bind(dbx.Params{"id": id}).One(&row); err != nil {
		return EstimatePoints{}
	}
	return EstimatePoints{
		Backlog:   row.Backlog,
		Unstarted: row.Unstarted,
		Started:   row.Started,
		Cancelled: row.Cancelled,
		Completed: row.Completed,
		Total:     row.Total,
	}
}

// CycleStatus exposes the computed cycle status (CURRENT/UPCOMING/COMPLETED/
// DRAFT) so handlers can filter on it (e.g. `?cycle_view=current`).
func CycleStatus(r *core.Record) string { return cycleStatus(r) }

// cycleStatus reproduces CycleViewSet's `status` Case expression.
func cycleStatus(r *core.Record) string {
	now := Now()
	start := r.GetDateTime("start_date").Time()
	end := r.GetDateTime("end_date").Time()
	hasStart := !start.IsZero()
	hasEnd := !end.IsZero()
	switch {
	case hasStart && hasEnd && !start.After(now) && !end.Before(now):
		return "CURRENT"
	case hasStart && start.After(now):
		return "UPCOMING"
	case hasEnd && end.Before(now):
		return "COMPLETED"
	default:
		return "DRAFT"
	}
}

// cycleAssigneeIDs resolves the distinct assignees of every issue in the cycle,
// matching the ArrayAgg over `issue_cycle__issue__assignees`.
func cycleAssigneeIDs(app core.App, cycleID string) []string {
	return queryUUIDColumn(app, `
		SELECT DISTINCT u.uuid AS uuid
		FROM cycle_issues ci
		JOIN issue_assignees ia ON ia.issue = ci.issue AND ia.deleted_at = ''
		JOIN users u ON u.id = ia.assignee
		WHERE ci.cycle = {:id} AND ci.deleted_at = ''`, cycleID)
}

// cycleSubIssues counts the cycle's sub-issues (issues that have a parent).
func cycleSubIssues(app core.App, cycleID string) int {
	return queryCount(app, `
		SELECT COUNT(DISTINCT i.id)
		FROM cycle_issues ci
		JOIN issues i ON i.id = ci.issue
		WHERE ci.cycle = {:id} AND ci.deleted_at = ''
			AND i.parent != ''
			AND i.deleted_at = '' AND i.archived_at = '' AND i.is_draft = 0`, cycleID)
}

// queryUUIDColumn runs a single-column query aliased `uuid` and returns the
// non-empty values.
func queryUUIDColumn(app core.App, query, id string) []string {
	var rows []struct {
		UUID string `db:"uuid"`
	}
	out := []string{}
	if err := app.DB().NewQuery(query).Bind(dbx.Params{"id": id}).All(&rows); err != nil {
		return out
	}
	for _, row := range rows {
		if row.UUID != "" {
			out = append(out, row.UUID)
		}
	}
	return out
}

// queryCount runs a scalar COUNT query bound to `id`.
func queryCount(app core.App, query, id string) int {
	var count int
	if err := app.DB().NewQuery(query).Bind(dbx.Params{"id": id}).Row(&count); err != nil {
		return 0
	}
	return count
}

// sortOrderOrDefault renders `sort_order`, defaulting to Plane's 65535 when the
// column is unset.
func sortOrderOrDefault(r *core.Record) any {
	if r.Get("sort_order") == nil {
		return float64(65535)
	}
	return r.GetFloat("sort_order")
}

// versionOrDefault renders `version`, defaulting to Plane's 1.
func versionOrDefault(r *core.Record) int {
	if r.Get("version") == nil {
		return 1
	}
	return r.GetInt("version")
}
