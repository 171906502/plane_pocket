package serializers

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// DraftIssue mirrors Django's DraftIssueSerializer (plane/app/serializers/draft.py).
// Drafts are workspace-scoped and private to their creator. The many-to-many
// `cycle_id`, `module_ids`, `label_ids` and `assignee_ids` are read-side
// aggregates over the draft join tables, resolved here.
//
// `state_id`, `parent_id`, `project_id` and `type_id` are exposed with the
// `_id` suffix that the serializer declares explicitly (unlike the issue
// serializer, which exposes the bare relation names).
func DraftIssue(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	priority := r.GetString("priority")
	if priority == "" {
		priority = "none"
	}
	return map[string]any{
		"id":               UUID(r),
		"name":             Text(r, "name"),
		"state_id":         RelationUUID(app, r, "state"),
		"sort_order":       r.GetFloat("sort_order"),
		"completed_at":     NullableDateTime(r, "completed_at"),
		"estimate_point":   RelationUUID(app, r, "estimate_point"),
		"priority":         priority,
		"start_date":       DateOnly(r, "start_date"),
		"target_date":      DateOnly(r, "target_date"),
		"project_id":       RelationUUID(app, r, "project"),
		"parent_id":        RelationUUID(app, r, "parent"),
		"cycle_id":         draftCycleID(app, r),
		"module_ids":       draftModuleIDs(app, r),
		"label_ids":        draftLabelIDs(app, r),
		"assignee_ids":     draftAssigneeIDs(app, r),
		"created_at":       DateTime(r, "created_at"),
		"updated_at":       DateTime(r, "updated_at"),
		"created_by":       RelationUUID(app, r, "created_by"),
		"updated_by":       RelationUUID(app, r, "updated_by"),
		"type_id":          RelationUUID(app, r, "type"),
		"description_html": Text(r, "description_html"),
	}
}

// DraftIssueDetail mirrors Django's DraftIssueDetailSerializer. It re-declares
// description_html (as the issue detail serializer does), so the wire shape is
// identical to DraftIssue.
func DraftIssueDetail(app core.App, r *core.Record) map[string]any {
	return DraftIssue(app, r)
}

// draftCycleID resolves the first active cycle linked to the draft (Django uses
// a Subquery over DraftIssueCycle ordered by default, taking [:1]).
func draftCycleID(app core.App, draft *core.Record) any {
	rows, err := app.FindRecordsByFilter(
		"draft_issue_cycles",
		"draft_issue = {:draft} && deleted_at = ''",
		"", 1, 0,
		dbx.Params{"draft": draft.Id},
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

// draftModuleIDs resolves the draft's modules, skipping archived ones to match
// Django's filter.
func draftModuleIDs(app core.App, draft *core.Record) []string {
	rows, err := app.FindRecordsByFilter(
		"draft_issue_modules",
		"draft_issue = {:draft} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"draft": draft.Id},
	)
	out := make([]string, 0, len(rows))
	if err != nil {
		return out
	}
	seen := map[string]struct{}{}
	for _, row := range rows {
		module, _ := app.FindRecordById("modules", row.GetString("module"))
		if module == nil || module.GetString("archived_at") != "" {
			continue
		}
		id := UUID(module)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// draftLabelIDs resolves the draft's labels through `draft_issue_labels`.
func draftLabelIDs(app core.App, draft *core.Record) []string {
	rows, err := app.FindRecordsByFilter(
		"draft_issue_labels",
		"draft_issue = {:draft} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"draft": draft.Id},
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

// draftAssigneeIDs resolves the draft's assignees through `draft_issue_assignees`.
func draftAssigneeIDs(app core.App, draft *core.Record) []string {
	rows, err := app.FindRecordsByFilter(
		"draft_issue_assignees",
		"draft_issue = {:draft} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"draft": draft.Id},
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
