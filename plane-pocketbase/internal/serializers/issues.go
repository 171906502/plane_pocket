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
// (`issue_labels`, `issue_assignees`, and the self-referential `parent`) are
// resolved here. Fields whose backing collection is outside the slice are
// projected as their empty values and reported:
//
//	estimate_point    -> estimate_points  (excluded)
//	cycle_id          -> cycle_issues     (excluded)
//	module_ids        -> module_issues    (excluded)
//	link_count        -> issue_links      (excluded)
//	attachment_count  -> file_assets      (excluded)
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
		"cycle_id":         nil,
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
