package hooks

// Notification production (L-4). The router only ever exposed the CONSUMPTION
// side of `notifications` (list / detail / mark-read / unread-count …), so the
// inbox was permanently empty: nothing in the codebase ever inserted a row.
// This file is the missing write path, driven by the same record hooks the
// activity feed uses (see activity.go and docs/06-PITFALLS.md D-12).
//
// Minimal closed loop, exactly as scoped by 11-TASKLIST L-4:
//
//	assigned  — an issue_assignees row is created          (notifyAssigneeCreated)
//	mentioned — a <mention-component> appears in rich text (notifyIssueMentions /
//	            notifyCommentCreated / notifyCommentUpdated)
//	commented — an issue_comments row is created           (notifyCommentCreated)
//
// Everything else (state_change, issue_completed, property_change …) stays a
// TODO upstream-wise and is not generated here.
//
// Three invariants, each with its own guard:
//
//  1. never notify yourself                 — actor == receiver is skipped;
//  2. no duplicate within 5 minutes         — a lookup before insert
//     (receiver + entity_identifier + sender + created_at >= now-5m), NOT a
//     cache, so it survives restarts and covers every write path;
//  3. bounded fan-out                       — a per-save budget (cap) plus a
//     5s coalescing window on (actor, receiver, kind), the same idea as
//     activity.go's commitBulk.

import (
	"fmt"
	"html"
	"sync"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Trigger kinds. These strings land in `notifications.sender`, which the
// consumption side filters on:
//
//   - List/UnreadCount split mention vs non-mention with `sender ~ 'mentioned'`
//     (notification.go), so the mention kind MUST literally contain the
//     substring "mentioned";
//   - `sender` is a plain required text column (max 255), NOT a relation.
const (
	notifyKindAssigned  = "assigned"
	notifyKindMentioned = "mentioned"
	notifyKindCommented = "commented"
)

// notificationDedupWindow is the card-mandated "same receiver + same entity +
// same trigger kind within 5 minutes must not produce a second row" rule.
const notificationDedupWindow = 5 * time.Minute

// notifyFanoutCap bounds how many notification rows ONE triggering save may
// produce. The realistic worst case is a comment on an issue with hundreds of
// subscribers: without a cap a single app.Save would fan out into hundreds of
// rows (and hundreds of follow-up hook firings). Past the cap the remaining
// receivers are dropped with a log line — a lost notification is strictly
// better than a save path that degrades under load.
const notifyFanoutCap = 50

// notifyCoalesceWindow collapses a burst of identical (actor, receiver, kind)
// notifications onto its first row, mirroring activity.go's commitBulk. The
// scenario it defends against: a bulk operation touching 200 issues, each
// producing an "assigned"/"commented" notification for the same person —
// 200 rows where 1 summary row is the honest answer. The window is generous
// relative to a save loop (milliseconds per row) and tiny relative to
// human-paced edits, so two separate single PATCHes that happen to notify the
// same person about the same kind inside 5s collapse into one row — an
// accepted trade-off, identical to the one L-1 made for activities.
const notifyCoalesceWindow = 5 * time.Second

var (
	notifyCoalesceMu sync.Mutex
	notifyCoalesce   = map[notifyCoalesceKey]time.Time{}
)

type notifyCoalesceKey struct {
	actor    string
	receiver string
	kind     string
}

// ---------------------------------------------------------------------------
// notification row under construction
// ---------------------------------------------------------------------------

// notifyRow mirrors the `notifications` columns this pipeline writes
// (spec §3.14). Relation columns (workspace/project/triggered_by/receiver)
// hold PocketBase record ids; entity_identifier holds the ISSUE's Plane UUID
// because that is what the serializer emits and the frontend links to — not
// the PocketBase record id.
type notifyRow struct {
	workspace        string
	project          string
	entityIdentifier string // issue Plane UUID
	issueRef         string // "PROJ-1", for the human-readable text
	actorID          string // triggered_by; "" when unknown (system writes)
	receiver         string
	kind             string
	title            string
	html             string // message_html
	stripped         string // message_stripped
}

// notifier carries the per-save fan-out budget across the receivers of one
// triggering record.
type notifier struct {
	app       core.App
	remaining int
	capped    bool // the cap warning was already logged for this save
}

func newNotifier(app core.App) *notifier {
	return &notifier{app: app, remaining: notifyFanoutCap}
}

// notify writes one notification, applying every guard in order:
// self-notification, preference, 5-minute dedup, 5s coalescing, fan-out cap.
func (n *notifier) notify(row notifyRow) {
	if row.receiver == "" || row.entityIdentifier == "" || row.workspace == "" {
		// Not a Plane-shaped anchor (e.g. a row without its issue). Nothing
		// the inbox could render anyway.
		return
	}

	// Invariant 1: never notify yourself.
	if row.actorID != "" && row.receiver == row.actorID {
		return
	}

	// Preference gate. A missing row means defaults all-on — the same lazy
	// defaults notification.go's preference() seeds on read.
	if !notificationEnabled(n.app, row.receiver, row.kind) {
		return
	}

	// Invariant 2: dedup lookup before insert (not a cache).
	if notificationExistsRecently(n.app, row) {
		return
	}

	// Invariant 3a: coalescing window.
	if notifyCoalesceSuppressed(row) {
		return
	}

	// Invariant 3b: per-save fan-out cap.
	if n.remaining <= 0 {
		if !n.capped {
			n.capped = true
			n.app.Logger().Warn(
				"hooks: notification fan-out cap reached, dropping the rest (main save unaffected)",
				"kind", row.kind, "cap", notifyFanoutCap,
			)
		}
		return
	}
	n.remaining--

	collection, err := n.app.FindCollectionByNameOrId("notifications")
	if err != nil {
		n.app.Logger().Error("hooks: notifications collection missing", "error", err)
		return
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("workspace", row.workspace)
	if row.project != "" {
		record.Set("project", row.project)
	}
	record.Set("data", map[string]any{
		"issue":   row.entityIdentifier,
		"trigger": row.kind,
	})
	record.Set("entity_identifier", row.entityIdentifier)
	// entity_name MUST be "issue": List filters `entity_name = 'issue'`, so
	// anything else would be invisible in the inbox.
	record.Set("entity_name", "issue")
	record.Set("title", row.title)
	record.Set("message", map[string]any{
		"html": row.html,
		"text": row.stripped,
	})
	record.Set("message_html", row.html)
	record.Set("message_stripped", row.stripped)
	record.Set("sender", row.kind)
	if row.actorID != "" {
		record.Set("triggered_by", row.actorID)
		record.Set("created_by", row.actorID)
	}
	record.Set("receiver", row.receiver)
	if err := n.app.Save(record); err != nil {
		// D-12 premise 3: the row that triggered us is already committed;
		// a derived-data failure must never surface as the user's save
		// failing.
		n.app.Logger().Error(
			"hooks: notifications write failed (main save unaffected)",
			"kind", row.kind, "receiver", row.receiver, "error", err,
		)
	}
}

// ---------------------------------------------------------------------------
// guards
// ---------------------------------------------------------------------------

// notificationEnabled reports whether `userID` still wants `kind`
// notifications. A missing preference row means defaults all-on (the handler
// seeds the same all-true defaults lazily; see notification.go preference()).
//
// Upstream has NO dedicated preference column for `assigned` notifications —
// the five booleans are property_change, state_change, comment, mention,
// issue_completed. Choice made here: assignments are gated on
// `property_change`, the catch-all for "something on the issue changed", so a
// user who wants a quiet inbox can mute them while the all-true default keeps
// upstream behaviour. The other two kinds map to their obvious columns.
func notificationEnabled(app core.App, userID, kind string) bool {
	pref, err := app.FindFirstRecordByFilter(
		"user_notification_preferences",
		"user = {:user} && deleted_at = ''",
		dbx.Params{"user": userID},
	)
	if err != nil || pref == nil {
		return true
	}
	switch kind {
	case notifyKindMentioned:
		return pref.GetBool("mention")
	case notifyKindCommented:
		return pref.GetBool("comment")
	case notifyKindAssigned:
		return pref.GetBool("property_change")
	}
	return true
}

// notificationExistsRecently implements the 5-minute dedup as a lookup before
// insert: same receiver + same entity (issue UUID) + same trigger kind
// (`sender`) + not soft-deleted, created inside the window. A lookup rather
// than a cache is deliberate — it must survive restarts and it must cover
// write paths this process did not originate.
func notificationExistsRecently(app core.App, row notifyRow) bool {
	cutoff := time.Now().UTC().Add(-notificationDedupWindow)
	existing, err := app.FindFirstRecordByFilter(
		"notifications",
		"receiver = {:receiver} && entity_identifier = {:entity} && sender = {:kind} && created_at >= {:cutoff} && deleted_at = ''",
		dbx.Params{
			"receiver": row.receiver,
			"entity":   row.entityIdentifier,
			"kind":     row.kind,
			"cutoff":   cutoff,
		},
	)
	return err == nil && existing != nil
}

// notifyCoalesceSuppressed reports whether an identical (actor, receiver, kind)
// notification was already produced inside notifyCoalesceWindow — the
// commitBulk idea from activity.go, applied to the fan-out risk.
func notifyCoalesceSuppressed(row notifyRow) bool {
	key := notifyCoalesceKey{actor: row.actorID, receiver: row.receiver, kind: row.kind}
	now := time.Now()

	notifyCoalesceMu.Lock()
	if len(notifyCoalesce) > 512 {
		for k, at := range notifyCoalesce {
			if now.Sub(at) >= notifyCoalesceWindow {
				delete(notifyCoalesce, k)
			}
		}
	}
	suppressed := false
	if last, ok := notifyCoalesce[key]; ok && now.Sub(last) < notifyCoalesceWindow {
		suppressed = true
	} else {
		notifyCoalesce[key] = now
	}
	notifyCoalesceMu.Unlock()
	return suppressed
}

// ---------------------------------------------------------------------------
// message text
// ---------------------------------------------------------------------------

// assignedMessage renders the "assigned" notification text in the phrasing
// style of activity.go ("added assignee …"), kept short and human-readable so
// the inbox card has something to render.
func assignedMessage(actorName, issueRef string) (title, htmlText, stripped string) {
	title = "Assigned to you"
	if actorName == "" {
		actorName = "Someone"
	}
	return title,
		fmt.Sprintf("<p>%s assigned %s to you</p>", html.EscapeString(actorName), html.EscapeString(issueRef)),
		fmt.Sprintf("%s assigned %s to you", actorName, issueRef)
}

// mentionedMessage renders the "mentioned" notification text.
func mentionedMessage(actorName, issueRef string) (title, htmlText, stripped string) {
	title = "You were mentioned"
	if actorName == "" {
		actorName = "Someone"
	}
	return title,
		fmt.Sprintf("<p>%s mentioned you in %s</p>", html.EscapeString(actorName), html.EscapeString(issueRef)),
		fmt.Sprintf("%s mentioned you in %s", actorName, issueRef)
}

// commentedMessage renders the "commented" notification text.
func commentedMessage(actorName, issueRef string) (title, htmlText, stripped string) {
	title = "New comment"
	if actorName == "" {
		actorName = "Someone"
	}
	return title,
		fmt.Sprintf("<p>%s commented on %s</p>", html.EscapeString(actorName), html.EscapeString(issueRef)),
		fmt.Sprintf("%s commented on %s", actorName, issueRef)
}
