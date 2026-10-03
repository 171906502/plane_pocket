package serializers

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// Intake mirrors Django's IntakeSerializer (fields = "__all__" plus
// `project_detail` and the annotated `pending_issue_count`). `pending_issue_count`
// counts the intake's intake_issues whose status is PENDING (-2); the handler
// computes it and passes it in.
func Intake(app core.App, r *core.Record, pendingCount int) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":          UUID(r),
		"created_at":  DateTime(r, "created_at"),
		"updated_at":  DateTime(r, "updated_at"),
		"created_by":  RelationUUID(app, r, "created_by"),
		"updated_by":  RelationUUID(app, r, "updated_by"),
		"deleted_at":  NullableDateTime(r, "deleted_at"),
		"project":     RelationUUID(app, r, "project"),
		"workspace":   RelationUUID(app, r, "workspace"),
		"name":        Text(r, "name"),
		"description": Text(r, "description"),
		"is_default":  r.GetBool("is_default"),
		"view_props":  jsonObject(r, "view_props"),
		"logo_props":  jsonObject(r, "logo_props"),

		"project_detail":      intakeProjectLite(app, r),
		"pending_issue_count": pendingCount,
	}
}

// intakeProjectLite mirrors Django's ProjectLiteSerializer embedded as
// `project_detail` on the intake.
func intakeProjectLite(app core.App, r *core.Record) any {
	project, _ := app.FindRecordById("projects", r.GetString("project"))
	if project == nil {
		return nil
	}
	cover := NullableText(project, "cover_image")
	return map[string]any{
		"id":              UUID(project),
		"identifier":      Text(project, "identifier"),
		"name":            Text(project, "name"),
		"cover_image":     cover,
		"cover_image_url": cover,
		"logo_props":      jsonObject(project, "logo_props"),
		"description":     Text(project, "description"),
	}
}

// IntakeIssue mirrors Django's IntakeIssueSerializer: the intake-issue row plus
// a nested `issue` in the IssueIntakeSerializer shape. `r` is the IntakeIssue
// record; the nested issue is resolved through its `issue` relation.
func IntakeIssue(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	issue, _ := app.FindRecordById("issues", r.GetString("issue"))
	return map[string]any{
		"id":           UUID(r),
		"status":       r.GetInt("status"),
		"duplicate_to": RelationUUID(app, r, "duplicate_to"),
		"snoozed_till": NullableDateTime(r, "snoozed_till"),
		"source":       NullableText(r, "source"),
		"issue":        IssueIntake(app, issue),
		"created_by":   RelationUUID(app, r, "created_by"),
	}
}

// IntakeIssueDetail mirrors Django's IntakeIssueDetailSerializer: the intake
// row, the full IssueDetailSerializer nested under `issue`, and the duplicate
// target expanded under `duplicate_issue_detail`.
func IntakeIssueDetail(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	issue, _ := app.FindRecordById("issues", r.GetString("issue"))
	var duplicate any
	if dup, _ := app.FindRecordById("issues", r.GetString("duplicate_to")); dup != nil {
		duplicate = IssueIntake(app, dup)
	}
	return map[string]any{
		"id":                     UUID(r),
		"status":                 r.GetInt("status"),
		"duplicate_to":           RelationUUID(app, r, "duplicate_to"),
		"snoozed_till":           NullableDateTime(r, "snoozed_till"),
		"duplicate_issue_detail": duplicate,
		"source":                 NullableText(r, "source"),
		"issue":                  IssueDetail(app, issue),
	}
}

// IssueIntake mirrors Django's IssueIntakeSerializer — the compact issue shape
// nested inside an intake-issue list row.
func IssueIntake(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	priority := r.GetString("priority")
	if priority == "" {
		priority = "none"
	}
	return map[string]any{
		"id":          UUID(r),
		"name":        Text(r, "name"),
		"priority":    priority,
		"sequence_id": r.GetInt("sequence_id"),
		"project_id":  RelationUUID(app, r, "project"),
		"created_at":  DateTime(r, "created_at"),
		"label_ids":   issueLabelIDs(app, r),
		"created_by":  RelationUUID(app, r, "created_by"),
	}
}

// IssueDetail mirrors Django's IssueDetailSerializer: IssueSerializer plus
// `description_html`, `is_subscribed` and `is_intake`. The two booleans are
// annotations in Plane; without a subscriptions/intake lookup they default to
// false, matching an issue that is neither subscribed nor itself an intake row.
func IssueDetail(app core.App, r *core.Record) map[string]any {
	data := Issue(app, r)
	if data == nil {
		return nil
	}
	data["description_html"] = Text(r, "description_html")
	data["is_subscribed"] = false
	data["is_intake"] = false
	return data
}

// PendingIntakeCount counts an intake's pending intake_issues (status -2).
func PendingIntakeCount(app core.App, intake *core.Record) int {
	var count int
	err := app.DB().
		NewQuery("SELECT COUNT(*) FROM intake_issues WHERE intake = {:intake} AND status = -2 AND deleted_at = ''").
		Bind(dbx.Params{"intake": intake.Id}).
		Row(&count)
	if err != nil {
		return 0
	}
	return count
}
