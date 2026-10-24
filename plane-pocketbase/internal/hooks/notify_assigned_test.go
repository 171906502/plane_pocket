package hooks_test

// Evidence item 2: A assigns an issue to B → B's unread count goes 0 → 1 and
// the row carries the fields the inbox renders.

import (
	"testing"

	"github.com/pocketbase/dbx"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// TestNotifyAssignmentProducesUnreadForAssignee drives the production wiring
// (router.Register, exactly what main.go calls) with plain app.Save calls —
// the same calls the handlers make. Assertions:
//
//   - before: B's unread total is 0;
//   - after one issue_assignees row: B's unread total is 1 (mention split 0);
//   - the row is inbox-visible: entity_name = "issue", sender = "assigned",
//     non-empty title/message_html, entity_identifier = the issue's PLANE
//     UUID (not the PocketBase record id), triggered_by = the assigner.
//
// Reverse-verification: remove the `notifyAssigneeCreated(e)` case from
// internal/hooks/hooks.go → the unread assertion FAILS.
func TestNotifyAssignmentProducesUnreadForAssignee(t *testing.T) {
	f := newNotifyFixture(t)
	issue := f.newIssue(f.a) // created before Register: only the assignment must notify
	f.register()

	if total, mention := f.unread(f.b.Id); total != 0 || mention != 0 {
		t.Fatalf("baseline unread for B = (%d, %d), want (0, 0)", total, mention)
	}

	f.assign(f.a, f.b, issue)

	total, mention := f.unread(f.b.Id)
	if total != 1 || mention != 0 {
		t.Fatalf("unread for B after assignment = (%d, %d), want (1, 0)", total, mention)
	}

	rows := f.notifications(f.b.Id)
	if len(rows) != 1 {
		t.Fatalf("B has %d notification rows, want 1", len(rows))
	}
	row := rows[0]
	if got := row.GetString("entity_name"); got != "issue" {
		t.Errorf("entity_name = %q, want %q (List filters on it)", got, "issue")
	}
	if got := row.GetString("sender"); got != "assigned" {
		t.Errorf("sender = %q, want %q", got, "assigned")
	}
	if got := row.GetString("title"); got == "" {
		t.Error("title is empty — the inbox card would render nothing")
	}
	if got := row.GetString("message_html"); got == "" {
		t.Error("message_html is empty — the inbox card would render nothing")
	}
	if got := row.GetString("message_stripped"); got == "" {
		t.Error("message_stripped is empty")
	}
	if got := row.GetString("entity_identifier"); got != serializers.UUID(issue) {
		t.Errorf("entity_identifier = %q, want the issue Plane UUID %q", got, serializers.UUID(issue))
	}
	if got := row.GetString("triggered_by"); got != f.a.Id {
		t.Errorf("triggered_by = %q, want the assigner %q", got, f.a.Id)
	}
	if got := row.GetString("receiver"); got != f.b.Id {
		t.Errorf("receiver = %q, want %q", got, f.b.Id)
	}
	if got := row.GetString("read_at"); got != "" {
		t.Errorf("read_at = %q, want empty (fresh notification is unread)", got)
	}
	if data := row.Get("data"); data == nil {
		t.Error("data (json) is unset")
	}
	if msg := row.Get("message"); msg == nil {
		t.Error("message (json) is unset")
	}
}

// TestNotifyAssignmentIsInvisibleToListWithoutEntityIssue pins the field the
// List endpoint filters on: a notification whose entity_name is anything but
// "issue" never reaches the inbox (notification.go List). The hook writes
// "issue", so the inbox-visible count must equal the row count.
func TestNotifyAssignmentSurfacesInInboxList(t *testing.T) {
	f := newNotifyFixture(t)
	f.register()
	issue := f.newIssue(f.a)
	f.assign(f.a, f.b, issue)

	// List's filter, verbatim from notification.go (workspace + receiver +
	// entity_name + not deleted + not snoozed + not archived).
	rows, err := f.app.FindRecordsByFilter(
		"notifications",
		"workspace = {:workspace} && receiver = {:receiver} && entity_name = 'issue' && deleted_at = '' && (snoozed_till >= {:now} || snoozed_till = '') && archived_at = ''",
		"-created_at", 0, 0,
		dbx.Params{
			"workspace": f.ws.Id,
			"receiver":  f.b.Id,
			"now":       serializers.Now(),
		},
	)
	if err != nil {
		t.Fatalf("inbox list query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("inbox list returned %d rows, want 1", len(rows))
	}
}
