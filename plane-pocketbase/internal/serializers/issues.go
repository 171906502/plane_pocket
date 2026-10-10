package serializers

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// Issue mirrors Django's IssueSerializer (plane/app/serializers/issue.py) plus
// the two computed fields the api spec lists for `issue.list_create`
// (`workspace_id` and `state__group`, the latter from Issue.state.group).
//
// Counts and id-lists that Plane reads from join collections inside this slice
// (`issue_labels`, `issue_assignees`, the self-referential `parent`, and
// `cycle_issues`) are resolved here. Fields whose backing collection is outside
// the slice are projected as their empty values and reported:
//
//	estimate_point    -> estimate_points  (excluded)
//	module_ids        -> module_issues    (excluded)
//	link_count        -> issue_links      (excluded)
//	attachment_count  -> file_assets      (excluded)
//
// `cycle_id` used to be listed above as excluded and hardcoded to nil. That was
// a stale placeholder: `cycle_issues` now exists, so reporting nil made every
// issue look detached from its cycle. The cycles UI reads `issue.cycle_id`
// (packages/types/src/issues/issue.ts:63) and offers it as a sortable/groupable
// property (:149, :177), so the placeholder was user-visible — and it made the
// Wave-2 `cycles/{id}/cycle-progress/` endpoint permanently report 0 issues.
func Issue(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}

	priority := r.GetString("priority")
	if priority == "" {
		priority = "none"
	}

	stateGroup := any(nil)
	stateID := RelationUUID(app, r, "state")
	if stateRecord, _ := app.FindRecordById("states", r.GetString("state")); stateRecord != nil {
		stateGroup = stateRecord.GetString("group")
	}

	sortOrder := any(r.GetFloat("sort_order"))
	if r.Get("sort_order") == nil {
		sortOrder = float64(65535)
	}

	return map[string]any{
		"id":               UUID(r),
		"name":             Text(r, "name"),
		"state_id":         stateID,
		"state__group":     stateGroup,
		"sort_order":       sortOrder,
		"completed_at":     DateOnly(r, "completed_at"),
		"estimate_point":   nil,
		"priority":         priority,
		"start_date":       DateOnly(r, "start_date"),
		"target_date":      DateOnly(r, "target_date"),
		"sequence_id":      r.GetInt("sequence_id"),
		"project_id":       RelationUUID(app, r, "project"),
		"workspace_id":     RelationUUID(app, r, "workspace"),
		"parent_id":        RelationUUID(app, r, "parent"),
		"cycle_id":         issueCycleID(app, r),
		"module_ids":       []string{},
		"label_ids":        issueLabelIDs(app, r),
		"assignee_ids":     issueAssigneeIDs(app, r),
		"sub_issues_count": subIssuesCount(app, r),
		"created_at":       DateTime(r, "created_at"),
		"updated_at":       DateTime(r, "updated_at"),
		"created_by":       RelationUUID(app, r, "created_by"),
		"updated_by":       RelationUUID(app, r, "updated_by"),
		"attachment_count": 0,
		"link_count":       0,
		"is_draft":         r.GetBool("is_draft"),
		"archived_at":      DateOnly(r, "archived_at"),
	}
}

// issueCycleID resolves the issue's cycle through the `cycle_issues` through
// table, mirroring Django's IssueSerializer.to_representation
// (plane/app/serializers/issue.py:744-769):
//
//	CycleIssue = apps.get_model("db.CycleIssue")
//	cycle_issue = CycleIssue.objects.filter(issue=issue).first()
//	cycle = cycle_issue.cycle_id if cycle_issue else None
//
// The association lives ONLY in CycleIssue — there is no `issues.cycle` column
// — and the first bridge row wins, so the filter is limited to 1.
func issueCycleID(app core.App, issue *core.Record) any {
	rows, err := app.FindRecordsByFilter(
		"cycle_issues",
		"issue = {:issue} && deleted_at = ''",
		"", 1, 0,
		dbx.Params{"issue": issue.Id},
	)
	if err != nil || len(rows) == 0 {
		return nil
	}
	cycle, _ := app.FindRecordById("cycles", rows[0].GetString("cycle"))
	if cycle == nil {
		return nil
	}
	return UUID(cycle)
}

// issueLabelIDs resolves the issue's labels through the `issue_labels` through
// table, matching Django's ArrayAgg over IssueLabel.
func issueLabelIDs(app core.App, issue *core.Record) []string {
	rows, err := app.FindRecordsByFilter(
		"issue_labels",
		"issue = {:issue} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"issue": issue.Id},
	)
	out := make([]string, 0, len(rows))
	if err != nil {
		return out
	}
	seen := map[string]struct{}{}
	for _, row := range rows {
		label, _ := app.FindRecordById("labels", row.GetString("label"))
		if label == nil {
			continue
		}
		id := UUID(label)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// issueAssigneeIDs resolves the issue's assignees through `issue_assignees`.
func issueAssigneeIDs(app core.App, issue *core.Record) []string {
	rows, err := app.FindRecordsByFilter(
		"issue_assignees",
		"issue = {:issue} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"issue": issue.Id},
	)
	out := make([]string, 0, len(rows))
	if err != nil {
		return out
	}
	seen := map[string]struct{}{}
	for _, row := range rows {
		user, _ := app.FindRecordById("users", row.GetString("assignee"))
		if user == nil {
			continue
		}
		id := UUID(user)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// subIssuesCount counts issues whose parent is the given issue.
func subIssuesCount(app core.App, issue *core.Record) int {
	var count int
	err := app.DB().
		NewQuery("SELECT COUNT(*) FROM issues WHERE parent = {:parent} AND deleted_at = ''").
		Bind(dbx.Params{"parent": issue.Id}).
		Row(&count)
	if err != nil {
		return 0
	}
	return count
}
