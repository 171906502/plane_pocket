package hooks_test

// Evidence item 5: fan-out stays bounded and never errors.

import (
	"fmt"
	"testing"

	"github.com/pocketbase/dbx"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// notifyFanoutCapMirror mirrors the cap documented in notify.go. If that
// constant changes, this test must change with it — the point is that the cap
// EXISTS and is enforced, not its exact value.
const notifyFanoutCapMirror = 50

func fmtSubscriberName(i int) string { return fmt.Sprintf("sub-%03d", i) }

func dbxIssueParam(issueUUID string) dbx.Params {
	return dbx.Params{"issue": issueUUID}
}

func dbxReceiverParam(receiver string) dbx.Params {
	return dbx.Params{"receiver": receiver}
}

// TestNotifyFanoutCapPerSave: one comment on an issue with 200 subscribers
// must produce at most notifyFanoutCap notifications, and the comment save
// itself must not error (a derived-data failure can never fail the user's
// action — D-12 premise 3).
//
// Reverse-verification: remove the `n.remaining` budget check in notify.go's
// notify() → 200 rows are written and this test FAILS.
func TestNotifyFanoutCapPerSave(t *testing.T) {
	f := newNotifyFixture(t)
	f.register()
	issue := f.newIssue(f.a)

	for i := 0; i < 200; i++ {
		subscriber := f.user(fmtSubscriberName(i), "")
		f.subscribe(subscriber, issue)
	}

	// The save itself must succeed even though the fan-out is 200.
	f.comment(f.a, issue, "<p>bulk ping</p>")

	rows, err := f.app.FindRecordsByFilter(
		"notifications",
		"entity_identifier = {:issue} && sender = 'commented' && deleted_at = ''",
		"", 0, 0,
		dbxIssueParam(serializers.UUID(issue)),
	)
	if err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	if len(rows) != notifyFanoutCapMirror {
		t.Fatalf("notifications from one comment = %d, want the cap %d", len(rows), notifyFanoutCapMirror)
	}
}

// TestNotifyFanoutCoalescesBurst: a bulk operation assigning the SAME user to
// 200 different issues in a tight loop must collapse into ONE notification —
// the commitBulk idea from activity.go applied to notifications. 200 rows
// where 1 is the honest answer; the alternative (200 rows for one batch) is
// exactly the flood the card calls 中高.
//
// Reverse-verification: remove the notifyCoalesceSuppressed() call in
// notify.go's notify() → 200 rows are written and this test FAILS.
func TestNotifyFanoutCoalescesBurst(t *testing.T) {
	f := newNotifyFixture(t)
	f.register()

	for i := 0; i < 200; i++ {
		issue := f.newIssue(f.a)
		f.assign(f.a, f.b, issue)
	}

	rows, err := f.app.FindRecordsByFilter(
		"notifications",
		"receiver = {:receiver} && sender = 'assigned' && deleted_at = ''",
		"", 0, 0,
		dbxReceiverParam(f.b.Id),
	)
	if err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("B has %d assignment notifications from a 200-issue burst, want 1 (coalesced)", len(rows))
	}
}
