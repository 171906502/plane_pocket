package serializers

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// Project mirrors Django's ProjectListSerializer (fields = "__all__" plus the
// computed `members`, `member_role`, `cover_image_url`, `inbox_view`,
// `sort_order`, `is_favorite`, `anchor` and `next_work_item_sequence`).
//
// Aggregates that Plane reads from collections outside this vertical slice
// (`user_favorites`, `project_user_properties`, `project_deploy_boards`) are
// projected as their empty defaults so the shape stays stable for the frontend.
func Project(app core.App, r *core.Record, currentUser *core.Record) map[string]any {
	if r == nil {
		return nil
	}

	workspaceSlug := ""
	if ws, _ := app.FindRecordById("workspaces", r.GetString("workspace")); ws != nil {
		workspaceSlug = ws.GetString("slug")
	}

	members := make([]string, 0)
	var memberRole any
	memberRecords, _ := app.FindRecordsByFilter(
		"project_members",
		"project = {:project} && is_active = true",
		"", 0, 0,
		dbx.Params{"project": r.Id},
	)
	for _, m := range memberRecords {
		user, _ := app.FindRecordById("users", m.GetString("member"))
		if user == nil || user.GetBool("is_bot") {
			continue
		}
		members = append(members, UUID(user))
		if currentUser != nil && m.GetString("member") == currentUser.Id {
			memberRole = m.GetInt("role")
		}
	}

	cover := NullableText(r, "cover_image")
	intakeView := r.GetBool("intake_view")

	return map[string]any{
		"id":                       UUID(r),
		"name":                     Text(r, "name"),
		"description":              Text(r, "description"),
		"description_text":         JSONValue(r, "description_text"),
		"description_html":         JSONValue(r, "description_html"),
		"network":                  r.GetInt("network"),
		"workspace":                RelationUUID(app, r, "workspace"),
		"workspace_slug":           workspaceSlug,
		"identifier":               Text(r, "identifier"),
		"default_assignee":         RelationUUID(app, r, "default_assignee"),
		"project_lead":             RelationUUID(app, r, "project_lead"),
		"emoji":                    NullableText(r, "emoji"),
		"icon_prop":                JSONValue(r, "icon_prop"),
		"module_view":              r.GetBool("module_view"),
		"cycle_view":               r.GetBool("cycle_view"),
		"issue_views_view":         r.GetBool("issue_views_view"),
		"page_view":                r.GetBool("page_view"),
		"intake_view":              intakeView,
		"inbox_view":               intakeView,
		"is_time_tracking_enabled": r.GetBool("is_time_tracking_enabled"),
		"is_issue_type_enabled":    r.GetBool("is_issue_type_enabled"),
		"guest_view_all_features":  r.GetBool("guest_view_all_features"),
		"cover_image":              cover,
		"cover_image_url":          cover,
		// L-11: Django's ProjectSerializer is `fields = "__all__"`
		// (apps/api/plane/app/serializers/project.py:36), so `estimate` is the
		// flat FK UUID — the same decision L-2 took for issues.estimate_point.
		// The frontend's `areEstimateEnabledByProjectId` gates the issue
		// sidebar's Estimate row on this field (project-estimate.store.ts:139).
		"estimate":                RelationUUID(app, r, "estimate"),
		"archive_in":              r.GetInt("archive_in"),
		"close_in":                r.GetInt("close_in"),
		"logo_props":              JSONValue(r, "logo_props"),
		"default_state":           RelationUUID(app, r, "default_state"),
		"archived_at":             NullableDateTime(r, "archived_at"),
		"timezone":                NullableText(r, "timezone"),
		"external_source":         NullableText(r, "external_source"),
		"external_id":             NullableText(r, "external_id"),
		"created_at":              DateTime(r, "created_at"),
		"updated_at":              DateTime(r, "updated_at"),
		"created_by":              RelationUUID(app, r, "created_by"),
		"updated_by":              RelationUUID(app, r, "updated_by"),
		"deleted_at":              NullableDateTime(r, "deleted_at"),
		"members":                 members,
		"member_role":             memberRole,
		"sort_order":              nil,
		"is_favorite":             false,
		"anchor":                  nil,
		"next_work_item_sequence": nextWorkItemSequence(app, r),
	}
}

// PartialProject mirrors the exact projection of ProjectViewSet.list
// (`apps/api/plane/app/views/project/base.py`), which is what
// GET /api/workspaces/{slug}/projects/ returns:
//
//	Project.objects…values("id", "name", "identifier", "sort_order",
//	    "logo_props", "member_role", "intake_count", "archived_at",
//	    "workspace", "cycle_view", "issue_views_view", "module_view",
//	    "page_view", "inbox_view", "guest_view_all_features", "project_lead",
//	    "network", "created_at", "updated_at", "created_by", "updated_by")
//
// The frontend stores these in `projectMap` and keys off `workspace` (the
// workspace UUID) plus `archived_at` when computing `workspaceProjectIds`, so
// both must be present and correctly typed.
func PartialProject(app core.App, r *core.Record, currentUser *core.Record) map[string]any {
	if r == nil {
		return nil
	}

	var memberRole any
	if currentUser != nil {
		members, _ := app.FindRecordsByFilter(
			"project_members",
			"project = {:project} && member = {:member} && is_active = true && deleted_at = ''",
			"", 1, 0,
			dbx.Params{"project": r.Id, "member": currentUser.Id},
		)
		if len(members) > 0 {
			memberRole = members[0].GetInt("role")
		}
	}

	return map[string]any{
		"id":                      UUID(r),
		"name":                    Text(r, "name"),
		"identifier":              Text(r, "identifier"),
		"sort_order":              nil,
		"logo_props":              JSONValue(r, "logo_props"),
		"member_role":             memberRole,
		"intake_count":            pendingIntakeCount(app, r),
		"archived_at":             NullableDateTime(r, "archived_at"),
		"workspace":               RelationUUID(app, r, "workspace"),
		"cycle_view":              r.GetBool("cycle_view"),
		"issue_views_view":        r.GetBool("issue_views_view"),
		"module_view":             r.GetBool("module_view"),
		"page_view":               r.GetBool("page_view"),
		"inbox_view":              r.GetBool("intake_view"),
		"guest_view_all_features": r.GetBool("guest_view_all_features"),
		"project_lead":            RelationUUID(app, r, "project_lead"),
		"network":                 r.GetInt("network"),
		"created_at":              DateTime(r, "created_at"),
		"updated_at":              DateTime(r, "updated_at"),
		"created_by":              RelationUUID(app, r, "created_by"),
		"updated_by":              RelationUUID(app, r, "updated_by"),
	}
}

// pendingIntakeCount mirrors the `intake_count` annotation of
// ProjectViewSet.list: the number of pending, non-deleted intake issues.
func pendingIntakeCount(app core.App, project *core.Record) int {
	var count int
	err := app.DB().
		NewQuery("SELECT COUNT(*) FROM intake_issues WHERE project = {:project} AND status = 0 AND deleted_at = ''").
		Bind(dbx.Params{"project": project.Id}).
		Row(&count)
	if err != nil {
		return 0
	}
	return count
}

// DefaultProps mirrors `get_default_props()` in plane/db/models/project.py,
// which seeds ProjectMember.view_props / .default_props. PocketBase has no
// field-level defaults, so the value is projected at serialization time.
func DefaultProps() map[string]any {
	return map[string]any{
		"filters": map[string]any{
			"priority":    nil,
			"state":       nil,
			"state_group": nil,
			"assignees":   nil,
			"created_by":  nil,
			"labels":      nil,
			"start_date":  nil,
			"target_date": nil,
			"subscriber":  nil,
		},
		"display_filters": map[string]any{
			"group_by":            nil,
			"order_by":            "-created_at",
			"type":                nil,
			"sub_issue":           true,
			"show_empty_groups":   true,
			"layout":              "list",
			"calendar_date_range": "",
		},
	}
}

// DefaultPreferences mirrors `get_default_preferences()` in
// plane/db/models/project.py.
func DefaultPreferences() map[string]any {
	return map[string]any{
		"pages":      map[string]any{"block_display": true},
		"navigation": map[string]any{"default_tab": "work_items", "hide_in_more_menu": []any{}},
	}
}

// DefaultIssueFilters mirrors `get_default_filters()` in plane/db/models/issue.py.
func DefaultIssueFilters() map[string]any {
	return map[string]any{
		"priority":    nil,
		"state":       nil,
		"state_group": nil,
		"assignees":   nil,
		"created_by":  nil,
		"labels":      nil,
		"start_date":  nil,
		"target_date": nil,
		"subscriber":  nil,
	}
}

// DefaultDisplayFilters mirrors `get_default_display_filters()` in
// plane/db/models/issue.py.
func DefaultDisplayFilters() map[string]any {
	return map[string]any{
		"group_by":            nil,
		"order_by":            "-created_at",
		"type":                nil,
		"sub_issue":           true,
		"show_empty_groups":   true,
		"layout":              "list",
		"calendar_date_range": "",
	}
}

// DefaultDisplayProperties mirrors `get_default_display_properties()` in
// plane/db/models/issue.py.
func DefaultDisplayProperties() map[string]any {
	return map[string]any{
		"assignee":         true,
		"attachment_count": true,
		"created_on":       true,
		"due_date":         true,
		"estimate":         true,
		"key":              true,
		"labels":           true,
		"link":             true,
		"priority":         true,
		"start_date":       true,
		"state":            true,
		"sub_issue_count":  true,
		"updated_on":       true,
	}
}

// ProjectMember mirrors Django's ProjectMemberSerializer: the ProjectMember
// model with `fields = "__all__"`, plus `workspace` / `project` / `member`
// nested as their Lite serializers.
//
// `GET …/projects/{id}/project-members/me/` is what populates
// `workspaceProjectsPermissions[slug][projectId]` on the frontend; the project
// layout denies access ("Project not found") whenever `role` is missing.
func ProjectMember(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	workspace, _ := app.FindRecordById("workspaces", r.GetString("workspace"))
	project, _ := app.FindRecordById("projects", r.GetString("project"))
	member, _ := app.FindRecordById("users", r.GetString("member"))

	viewProps := JSONOrDefault(r, "view_props", DefaultProps())
	defaultProps := JSONOrDefault(r, "default_props", DefaultProps())
	preferences := JSONOrDefault(r, "preferences", DefaultPreferences())
	sortOrder := r.GetFloat("sort_order")
	if sortOrder == 0 {
		sortOrder = 65535
	}

	return map[string]any{
		"id":            UUID(r),
		"workspace":     WorkspaceLite(workspace),
		"project":       ProjectLite(project),
		"member":        UserLite(app, member),
		"comment":       NullableText(r, "comment"),
		"role":          r.GetInt("role"),
		"view_props":    viewProps,
		"default_props": defaultProps,
		"preferences":   preferences,
		"sort_order":    sortOrder,
		"is_active":     r.GetBool("is_active"),
		"created_at":    DateTime(r, "created_at"),
		"updated_at":    DateTime(r, "updated_at"),
		"created_by":    RelationUUID(app, r, "created_by"),
		"updated_by":    RelationUUID(app, r, "updated_by"),
		"deleted_at":    NullableDateTime(r, "deleted_at"),
	}
}

// ProjectMemberAdmin mirrors Django's ProjectMemberAdminSerializer: identical
// to ProjectMemberSerializer except that the nested member uses the admin-lite
// shape, which also carries the email. `…/projects/{id}/members/` is an
// admin-facing list, so it uses this variant.
func ProjectMemberAdmin(app core.App, r *core.Record) map[string]any {
	out := ProjectMember(app, r)
	if out == nil {
		return nil
	}
	member, _ := app.FindRecordById("users", r.GetString("member"))
	out["member"] = UserAdminLite(app, member)
	return out
}

// ProjectUserProperty mirrors Django's ProjectUserPropertySerializer
// (ProjectUserProperty model, `fields = "__all__"`). Served by
// GET/PATCH …/projects/{id}/user-properties/ — the project view filters,
// display filters and display properties.
func ProjectUserProperty(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	filters := JSONOrDefault(r, "filters", DefaultIssueFilters())
	displayFilters := JSONOrDefault(r, "display_filters", DefaultDisplayFilters())
	displayProperties := JSONOrDefault(r, "display_properties", DefaultDisplayProperties())
	preferences := JSONOrDefault(r, "preferences", DefaultPreferences())
	richFilters := JSONOrDefault(r, "rich_filters", map[string]any{})
	sortOrder := r.GetFloat("sort_order")
	if sortOrder == 0 {
		sortOrder = 65535
	}

	return map[string]any{
		"id":                 UUID(r),
		"user":               RelationUUID(app, r, "user"),
		"workspace":          RelationUUID(app, r, "workspace"),
		"project":            RelationUUID(app, r, "project"),
		"filters":            filters,
		"display_filters":    displayFilters,
		"display_properties": displayProperties,
		"rich_filters":       richFilters,
		"preferences":        preferences,
		"sort_order":         sortOrder,
		"created_at":         DateTime(r, "created_at"),
		"updated_at":         DateTime(r, "updated_at"),
		"created_by":         RelationUUID(app, r, "created_by"),
		"updated_by":         RelationUUID(app, r, "updated_by"),
		"deleted_at":         NullableDateTime(r, "deleted_at"),
	}
}

// Estimate mirrors Django's EstimateReadSerializer: the Estimate model with
// `fields = "__all__"` plus its `points` (EstimatePointSerializer).
func Estimate(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	points := make([]any, 0)
	pointRecords, _ := app.FindRecordsByFilter(
		"estimate_points", "estimate = {:estimate}", "value", 0, 0,
		dbx.Params{"estimate": r.Id},
	)
	for _, point := range pointRecords {
		points = append(points, EstimatePoint(app, point))
	}
	return map[string]any{
		"id":          UUID(r),
		"name":        Text(r, "name"),
		"description": Text(r, "description"),
		"type":        TextOr(r, "type", "categories"),
		"last_used":   r.GetBool("last_used"),
		"project":     RelationUUID(app, r, "project"),
		"workspace":   RelationUUID(app, r, "workspace"),
		"points":      points,
		"created_at":  DateTime(r, "created_at"),
		"updated_at":  DateTime(r, "updated_at"),
		"created_by":  RelationUUID(app, r, "created_by"),
		"updated_by":  RelationUUID(app, r, "updated_by"),
		"deleted_at":  NullableDateTime(r, "deleted_at"),
	}
}

// EstimatePoint mirrors Django's EstimatePointSerializer.
func EstimatePoint(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":          UUID(r),
		"estimate":    RelationUUID(app, r, "estimate"),
		"key":         r.GetInt("key"),
		"value":       Text(r, "value"),
		"description": Text(r, "description"),
		"project":     RelationUUID(app, r, "project"),
		"workspace":   RelationUUID(app, r, "workspace"),
		"created_at":  DateTime(r, "created_at"),
		"updated_at":  DateTime(r, "updated_at"),
		"created_by":  RelationUUID(app, r, "created_by"),
		"updated_by":  RelationUUID(app, r, "updated_by"),
		"deleted_at":  NullableDateTime(r, "deleted_at"),
	}
}

// nextWorkItemSequence mirrors ProjectListSerializer.get_next_work_item_sequence.
// Plane tracks the allocator in `issue_sequences`, which is not part of this
// vertical slice, so the current maximum `issues.sequence_id` for the project is
// the equivalent source of truth.
func nextWorkItemSequence(app core.App, project *core.Record) int {
	var maxSeq int
	err := app.DB().
		NewQuery("SELECT COALESCE(MAX(sequence_id), 0) FROM issues WHERE project = {:project}").
		Bind(dbx.Params{"project": project.Id}).
		Row(&maxSeq)
	if err != nil {
		return 1
	}
	return maxSeq + 1
}
