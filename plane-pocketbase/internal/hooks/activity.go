package hooks

import (
	"fmt"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// ---------------------------------------------------------------------------
// issue_activities row under construction
// ---------------------------------------------------------------------------

// activityRow mirrors the upstream IssueActivity model columns this pipeline
// writes (plane/db/models/issue_activity.py). Relation columns (issue/project/
// workspace/actor/issue_comment) hold PocketBase record ids; the two
// *_identifier columns hold Plane UUIDs because the frontend renders them as
// links (e.g. /projects/{project}/cycles/{new_identifier}).
type activityRow struct {
	issue         string
	project       string
	workspace     string
	actor         string
	verb          string
	field         string
	comment       string
	oldValue      string
	newValue      string
	oldIdentifier string
	newIdentifier string
	issueComment  string
}

// writeActivity persists one derived activity row. Errors are logged and
// swallowed: a failure here must never fail the save that triggered it
// (06-PITFALLS.md D-12 premise 3).
func writeActivity(app core.App, row activityRow) {
	if row.issue == "" || row.project == "" || row.workspace == "" || row.verb == "" {
		// The required relations are missing — the triggering record is not a
		// Plane-shaped row (e.g. a system collection). Nothing to anchor to.
		return
	}
	collection, err := app.FindCollectionByNameOrId("issue_activities")
	if err != nil {
		app.Logger().Error("hooks: issue_activities collection missing", "error", err)
		return
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("verb", row.verb)
	record.Set("issue", row.issue)
	record.Set("project", row.project)
	record.Set("workspace", row.workspace)
	if row.field != "" {
		record.Set("field", row.field)
	}
	if row.comment != "" {
		record.Set("comment", row.comment)
	}
	record.Set("old_value", row.oldValue)
	record.Set("new_value", row.newValue)
	if row.oldIdentifier != "" {
		record.Set("old_identifier", row.oldIdentifier)
	}
	if row.newIdentifier != "" {
		record.Set("new_identifier", row.newIdentifier)
	}
	if row.issueComment != "" {
		record.Set("issue_comment", row.issueComment)
	}
	if row.actor != "" {
		record.Set("actor", row.actor)
		record.Set("created_by", row.actor)
	}
	if err := app.Save(record); err != nil {
		app.Logger().Error(
			"hooks: issue_activities write failed (main save unaffected)",
			"issue", row.issue, "verb", row.verb, "field", row.field, "error", err,
		)
	}
}

// ---------------------------------------------------------------------------
// bulk-update coalescing
// ---------------------------------------------------------------------------

// bulkWindow is how long an identical (actor, project, field, new_value)
// transition counts as one batch. POST bulk-operation-issues saves N issues in
// a tight loop; without coalescing each row would produce its own activity.
// The window is generous relative to a loop (milliseconds per issue) and tiny
// relative to human-paced edits, so two separate single PATCHes that happen to
// make the same change inside 5s collapse into one row — an accepted trade-off
// (upstream has no bulk-operation endpoint at all, so there is no contract to
// violate).
const bulkWindow = 5 * time.Second

var (
	bulkMu   sync.Mutex
	bulkSeen = map[bulkKey]time.Time{}
)

type bulkKey struct {
	actor    string
	project  string
	field    string
	newValue string
}

// commitBulk writes one property-change activity, collapsing bursts of
// identical transitions into the first row of the batch.
func commitBulk(app core.App, row activityRow) {
	key := bulkKey{actor: row.actor, project: row.project, field: row.field, newValue: row.newValue}
	now := time.Now()

	bulkMu.Lock()
	if len(bulkSeen) > 512 {
		for k, at := range bulkSeen {
			if now.Sub(at) >= bulkWindow {
				delete(bulkSeen, k)
			}
		}
	}
	suppressed := false
	if last, ok := bulkSeen[key]; ok && now.Sub(last) < bulkWindow {
		suppressed = true
	} else {
		bulkSeen[key] = now
	}
	bulkMu.Unlock()

	if suppressed {
		return
	}
	writeActivity(app, row)
}

// ---------------------------------------------------------------------------
// issues — create / property diff
// ---------------------------------------------------------------------------

// activityIssueCreated mirrors upstream create_issue_activity: one row with
// verb="created" and an empty `field` — the frontend's activity dispatcher
// renders a null field as "created the work item" (activity-list.tsx).
func activityIssueCreated(e *core.RecordEvent) {
	issue := e.Record
	writeActivity(e.App, activityRow{
		issue:     issue.Id,
		project:   issue.GetString("project"),
		workspace: issue.GetString("workspace"),
		actor:     issue.GetString("created_by"),
		verb:      "created",
		comment:   "created the issue",
	})
}

// activityIssueUpdated diffs the whitelisted property columns against
// record.Original() (the pre-save DB state) and writes one activity per
// changed field, mirroring upstream update_issue_activity's ISSUE_ACTIVITY_MAPPER.
//
// Whitelist (11-TASKLIST L-1): state, priority, assignees, cycle, module,
// labels, start_date, target_date, estimate_point, parent, name. The join-table
// properties (assignees/labels/cycle/module) are not columns on `issues`; they
// are derived from their bridge collections below. estimate_point has no
// column yet (L-2) — the diff is dormant until it lands and then starts
// working with no further change here.
func activityIssueUpdated(e *core.RecordEvent) {
	issue := e.Record
	orig := issue.Original()
	app := e.App

	base := activityRow{
		issue:     issue.Id,
		project:   issue.GetString("project"),
		workspace: issue.GetString("workspace"),
		actor:     issue.GetString("updated_by"),
		verb:      "updated",
	}
	if base.actor == "" {
		base.actor = issue.GetString("created_by")
	}

	if old, new := orig.GetString("state"), issue.GetString("state"); old != new {
		oldName, oldUUID := resolveName(app, "states", old)
		newName, newUUID := resolveName(app, "states", new)
		row := base
		row.field = "state"
		row.comment = "updated the state to"
		row.oldValue, row.oldIdentifier = oldName, oldUUID
		row.newValue, row.newIdentifier = newName, newUUID
		commitBulk(app, row)
	}

	if old, new := orig.GetString("priority"), issue.GetString("priority"); old != new {
		row := base
		row.field = "priority"
		row.comment = "updated the priority to"
		row.oldValue, row.newValue = old, new
		commitBulk(app, row)
	}

	if old, new := orig.GetString("name"), issue.GetString("name"); old != new {
		row := base
		row.field = "name"
		row.comment = "updated the name to"
		row.oldValue, row.newValue = old, new
		commitBulk(app, row)
	}

	if old, new := orig.GetString("parent"), issue.GetString("parent"); old != new {
		oldRef, oldUUID := issueRef(app, old)
		newRef, newUUID := issueRef(app, new)
		row := base
		row.field = "parent"
		row.comment = "updated the parent issue to"
		row.oldValue, row.oldIdentifier = oldRef, oldUUID
		row.newValue, row.newIdentifier = newRef, newUUID
		commitBulk(app, row)
	}

	// Dates are compared as parsed times, not raw strings: a handler may Set()
	// "2026-10-24" while the stored form is "2026-10-24 00:00:00.000Z", and a
	// string diff there would fire on every save.
	if !orig.GetDateTime("start_date").Equal(issue.GetDateTime("start_date")) {
		row := base
		row.field = "start_date"
		row.comment = "updated the start date to "
		row.oldValue, row.newValue = dateValue(orig, "start_date"), dateValue(issue, "start_date")
		commitBulk(app, row)
	}

	if !orig.GetDateTime("target_date").Equal(issue.GetDateTime("target_date")) {
		row := base
		row.field = "target_date"
		row.comment = "updated the target date to"
		row.oldValue, row.newValue = dateValue(orig, "target_date"), dateValue(issue, "target_date")
		commitBulk(app, row)
	}

	if old, new := orig.GetString("estimate_point"), issue.GetString("estimate_point"); old != new {
		// Dormant until L-2 adds the column; upstream resolves the point's
		// `value` for display and its estimate type for the field name, but the
		// frontend accepts the legacy "estimate_point" field too.
		oldName, oldUUID := resolveName(app, "estimate_points", old)
		newName, newUUID := resolveName(app, "estimate_points", new)
		row := base
		row.field = "estimate_point"
		row.comment = "updated the estimate point to "
		row.oldValue, row.oldIdentifier = oldName, oldUUID
		row.newValue, row.newIdentifier = newName, newUUID
		commitBulk(app, row)
	}
}

// ---------------------------------------------------------------------------
// issue_comments
// ---------------------------------------------------------------------------

// activityCommentCreated mirrors upstream create_comment_activity. The row is
// excluded from every property feed (`field != "comment"` filters in
// wave2.go / user_profile.go / activity_export.go) and only surfaces in the
// dashboard's recent-activity widget, which does not filter — same as upstream.
func activityCommentCreated(e *core.RecordEvent) {
	comment := e.Record
	writeActivity(e.App, activityRow{
		issue:         comment.GetString("issue"),
		project:       comment.GetString("project"),
		workspace:     comment.GetString("workspace"),
		actor:         comment.GetString("actor"),
		verb:          "comment",
		field:         "comment",
		comment:       "created a comment",
		newValue:      comment.GetString("comment_html"),
		newIdentifier: serializers.UUID(comment),
		issueComment:  comment.Id,
	})
}

// ---------------------------------------------------------------------------
// issue_relations
// ---------------------------------------------------------------------------

// activityRelationCreated mirrors upstream create_issue_relation_activity:
// one activity on each side of the relation. The stored row keeps the
// normalised relation type (getActualRelation) with the two sides in `issue` /
// `related_issue`, which is enough to reconstruct both upstream rows
// deterministically — the "requested" and "inverse" orientations always map to
// the same pair.
func activityRelationCreated(e *core.RecordEvent) {
	row := e.Record
	app := e.App

	leftRef, leftUUID := issueRef(app, row.GetString("issue"))
	rightRef, rightUUID := issueRef(app, row.GetString("related_issue"))
	relationType := row.GetString("relation_type")
	inverse := inverseRelation(relationType)
	actor := row.GetString("created_by")
	project := row.GetString("project")
	workspace := row.GetString("workspace")

	// On the `issue` side: this relation type, pointing at the related issue.
	writeActivity(app, activityRow{
		issue:         row.GetString("issue"),
		project:       project,
		workspace:     workspace,
		actor:         actor,
		verb:          "updated",
		field:         relationType,
		comment:       fmt.Sprintf("added %s relation", relationType),
		oldValue:      "",
		newValue:      rightRef,
		oldIdentifier: rightUUID,
	})
	// On the `related_issue` side: the inverse type, pointing back.
	writeActivity(app, activityRow{
		issue:         row.GetString("related_issue"),
		project:       project,
		workspace:     workspace,
		actor:         actor,
		verb:          "updated",
		field:         inverse,
		comment:       fmt.Sprintf("added %s relation", inverse),
		oldValue:      "",
		newValue:      leftRef,
		oldIdentifier: leftUUID,
	})
}

// inverseRelation ports plane.utils.issue_relation_mapper.get_inverse_relation.
func inverseRelation(relationType string) string {
	switch relationType {
	case "start_after", "start_before":
		if relationType == "start_after" {
			return "start_before"
		}
		return "start_after"
	case "finish_after", "finish_before":
		if relationType == "finish_after" {
			return "finish_before"
		}
		return "finish_after"
	case "blocked_by":
		return "blocking"
	case "blocking":
		return "blocked_by"
	case "implemented_by":
		return "implements"
	case "implements":
		return "implemented_by"
	default:
		return relationType
	}
}

// ---------------------------------------------------------------------------
// join tables: assignees / labels / cycle / module
// ---------------------------------------------------------------------------

// activityAssigneeAdded mirrors the "added assignee" half of upstream
// track_assignees. Removals go through app.Delete (hard delete) which is
// outside the sanctioned hook set, so they are not tracked (noted in the L-1
// report).
func activityAssigneeAdded(e *core.RecordEvent) {
	row := e.Record
	name, uuid := resolveName(e.App, "users", row.GetString("assignee"))
	commitBulk(e.App, activityRow{
		issue:         row.GetString("issue"),
		project:       row.GetString("project"),
		workspace:     row.GetString("workspace"),
		actor:         row.GetString("created_by"),
		verb:          "updated",
		field:         "assignees",
		comment:       "added assignee ",
		oldValue:      "",
		newValue:      name,
		newIdentifier: uuid,
	})
}

// activityLabelAdded mirrors the "added label" half of upstream track_labels.
func activityLabelAdded(e *core.RecordEvent) {
	row := e.Record
	name, uuid := resolveName(e.App, "labels", row.GetString("label"))
	commitBulk(e.App, activityRow{
		issue:         row.GetString("issue"),
		project:       row.GetString("project"),
		workspace:     row.GetString("workspace"),
		actor:         row.GetString("created_by"),
		verb:          "updated",
		field:         "labels",
		comment:       "added label ",
		oldValue:      "",
		newValue:      name,
		newIdentifier: uuid,
	})
}

// activityCycleLinked mirrors upstream create_cycle_issue_activity's created
// branch: verb="created", field="cycles".
func activityCycleLinked(e *core.RecordEvent) {
	row := e.Record
	name, uuid := resolveName(e.App, "cycles", row.GetString("cycle"))
	commitBulk(e.App, activityRow{
		issue:         row.GetString("issue"),
		project:       row.GetString("project"),
		workspace:     row.GetString("workspace"),
		actor:         row.GetString("created_by"),
		verb:          "created",
		field:         "cycles",
		comment:       fmt.Sprintf("added cycle %s", name),
		oldValue:      "",
		newValue:      name,
		newIdentifier: uuid,
	})
}

// activityCycleLinkChanged covers the two update shapes of a cycle bridge row:
// moving the issue to another cycle (upstream's updated branch) and removing it
// from the cycle (soft delete via deleted_at — upstream delete_cycle_issue_activity).
func activityCycleLinkChanged(e *core.RecordEvent) {
	row := e.Record
	orig := row.Original()

	if orig.GetString("deleted_at") == "" && row.GetString("deleted_at") != "" {
		name, uuid := resolveName(e.App, "cycles", row.GetString("cycle"))
		commitBulk(e.App, activityRow{
			issue:         row.GetString("issue"),
			project:       row.GetString("project"),
			workspace:     row.GetString("workspace"),
			actor:         row.GetString("updated_by"),
			verb:          "deleted",
			field:         "cycles",
			comment:       fmt.Sprintf("removed this issue from %s", name),
			oldValue:      name,
			newValue:      "",
			oldIdentifier: uuid,
		})
		return
	}

	if old, new := orig.GetString("cycle"), row.GetString("cycle"); old != new {
		oldName, oldUUID := resolveName(e.App, "cycles", old)
		newName, newUUID := resolveName(e.App, "cycles", new)
		commitBulk(e.App, activityRow{
			issue:         row.GetString("issue"),
			project:       row.GetString("project"),
			workspace:     row.GetString("workspace"),
			actor:         row.GetString("updated_by"),
			verb:          "updated",
			field:         "cycles",
			comment:       fmt.Sprintf("updated cycle from %s to %s", oldName, newName),
			oldValue:      oldName,
			newValue:      newName,
			oldIdentifier: oldUUID,
			newIdentifier: newUUID,
		})
	}
}

// activityModuleLinked mirrors upstream create_module_issue_activity.
func activityModuleLinked(e *core.RecordEvent) {
	row := e.Record
	name, uuid := resolveName(e.App, "modules", row.GetString("module"))
	commitBulk(e.App, activityRow{
		issue:         row.GetString("issue"),
		project:       row.GetString("project"),
		workspace:     row.GetString("workspace"),
		actor:         row.GetString("created_by"),
		verb:          "created",
		field:         "modules",
		comment:       fmt.Sprintf("added module %s", name),
		oldValue:      "",
		newValue:      name,
		newIdentifier: uuid,
	})
}

// activityModuleLinkChanged is the module twin of activityCycleLinkChanged.
func activityModuleLinkChanged(e *core.RecordEvent) {
	row := e.Record
	orig := row.Original()

	if orig.GetString("deleted_at") == "" && row.GetString("deleted_at") != "" {
		name, uuid := resolveName(e.App, "modules", row.GetString("module"))
		commitBulk(e.App, activityRow{
			issue:         row.GetString("issue"),
			project:       row.GetString("project"),
			workspace:     row.GetString("workspace"),
			actor:         row.GetString("updated_by"),
			verb:          "deleted",
			field:         "modules",
			comment:       fmt.Sprintf("removed this issue from %s", name),
			oldValue:      name,
			newValue:      "",
			oldIdentifier: uuid,
		})
		return
	}

	if old, new := orig.GetString("module"), row.GetString("module"); old != new {
		oldName, oldUUID := resolveName(e.App, "modules", old)
		newName, newUUID := resolveName(e.App, "modules", new)
		commitBulk(e.App, activityRow{
			issue:         row.GetString("issue"),
			project:       row.GetString("project"),
			workspace:     row.GetString("workspace"),
			actor:         row.GetString("updated_by"),
			verb:          "updated",
			field:         "modules",
			comment:       fmt.Sprintf("updated module from %s to %s", oldName, newName),
			oldValue:      oldName,
			newValue:      newName,
			oldIdentifier: oldUUID,
			newIdentifier: newUUID,
		})
	}
}

// ---------------------------------------------------------------------------
// single-record lookups (id -> display name + Plane UUID)
// ---------------------------------------------------------------------------

// resolveName loads one related record by its PocketBase id and returns the
// human-readable value the activity renderers display plus its Plane UUID for
// the *_identifier columns. One FindRecordById — never a list query — and only
// for fields that actually changed.
func resolveName(app core.App, collection, id string) (name, uuid string) {
	if id == "" {
		return "", ""
	}
	record, err := app.FindRecordById(collection, id)
	if err != nil || record == nil {
		return "", ""
	}
	switch collection {
	case "users":
		name = record.GetString("display_name")
	case "estimate_points":
		name = record.GetString("value")
	default:
		name = record.GetString("name")
	}
	return name, serializers.UUID(record)
}

// issueRef renders an issue the way the activity feed links it:
// "{project.identifier}-{sequence_id}" (upstream track_parent /
// create_issue_relation_activity).
func issueRef(app core.App, id string) (ref, uuid string) {
	if id == "" {
		return "", ""
	}
	issue, err := app.FindRecordById("issues", id)
	if err != nil || issue == nil {
		return "", ""
	}
	identifier := ""
	if project, err := app.FindRecordById("projects", issue.GetString("project")); err == nil && project != nil {
		identifier = project.GetString("identifier")
	}
	return fmt.Sprintf("%s-%d", identifier, issue.GetInt("sequence_id")), serializers.UUID(issue)
}

// dateValue renders a PocketBase date column as DRF's DATE_FORMAT, the same
// string the rest of the API exposes for start_date/target_date. Unset -> "".
func dateValue(r *core.Record, field string) string {
	dt := r.GetDateTime(field)
	if dt.IsZero() {
		return ""
	}
	return dt.Time().UTC().Format("2006-01-02")
}
