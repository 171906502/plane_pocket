package hooks

// Mention parsing + the three L-4 trigger hooks:
//
//	notifyAssigneeCreated — assigned
//	notifyCommentCreated  — commented (+ comment mentions)
//	notifyCommentUpdated  — comment mentions added by an edit
//	notifyIssueMentions   — issue description mentions (create + update)
//
// Mention markup contract (frontend editor): the description/comment HTML
// contains
//
//	<mention-component entity_name="user_mention" entity_identifier="<user uuid>">
//
// where entity_identifier is the mentioned user's PLANE UUID (the API id, not
// the PocketBase record id) — resolved back with serializers.FindByUUID.

import (
	"regexp"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

var (
	mentionTagPattern       = regexp.MustCompile(`<mention-component\b[^>]*>`)
	entityNamePattern       = regexp.MustCompile(`entity_name\s*=\s*"([^"]*)"`)
	entityIdentifierPattern = regexp.MustCompile(`entity_identifier\s*=\s*"([^"]*)"`)
)

// parseUserMentionUUIDs extracts the mentioned USER Plane UUIDs from one
// rich-text blob, in first-seen order and deduplicated. Only
// entity_name="user_mention" components count (the same tag shape is used for
// other entity kinds upstream); a tag without entity_identifier is skipped.
func parseUserMentionUUIDs(richText string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, tag := range mentionTagPattern.FindAllString(richText, -1) {
		nameMatch := entityNamePattern.FindStringSubmatch(tag)
		if len(nameMatch) != 2 || nameMatch[1] != "user_mention" {
			continue
		}
		idMatch := entityIdentifierPattern.FindStringSubmatch(tag)
		if len(idMatch) != 2 || idMatch[1] == "" {
			continue
		}
		if _, ok := seen[idMatch[1]]; ok {
			continue
		}
		seen[idMatch[1]] = struct{}{}
		out = append(out, idMatch[1])
	}
	return out
}

// addedMentionUUIDs diffs two rich-text blobs and returns the user UUIDs that
// appear in `newText` but not in `oldText`. On create, oldText is blank (a
// core.NewRecord-built record has a blank Original() — D-12 premise 2), so
// every mention in the new text counts as added. Re-saving the SAME html
// yields an empty diff, which is what makes the whole path idempotent.
func addedMentionUUIDs(oldText, newText string) []string {
	old := map[string]struct{}{}
	for _, uuid := range parseUserMentionUUIDs(oldText) {
		old[uuid] = struct{}{}
	}
	out := []string{}
	for _, uuid := range parseUserMentionUUIDs(newText) {
		if _, ok := old[uuid]; ok {
			continue
		}
		out = append(out, uuid)
	}
	return out
}

// mentionContext is everything a mention notification needs about its issue.
type mentionContext struct {
	issueID   string // PocketBase id
	issueUUID string // Plane UUID (entity_identifier)
	issueRef  string // "PROJ-1"
	project   string
	workspace string
	actorID   string
}

// notifyMentions resolves the mentioned users, books one issue_mentions row
// per (issue, user) and notifies each of them.
func notifyMentions(app core.App, ctx mentionContext, uuids []string) {
	if len(uuids) == 0 || ctx.issueID == "" || ctx.workspace == "" {
		return
	}
	actorName := ""
	if ctx.actorID != "" {
		actorName, _ = resolveName(app, "users", ctx.actorID)
	}
	title, htmlText, stripped := mentionedMessage(actorName, ctx.issueRef)

	n := newNotifier(app)
	for _, uuid := range uuids {
		user, err := serializers.FindByUUID(app, "users", uuid)
		if err != nil || user == nil {
			continue
		}
		writeIssueMention(app, ctx, user.Id)
		n.notify(notifyRow{
			workspace:        ctx.workspace,
			project:          ctx.project,
			entityIdentifier: ctx.issueUUID,
			issueRef:         ctx.issueRef,
			actorID:          ctx.actorID,
			receiver:         user.Id,
			kind:             notifyKindMentioned,
			title:            title,
			html:             htmlText,
			stripped:         stripped,
		})
	}
}

// writeIssueMention books one issue_mentions row (Plane's IssueMention model:
// issue, mention, project, workspace). The partial UNIQUE index on
// (issue, mention) WHERE deleted_at = ” is guarded by a lookup first, so an
// existing live row is left alone and re-saving the same html adds nothing.
// Rows are never removed here (upstream keeps them; removal is out of L-4's
// scope), which is also why the unique index matters more than a diff.
func writeIssueMention(app core.App, ctx mentionContext, mentionID string) {
	existing, err := app.FindFirstRecordByFilter(
		"issue_mentions",
		"issue = {:issue} && mention = {:mention} && deleted_at = ''",
		dbx.Params{"issue": ctx.issueID, "mention": mentionID},
	)
	if err == nil && existing != nil {
		return
	}
	collection, err := app.FindCollectionByNameOrId("issue_mentions")
	if err != nil {
		app.Logger().Error("hooks: issue_mentions collection missing", "error", err)
		return
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("issue", ctx.issueID)
	record.Set("mention", mentionID)
	record.Set("project", ctx.project)
	record.Set("workspace", ctx.workspace)
	if ctx.actorID != "" {
		record.Set("created_by", ctx.actorID)
	}
	if err := app.Save(record); err != nil {
		app.Logger().Error(
			"hooks: issue_mentions write failed (main save unaffected)",
			"issue", ctx.issueID, "mention", mentionID, "error", err,
		)
	}
}

// ---------------------------------------------------------------------------
// assigned — issue_assignees row created
// ---------------------------------------------------------------------------

// notifyAssigneeCreated mirrors the structure of activityAssigneeAdded: an
// issue_assignees create IS the assignment, so it notifies the assignee.
// Removals go through app.Delete (hard delete), which is outside the
// sanctioned hook set — same known limitation as the L-1 activity hook.
func notifyAssigneeCreated(e *core.RecordEvent) {
	row := e.Record
	ref, issueUUID := issueRef(e.App, row.GetString("issue"))
	if issueUUID == "" {
		return // dangling issue reference — nothing to anchor to
	}
	workspace := row.GetString("workspace")
	project := row.GetString("project")
	if workspace == "" || project == "" {
		if issue, err := e.App.FindRecordById("issues", row.GetString("issue")); err == nil && issue != nil {
			if workspace == "" {
				workspace = issue.GetString("workspace")
			}
			if project == "" {
				project = issue.GetString("project")
			}
		}
	}

	actorID := row.GetString("created_by")
	actorName := ""
	if actorID != "" {
		actorName, _ = resolveName(e.App, "users", actorID)
	}
	title, htmlText, stripped := assignedMessage(actorName, ref)

	newNotifier(e.App).notify(notifyRow{
		workspace:        workspace,
		project:          project,
		entityIdentifier: issueUUID,
		issueRef:         ref,
		actorID:          actorID,
		receiver:         row.GetString("assignee"),
		kind:             notifyKindAssigned,
		title:            title,
		html:             htmlText,
		stripped:         stripped,
	})
}

// ---------------------------------------------------------------------------
// commented — issue_comments row created
// ---------------------------------------------------------------------------

// notifyCommentCreated notifies everyone who should hear about a new comment:
// the issue's subscribers, or — when the issue has no subscriber rows at all —
// its assignees plus its creator (see commentReceivers). Mentions inside the
// comment are handled by the same call.
func notifyCommentCreated(e *core.RecordEvent) {
	comment := e.Record
	app := e.App
	ref, issueUUID := issueRef(app, comment.GetString("issue"))
	if issueUUID == "" {
		return
	}
	workspace := comment.GetString("workspace")
	project := comment.GetString("project")
	actorID := comment.GetString("actor")
	actorName := ""
	if actorID != "" {
		actorName, _ = resolveName(app, "users", actorID)
	}
	title, htmlText, stripped := commentedMessage(actorName, ref)

	n := newNotifier(app)
	for _, receiver := range commentReceivers(app, comment) {
		n.notify(notifyRow{
			workspace:        workspace,
			project:          project,
			entityIdentifier: issueUUID,
			issueRef:         ref,
			actorID:          actorID,
			receiver:         receiver,
			kind:             notifyKindCommented,
			title:            title,
			html:             htmlText,
			stripped:         stripped,
		})
	}

	notifyMentions(app, mentionContext{
		issueID:   comment.GetString("issue"),
		issueUUID: issueUUID,
		issueRef:  ref,
		project:   project,
		workspace: workspace,
		actorID:   actorID,
	}, parseUserMentionUUIDs(comment.GetString("comment_html")))
}

// notifyCommentUpdated only notifies about mentions ADDED by the edit (diff
// against Original().comment_html). An edit never re-notifies subscribers —
// the "commented" notification belongs to the creation.
func notifyCommentUpdated(e *core.RecordEvent) {
	comment := e.Record
	added := addedMentionUUIDs(comment.Original().GetString("comment_html"), comment.GetString("comment_html"))
	if len(added) == 0 {
		return
	}
	ref, issueUUID := issueRef(e.App, comment.GetString("issue"))
	if issueUUID == "" {
		return
	}
	notifyMentions(e.App, mentionContext{
		issueID:   comment.GetString("issue"),
		issueUUID: issueUUID,
		issueRef:  ref,
		project:   comment.GetString("project"),
		workspace: comment.GetString("workspace"),
		actorID:   comment.GetString("actor"),
	}, added)
}

// commentReceivers resolves who hears about a new comment on an issue: the
// live issue_subscribers, excluding the commenter. When the issue has NO
// subscriber rows at all — subscriptions only exist through the explicit
// subscribe endpoints, and a freshly created issue normally has none — fall
// back to the issue's assignees plus its creator, so the first comment on an
// issue is never silent. When subscribers DO exist, only they are notified
// (a subscriber list that happens to contain just the commenter yields
// nobody).
func commentReceivers(app core.App, comment *core.Record) []string {
	commenter := comment.GetString("actor")
	seen := map[string]struct{}{}
	out := []string{}
	add := func(id string) {
		if id == "" || id == commenter {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}

	subscribers, err := app.FindRecordsByFilter(
		"issue_subscribers",
		"issue = {:issue} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"issue": comment.GetString("issue")},
	)
	if err == nil && len(subscribers) > 0 {
		for _, row := range subscribers {
			add(row.GetString("subscriber"))
		}
		return out
	}

	assignees, err := app.FindRecordsByFilter(
		"issue_assignees",
		"issue = {:issue} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"issue": comment.GetString("issue")},
	)
	if err == nil {
		for _, row := range assignees {
			add(row.GetString("assignee"))
		}
	}
	if issue, err := app.FindRecordById("issues", comment.GetString("issue")); err == nil && issue != nil {
		add(issue.GetString("created_by"))
	}
	return out
}

// ---------------------------------------------------------------------------
// mentioned — issue description
// ---------------------------------------------------------------------------

// notifyIssueMentions handles mentions in issues.description_html on both
// create and update. On create every parsed mention is new (Original() is
// blank for NewRecord-built records — D-12 premise 2); on update only the
// mentions ADDED since Original().description_html notify, so re-saving the
// same html is a no-op.
//
// TODO(L-4 follow-ups, deliberately out of the minimal closed loop):
// state_change, issue_completed and property_change notifications.
func notifyIssueMentions(e *core.RecordEvent) {
	issue := e.Record
	added := addedMentionUUIDs(issue.Original().GetString("description_html"), issue.GetString("description_html"))
	if len(added) == 0 {
		return
	}
	ref, issueUUID := issueRef(e.App, issue.Id)
	if issueUUID == "" {
		return
	}
	actorID := issue.GetString("updated_by")
	if actorID == "" {
		actorID = issue.GetString("created_by")
	}
	notifyMentions(e.App, mentionContext{
		issueID:   issue.Id,
		issueUUID: issueUUID,
		issueRef:  ref,
		project:   issue.GetString("project"),
		workspace: issue.GetString("workspace"),
		actorID:   actorID,
	}, added)
}
