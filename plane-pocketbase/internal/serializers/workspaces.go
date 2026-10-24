package serializers

import "github.com/pocketbase/pocketbase/core"

// Workspace mirrors Django's WorkSpaceSerializer (fields = "__all__" plus the
// computed `total_members`, `logo_url` and `role`).
//
// `role` and `total_members` are per-caller aggregates, so the handler resolves
// them and passes them in.
func Workspace(app core.App, r *core.Record, role, totalMembers int) map[string]any {
	if r == nil {
		return nil
	}
	logo := NullableText(r, "logo")
	return map[string]any{
		"id":                UUID(r),
		"name":              Text(r, "name"),
		"slug":              Text(r, "slug"),
		"logo":              logo,
		"logo_url":          logo,
		"owner":             RelationUUID(app, r, "owner"),
		"organization_size": NullableText(r, "organization_size"),
		"timezone":          NullableText(r, "timezone"),
		"background_color":  NullableText(r, "background_color"),
		"created_at":        DateTime(r, "created_at"),
		"updated_at":        DateTime(r, "updated_at"),
		"created_by":        RelationUUID(app, r, "created_by"),
		"updated_by":        RelationUUID(app, r, "updated_by"),
		"deleted_at":        NullableDateTime(r, "deleted_at"),
		"total_members":     totalMembers,
		"role":              role,
	}
}

// DefaultWorkspaceProps mirrors `get_default_props()` in
// plane/db/models/workspace.py — the seed for WorkspaceMember.view_props and
// .default_props. PocketBase has no field-level defaults, so it is projected at
// serialization time. Note this is NOT the same shape as the *project* variant
// (serializers.DefaultProps): the workspace one carries an extra
// `display_properties` block.
func DefaultWorkspaceProps() map[string]any {
	return map[string]any{
		"filters": DefaultIssueFilters(),
		"display_filters": map[string]any{
			"group_by":            nil,
			"order_by":            "-created_at",
			"type":                nil,
			"sub_issue":           true,
			"show_empty_groups":   true,
			"layout":              "list",
			"calendar_date_range": "",
		},
		"display_properties": map[string]any{
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
		},
	}
}

// DefaultIssueProps mirrors `get_issue_props()` in plane/db/models/workspace.py.
func DefaultIssueProps() map[string]any {
	return map[string]any{
		"subscribed": true,
		"assigned":   true,
		"created":    true,
		"all_issues": true,
	}
}

// WorkspaceUserProperties mirrors Django's WorkspaceUserPropertiesSerializer
// (`fields = "__all__"`, `workspace`/`user` read-only).
//
// It is the body of GET/PATCH /api/workspaces/{slug}/user-properties/, which
// carries the caller's saved issue filters and display preferences *per
// workspace*. Note the three JSON columns are nullable in our schema and
// carry Django `default=` values, so they are projected with their defaults
// rather than as null — the frontend feeds `display_filters` straight into the
// issue layout.
func WorkspaceUserProperties(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	displayFilters := DefaultWorkspaceProps()["display_filters"]
	displayProperties := DefaultWorkspaceProps()["display_properties"]
	limit := r.GetInt("navigation_project_limit")
	if r.Get("navigation_project_limit") == nil {
		limit = 10
	}
	control := TextOr(r, "navigation_control_preference", "ACCORDION")
	return map[string]any{
		"id":                            UUID(r),
		"workspace":                     RelationUUID(app, r, "workspace"),
		"user":                          RelationUUID(app, r, "user"),
		"filters":                       JSONOrDefault(r, "filters", DefaultIssueFilters()),
		"display_filters":               JSONOrDefault(r, "display_filters", displayFilters),
		"display_properties":            JSONOrDefault(r, "display_properties", displayProperties),
		"rich_filters":                  JSONOrDefault(r, "rich_filters", map[string]any{}),
		"navigation_project_limit":      limit,
		"navigation_control_preference": control,
		"created_at":                    DateTime(r, "created_at"),
		"updated_at":                    DateTime(r, "updated_at"),
		"created_by":                    RelationUUID(app, r, "created_by"),
		"updated_by":                    RelationUUID(app, r, "updated_by"),
		"deleted_at":                    NullableDateTime(r, "deleted_at"),
	}
}

// WorkspaceUserPreference mirrors Django's WorkspaceUserPreferenceSerializer,
// whose field list is exactly `["key", "is_pinned", "sort_order"]`.
//
// GET /api/workspaces/{slug}/sidebar-preferences/ folds these into a map keyed
// by `key` (see WorkspaceUserPreferenceViewSet.get), so this returns the
// per-entry object and the handler does the keying.
func WorkspaceUserPreference(r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	sortOrder := any(r.GetFloat("sort_order"))
	if r.Get("sort_order") == nil {
		sortOrder = float64(65535)
	}
	return map[string]any{
		"key":        r.GetString("key"),
		"is_pinned":  r.GetBool("is_pinned"),
		"sort_order": sortOrder,
	}
}

// SidebarPreferenceKeys mirrors WorkspaceUserPreference.UserPreferenceKeys, in
// Django's declaration order (which is the order `get` seeds missing rows in).
var SidebarPreferenceKeys = []string{
	"views", "active_cycles", "analytics", "drafts", "your_work", "archives", "stickies",
}

// SidebarPreferencePinnedByDefault mirrors the keys Django pins on creation.
func SidebarPreferencePinnedByDefault(key string) bool {
	switch key {
	case "drafts", "your_work", "stickies":
		return true
	}
	return false
}

// HomeWidgetKeys mirrors WorkspaceHomePreference.HomeWidgetKeys, in Django's
// declaration order. The endpoint filters out the last two
// (`quick_tutorial`, `new_at_plane`) before seeding, so only the first three
// are ever returned by GET /home-preferences/.
var HomeWidgetKeys = []string{
	"quick_links", "recents", "my_stickies", "new_at_plane", "quick_tutorial",
}

// HomeWidgetSeededKeys is HomeWidgetKeys minus the two the view excludes.
var HomeWidgetSeededKeys = []string{"quick_links", "recents", "my_stickies"}

// WorkspaceHomePreference mirrors Django's list serialisation of
// WorkspaceHomePreference: `.values("key", "is_enabled", "config", "sort_order")`.
func WorkspaceHomePreference(r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	config := any(r.Get("config"))
	if r.Get("config") == nil {
		config = map[string]any{}
	}
	sortOrder := any(r.GetFloat("sort_order"))
	if r.Get("sort_order") == nil {
		sortOrder = float64(65535)
	}
	return map[string]any{
		"key":        r.GetString("key"),
		"is_enabled": r.GetBool("is_enabled"),
		"config":     config,
		"sort_order": sortOrder,
	}
}

// WorkspaceHomePreferencePatched mirrors WorkspaceHomePreferenceSerializer
// (plane/app/serializers/workspace.py), whose `fields` are
// `["key", "is_enabled", "sort_order"]` — note the PATCH response has **no**
// `config`, unlike the GET list above.
func WorkspaceHomePreferencePatched(r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	sortOrder := any(r.GetFloat("sort_order"))
	if r.Get("sort_order") == nil {
		sortOrder = float64(65535)
	}
	return map[string]any{
		"key":        r.GetString("key"),
		"is_enabled": r.GetBool("is_enabled"),
		"sort_order": sortOrder,
	}
}

// WorkspaceMemberMe mirrors Django's WorkspaceMemberMeSerializer
// (plane/app/serializers/workspace.py): `fields = "__all__"` over
// WorkspaceMember plus the `draft_issue_count` annotation.
//
// It is the body of GET /api/workspaces/{slug}/workspace-members/me/, which the
// web app's `workspace-wrapper.tsx` uses as its *membership gate* — an absent
// entry renders the "You're not a member of this workspace" screen. So a wrong
// or missing response here locks the user out of the entire workspace.
func WorkspaceMemberMe(app core.App, r *core.Record, draftIssueCount int) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":                        UUID(r),
		"workspace":                 RelationUUID(app, r, "workspace"),
		"member":                    RelationUUID(app, r, "member"),
		"role":                      r.GetInt("role"),
		"company_role":              NullableText(r, "company_role"),
		"view_props":                JSONOrDefault(r, "view_props", DefaultWorkspaceProps()),
		"default_props":             JSONOrDefault(r, "default_props", DefaultWorkspaceProps()),
		"issue_props":               JSONOrDefault(r, "issue_props", DefaultIssueProps()),
		"is_active":                 r.GetBool("is_active"),
		"getting_started_checklist": JSONOrDefault(r, "getting_started_checklist", map[string]any{}),
		"tips":                      JSONOrDefault(r, "tips", map[string]any{}),
		"explored_features":         JSONOrDefault(r, "explored_features", map[string]any{}),
		"created_at":                DateTime(r, "created_at"),
		"updated_at":                DateTime(r, "updated_at"),
		"created_by":                RelationUUID(app, r, "created_by"),
		"updated_by":                RelationUUID(app, r, "updated_by"),
		"deleted_at":                NullableDateTime(r, "deleted_at"),
		"draft_issue_count":         draftIssueCount,
	}
}

// WorkspaceMember mirrors the `id, member, role` projection used by
// WorkSpaceMemberViewSet.list (plane/app/views/workspace/member.py). `member`
// is the nested UserLite/UserAdminLite object — the frontend's IWorkspaceMember
// type expects an object there, not an id.
func WorkspaceMember(app core.App, r *core.Record, admin bool) map[string]any {
	if r == nil {
		return nil
	}
	memberRecord, _ := app.FindRecordById("users", r.GetString("member"))
	var member any
	if admin {
		member = UserAdminLite(app, memberRecord)
	} else {
		member = UserLite(app, memberRecord)
	}
	return map[string]any{
		"id":     UUID(r),
		"member": member,
		"role":   r.GetInt("role"),
	}
}
