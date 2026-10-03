package serializers

import (
	"github.com/pocketbase/pocketbase/core"
)

// IssueActivity mirrors Django's IssueActivitySerializer
// (plane/app/serializers/issue.py). The serializer declares
// `fields = "__all__"` over a ProjectBaseModel, so the body is every flat model
// column — including the `project`/`workspace`/`actor` foreign keys rendered as
// Plane UUIDs — plus four nested read-only objects and `source_data`.
//
// The profile page's activity feed reads `actor_detail`, `field`, `verb`,
// `old_value`, `new_value`, `issue_detail` and `project_detail.identifier`
// (see apps/web/components/profile/overview/activity.tsx), so the nested
// objects are not optional decoration.
func IssueActivity(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}

	issue := relatedRecord(app, r, "issue")
	actor := relatedRecord(app, r, "actor")
	project := relatedRecord(app, r, "project")
	workspace := relatedRecord(app, r, "workspace")

	return map[string]any{
		"id":             UUID(r),
		"created_at":     DateTime(r, "created_at"),
		"updated_at":     DateTime(r, "updated_at"),
		"created_by":     RelationUUID(app, r, "created_by"),
		"updated_by":     RelationUUID(app, r, "updated_by"),
		"project":        RelationUUID(app, r, "project"),
		"workspace":      RelationUUID(app, r, "workspace"),
		"issue":          RelationUUID(app, r, "issue"),
		"verb":           Text(r, "verb"),
		"field":          NullableText(r, "field"),
		"old_value":      NullableText(r, "old_value"),
		"new_value":      NullableText(r, "new_value"),
		"comment":        Text(r, "comment"),
		"attachments":    JSONOrDefault(r, "attachments", []any{}),
		"issue_comment":  RelationUUID(app, r, "issue_comment"),
		"actor":          RelationUUID(app, r, "actor"),
		"old_identifier": NullableText(r, "old_identifier"),
		"new_identifier": NullableText(r, "new_identifier"),
		"epoch":          NullableNumber(r, "epoch"),

		// Nested read-only projections (Django: actor_detail / issue_detail /
		// project_detail / workspace_detail).
		"actor_detail":     UserLite(app, actor),
		"issue_detail":     IssueFlat(issue),
		"project_detail":   ProjectLite(project),
		"workspace_detail": WorkspaceLite(workspace),

		// `source_data` is derived from the issue's inbound source rows, which
		// belong to the excluded intake integration — Django also returns null
		// when the issue has no source data.
		"source_data": nil,
	}
}

// IssueFlat mirrors Django's IssueFlatSerializer: the subset of issue columns
// the activity feed needs to render a work-item link (`name`, `sequence_id`)
// without a second request. Returns null when the issue row is gone — Django's
// `on_delete=DO_NOTHING` leaves the activity pointing at a deleted issue, and
// the frontend checks for exactly that (`activity.issue_detail ? ... : "This
// work item has been deleted"`).
func IssueFlat(issue *core.Record) map[string]any {
	if issue == nil {
		return nil
	}
	priority := issue.GetString("priority")
	if priority == "" {
		priority = "none"
	}
	sortOrder := any(issue.GetFloat("sort_order"))
	if issue.Get("sort_order") == nil {
		sortOrder = float64(65535)
	}
	return map[string]any{
		"id":               UUID(issue),
		"name":             Text(issue, "name"),
		"description_json": JSONOrDefault(issue, "description_json", map[string]any{}),
		"description_html": Text(issue, "description_html"),
		"priority":         priority,
		"start_date":       DateOnly(issue, "start_date"),
		"target_date":      DateOnly(issue, "target_date"),
		"sequence_id":      issue.GetInt("sequence_id"),
		"sort_order":       sortOrder,
		"is_draft":         issue.GetBool("is_draft"),
	}
}

// relatedRecord resolves a single-relation column to its record, or nil when
// the column is empty or dangling.
func relatedRecord(app core.App, r *core.Record, field string) *core.Record {
	id := r.GetString(field)
	if id == "" {
		return nil
	}
	record, err := app.FindRecordById(relationCollection(app, r, field), id)
	if err != nil {
		return nil
	}
	return record
}
