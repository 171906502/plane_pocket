package hooks_test

// Evidence item 4: self-action produces nothing; a second identical action
// inside 5 minutes produces nothing; a user with the preference turned off
// produces nothing.

import (
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// preference saves a user_notification_preferences row with the given column
// overrides; every other column stays false, which is fine because the hook
// only ever reads the column for the kind it is about to send.
func (f *notifyFixture) preference(user *core.Record, overrides map[string]bool) *core.Record {
	return f.save("user_notification_preferences", func(r *core.Record) {
		r.Set("user", user.Id)
		r.Set("workspace", f.ws.Id)
		for column, value := range overrides {
			r.Set(column, value)
		}
	})
}

// TestNotifyNeverNotifiesSelf: A assigns an issue to A (self-assignment is a
// legal issue_assignees row) — the inbox stays empty.
//
// Reverse-verification: remove the `row.receiver == row.actorID` guard in
// notify.go's notify() → this test FAILS.
func TestNotifyNeverNotifiesSelf(t *testing.T) {
	f := newNotifyFixture(t)
	f.register()
	issue := f.newIssue(f.a)

	f.assign(f.a, f.a, issue) // actor == receiver

	if got := len(f.notifications(f.a.Id)); got != 0 {
		t.Fatalf("A has %d notifications after self-assignment, want 0", got)
	}
}

// TestNotifyDedupWithinFiveMinutes: the same receiver + same entity + same
// trigger kind must not produce a second row within 5 minutes.
//
//   - two identical comments by A inside the window -> 1 notification;
//   - a comment by a DIFFERENT actor C inside the same window -> still 1
//     notification. The second half is what isolates the 5-minute dedup
//     lookup: the coalescing window is keyed on (actor, receiver, kind) and
//     cannot explain the suppression when the actor changes.
//
// Reverse-verification: remove the notificationExistsRecently() call in
// notify.go's notify() → the C-comment half produces a second row and this
// test FAILS.
func TestNotifyDedupWithinFiveMinutes(t *testing.T) {
	f := newNotifyFixture(t)
	f.register()
	issue := f.newIssue(f.a)
	f.subscribe(f.b, issue)

	f.comment(f.a, issue, "<p>first</p>")
	f.comment(f.a, issue, "<p>second</p>")
	f.comment(f.c, issue, "<p>third</p>")

	rows := f.notifications(f.b.Id)
	if len(rows) != 1 {
		t.Fatalf("B has %d notifications after three comments, want 1 (5-minute dedup)", len(rows))
	}
	if got := rows[0].GetString("sender"); got != "commented" {
		t.Errorf("sender = %q, want %q", got, "commented")
	}
}

// TestNotifyRespectsPreferences: a user who turned off the matching preference
// gets no notification of that kind. The issue_mentions bookkeeping row is
// still written — the mention exists in the text; only the notification is
// gated.
//
// Reverse-verification: remove the notificationEnabled() call in notify.go's
// notify() → both halves FAIL.
func TestNotifyRespectsPreferences(t *testing.T) {
	f := newNotifyFixture(t)
	f.register()

	// mention = false: an issue mentioning B produces no notification.
	f.preference(f.b, map[string]bool{"mention": false})
	mentioned := f.save("issues", func(r *core.Record) {
		r.Set("name", "muted mention")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("state", f.state.Id)
		r.Set("created_by", f.a.Id)
		r.Set("sequence_id", 1)
		r.Set("description_html", "<p>ping "+mentionTag("user_mention", serializers.UUID(f.b))+"</p>")
	})
	if got := len(f.mentionRows(mentioned)); got != 1 {
		t.Errorf("issue_mentions rows = %d, want 1 (bookkeeping is not preference-gated)", got)
	}
	if got := len(f.notifications(f.b.Id)); got != 0 {
		t.Errorf("B has %d notifications with mention=false, want 0", got)
	}

	// comment = false: C subscribes to an issue, A comments — nothing for C.
	f.preference(f.c, map[string]bool{"comment": false})
	issue := f.newIssue(f.a)
	f.subscribe(f.c, issue)
	f.comment(f.a, issue, "<p>hello</p>")
	if got := len(f.notifications(f.c.Id)); got != 0 {
		t.Errorf("C has %d notifications with comment=false, want 0", got)
	}

	// assigned gated on property_change (documented choice in notify.go):
	// B turns property_change off -> no assignment notification either.
	f.preference(f.b, map[string]bool{"mention": true, "property_change": false})
	issue2 := f.newIssue(f.a)
	f.assign(f.a, f.b, issue2)
	if got := len(f.notifications(f.b.Id)); got != 0 {
		t.Errorf("B has %d notifications with property_change=false, want 0", got)
	}
}

// TestNotifyMissingPreferenceRowDefaultsAllOn mirrors notification.go's lazy
// seeding: a user WITHOUT a preference row still receives every kind.
func TestNotifyMissingPreferenceRowDefaultsAllOn(t *testing.T) {
	f := newNotifyFixture(t)
	f.register()

	// No user_notification_preferences row exists for B anywhere in this test.
	rows, err := f.app.FindRecordsByFilter(
		"user_notification_preferences",
		"user = {:user} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"user": f.b.Id},
	)
	if err != nil {
		t.Fatalf("preference lookup: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("fixture leak: %d preference rows for B, want 0", len(rows))
	}

	issue := f.newIssue(f.a)
	f.assign(f.a, f.b, issue)
	if got := len(f.notifications(f.b.Id)); got != 1 {
		t.Fatalf("B has %d notifications without a preference row, want 1 (defaults all-on)", got)
	}
}
