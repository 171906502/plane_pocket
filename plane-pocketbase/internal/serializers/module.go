package serializers

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// Module mirrors Django's ModuleSerializer (plane/app/serializers/module.py).
// Plane's `IModule` frontend type is the contract for these keys; note that
// `start_date`/`target_date` are DateFields (date-only on the wire) while
// `created_at`/`updated_at` are full timestamps.
//
// Estimate-point aggregates (`*_estimate_points`) are summed from
// `issue.estimate_point.value` grouped by the issue's state group — see
// BridgeEstimatePoints for the contract provenance. Only points whose estimate
// is of type "points" count, so a project using a "categories" estimate reports
// zeros here (as upstream does).
//
// When `detail` is true the ModuleDetailSerializer extras are added:
// `link_module`, `sub_issues` and the `distribution`/`estimate_distribution`
// objects.
func Module(app core.App, r *core.Record, isFavorite, detail bool) map[string]any {
	if r == nil {
		return nil
	}
	counts := bridgeIssueCounts(app, "module_issues", "module", r.Id)
	estimates := BridgeEstimatePoints(app, "module_issues", "module", r.Id)
	status := r.GetString("status")
	if status == "" {
		status = "planned"
	}
	out := map[string]any{
		"id":                        UUID(r),
		"workspace_id":              RelationUUID(app, r, "workspace"),
		"project_id":                RelationUUID(app, r, "project"),
		"name":                      Text(r, "name"),
		"description":               Text(r, "description"),
		"description_text":          JSONValue(r, "description_text"),
		"description_html":          JSONValue(r, "description_html"),
		"start_date":                DateOnly(r, "start_date"),
		"target_date":               DateOnly(r, "target_date"),
		"status":                    status,
		"lead_id":                   RelationUUID(app, r, "lead"),
		"member_ids":                moduleMemberIDs(app, r.Id),
		"view_props":                JSONValue(r, "view_props"),
		"sort_order":                sortOrderOrDefault(r),
		"external_source":           NullableText(r, "external_source"),
		"external_id":               NullableText(r, "external_id"),
		"logo_props":                JSONValue(r, "logo_props"),
		"total_estimate_points":     estimates.Total,
		"completed_estimate_points": estimates.Completed,
		"backlog_estimate_points":   estimates.Backlog,
		"unstarted_estimate_points": estimates.Unstarted,
		"started_estimate_points":   estimates.Started,
		"cancelled_estimate_points": estimates.Cancelled,
		"is_favorite":               isFavorite,
		"total_issues":              counts.Total,
		"cancelled_issues":          counts.Cancelled,
		"completed_issues":          counts.Completed,
		"started_issues":            counts.Started,
		"unstarted_issues":          counts.Unstarted,
		"backlog_issues":            counts.Backlog,
		"created_at":                DateTime(r, "created_at"),
		"updated_at":                DateTime(r, "updated_at"),
		"created_by":                RelationUUID(app, r, "created_by"),
		"updated_by":                RelationUUID(app, r, "updated_by"),
		"archived_at":               NullableDateTime(r, "archived_at"),
	}
	if detail {
		links := moduleLinks(app, r.Id)
		distribution := map[string]any{
			"assignees":        []any{},
			"labels":           []any{},
			"completion_chart": map[string]any{},
		}
		out["link_module"] = links
		out["sub_issues"] = moduleSubIssues(app, r.Id)
		out["distribution"] = distribution
		out["estimate_distribution"] = map[string]any{}
	}
	return out
}

// ModuleLink mirrors Django's ModuleLinkSerializer. `ModuleLink` on the frontend
// only reads `title`/`url`/`id`, but the full DRF shape is emitted so nothing
// downstream is missing.
func ModuleLink(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":           UUID(r),
		"title":        Text(r, "title"),
		"url":          Text(r, "url"),
		"metadata":     JSONValue(r, "metadata"),
		"module_id":    RelationUUID(app, r, "module"),
		"project_id":   RelationUUID(app, r, "project"),
		"workspace_id": RelationUUID(app, r, "workspace"),
		"created_at":   DateTime(r, "created_at"),
		"updated_at":   DateTime(r, "updated_at"),
		"created_by":   RelationUUID(app, r, "created_by"),
		"updated_by":   RelationUUID(app, r, "updated_by"),
	}
}

// ModuleUserProperties mirrors ModuleUserPropertiesSerializer
// (`fields = "__all__"`), emitting both the DRF FK names and `_id` aliases.
func ModuleUserProperties(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	module := RelationUUID(app, r, "module")
	user := RelationUUID(app, r, "user")
	project := RelationUUID(app, r, "project")
	workspace := RelationUUID(app, r, "workspace")
	return map[string]any{
		"id":                 UUID(r),
		"module":             module,
		"module_id":          module,
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

// moduleMemberIDs resolves the module's members through `module_members`.
func moduleMemberIDs(app core.App, moduleID string) []string {
	return queryUUIDColumn(app, `
		SELECT DISTINCT u.uuid AS uuid
		FROM module_members mm
		JOIN users u ON u.id = mm.member
		WHERE mm.module = {:id} AND mm.deleted_at = ''`, moduleID)
}

// moduleLinks returns the module's links, newest first (ModuleLinkViewSet).
func moduleLinks(app core.App, moduleID string) []any {
	rows, err := app.FindRecordsByFilter(
		"module_links",
		"module = {:module} && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"module": moduleID},
	)
	out := []any{}
	if err != nil {
		return out
	}
	for _, row := range rows {
		out = append(out, ModuleLink(app, row))
	}
	return out
}

// moduleSubIssues counts the module's sub-issues (issues that have a parent).
func moduleSubIssues(app core.App, moduleID string) int {
	return queryCount(app, `
		SELECT COUNT(DISTINCT i.id)
		FROM module_issues mi
		JOIN issues i ON i.id = mi.issue
		WHERE mi.module = {:id} AND mi.deleted_at = ''
			AND i.parent != ''
			AND i.deleted_at = '' AND i.archived_at = '' AND i.is_draft = 0`, moduleID)
}
