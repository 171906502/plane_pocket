package serializers

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// IssueView mirrors Django's IssueViewSerializer
// (plane/app/serializers/view.py), a DynamicBaseSerializer with
// `fields = "__all__"` plus the per-caller `is_favorite` boolean. The DRF FKs
// (`workspace`, `project`, `owned_by`) serialize as UUIDs; `uuid` is omitted.
//
// `is_favorite` is a UserFavorite lookup for the caller, so it is passed in
// rather than derived from the record.
func IssueView(app core.App, r *core.Record, isFavorite bool) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":                 UUID(r),
		"workspace":          RelationUUID(app, r, "workspace"),
		"project":            RelationUUID(app, r, "project"),
		"name":               Text(r, "name"),
		"description":        Text(r, "description"),
		"query":              jsonObject(r, "query"),
		"filters":            jsonObject(r, "filters"),
		"display_filters":    jsonObject(r, "display_filters"),
		"display_properties": jsonObject(r, "display_properties"),
		"rich_filters":       jsonObject(r, "rich_filters"),
		"access":             r.GetInt("access"),
		"sort_order":         sortOrderOrDefault(r),
		"logo_props":         jsonObject(r, "logo_props"),
		"owned_by":           RelationUUID(app, r, "owned_by"),
		"is_locked":          r.GetBool("is_locked"),
		"archived_at":        NullableDateTime(r, "archived_at"),
		"is_favorite":        isFavorite,
		"created_at":         DateTime(r, "created_at"),
		"updated_at":         DateTime(r, "updated_at"),
		"created_by":         RelationUUID(app, r, "created_by"),
		"updated_by":         RelationUUID(app, r, "updated_by"),
		"deleted_at":         NullableDateTime(r, "deleted_at"),
	}
}

// Sticky mirrors Django's StickySerializer (`fields = "__all__"` with
// `workspace`/`owner` read-only). `description` is a JSONField in Django;
// `description_binary` is a BinaryField backed by a protected PocketBase file,
// so the stored filename is projected (nil when unset), matching PageVersion.
func Sticky(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":                   UUID(r),
		"name":                 NullableText(r, "name"),
		"description":          jsonObject(r, "description"),
		"description_html":     Text(r, "description_html"),
		"description_stripped": NullableText(r, "description_stripped"),
		"description_binary":   NullableText(r, "description_binary"),
		"logo_props":           jsonObject(r, "logo_props"),
		"color":                NullableText(r, "color"),
		"background_color":     NullableText(r, "background_color"),
		"workspace":            RelationUUID(app, r, "workspace"),
		"owner":                RelationUUID(app, r, "owner"),
		"sort_order":           sortOrderOrDefault(r),
		"created_at":           DateTime(r, "created_at"),
		"updated_at":           DateTime(r, "updated_at"),
		"created_by":           RelationUUID(app, r, "created_by"),
		"updated_by":           RelationUUID(app, r, "updated_by"),
		"deleted_at":           NullableDateTime(r, "deleted_at"),
	}
}

// UserRecentVisit mirrors Django's WorkspaceRecentVisitSerializer: `entity_data`
// is the nested lite object for the visited entity (issue / page / project),
// resolved through the same entity map the Django serializer uses.
func UserRecentVisit(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":                UUID(r),
		"entity_name":       Text(r, "entity_name"),
		"entity_identifier": NullableText(r, "entity_identifier"),
		"entity_data":       recentVisitEntityData(app, r.GetString("entity_name"), r.GetString("entity_identifier")),
		"visited_at":        DateTime(r, "visited_at"),
	}
}

// recentVisitEntityData resolves the nested lite payload for a recent visit.
func recentVisitEntityData(app core.App, entityName, entityIdentifier string) any {
	if entityIdentifier == "" {
		return nil
	}
	switch entityName {
	case "issue":
		issue, _ := FindByUUID(app, "issues", entityIdentifier)
		if issue == nil {
			return nil
		}
		return map[string]any{
			"id":                 UUID(issue),
			"name":               Text(issue, "name"),
			"state":              RelationUUID(app, issue, "state"),
			"priority":           Text(issue, "priority"),
			"assignees":          issueAssigneeIDs(app, issue),
			"type":               nil,
			"sequence_id":        issue.GetInt("sequence_id"),
			"project_id":         RelationUUID(app, issue, "project"),
			"project_identifier": projectIdentifier(app, issue.GetString("project")),
		}
	case "page":
		page, _ := FindByUUID(app, "pages", entityIdentifier)
		if page == nil {
			return nil
		}
		project := firstPageProject(app, page.Id)
		return map[string]any{
			"id":                 UUID(page),
			"name":               Text(page, "name"),
			"logo_props":         jsonObject(page, "logo_props"),
			"project_id":         nullableString(project),
			"owned_by":           RelationUUID(app, page, "owned_by"),
			"project_identifier": projectIdentifier(app, project),
		}
	case "project":
		project, _ := FindByUUID(app, "projects", entityIdentifier)
		if project == nil {
			return nil
		}
		return map[string]any{
			"id":              UUID(project),
			"name":            Text(project, "name"),
			"logo_props":      jsonObject(project, "logo_props"),
			"project_members": projectMemberIDs(app, project.Id),
			"identifier":      Text(project, "identifier"),
		}
	}
	return nil
}

// projectMemberIDs resolves a project's active, non-bot member UUIDs.
func projectMemberIDs(app core.App, projectID string) []string {
	rows, err := app.FindRecordsByFilter(
		"project_members",
		"project = {:project} && is_active = true && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"project": projectID},
	)
	out := make([]string, 0, len(rows))
	if err != nil {
		return out
	}
	for _, row := range rows {
		user, _ := app.FindRecordById("users", row.GetString("member"))
		if user == nil || user.GetBool("is_bot") {
			continue
		}
		out = append(out, UUID(user))
	}
	return out
}

// projectIdentifier returns a project's short identifier (e.g. "PROJ").
func projectIdentifier(app core.App, projectID string) any {
	if projectID == "" {
		return nil
	}
	project, _ := app.FindRecordById("projects", projectID)
	if project == nil {
		return nil
	}
	return Text(project, "identifier")
}

// firstPageProject returns the Plane UUID of the first project a page is linked
// to via `project_pages` (Django's `Page.projects.first()`), or "" when none.
func firstPageProject(app core.App, pageID string) string {
	rows, err := app.FindRecordsByFilter(
		"project_pages",
		"page = {:page} && deleted_at = ''",
		"", 1, 0,
		dbx.Params{"page": pageID},
	)
	if err != nil || len(rows) == 0 {
		return ""
	}
	if uuid, ok := RelationUUID(app, rows[0], "project").(string); ok {
		return uuid
	}
	return ""
}

// nullableString renders an empty string as null.
func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
