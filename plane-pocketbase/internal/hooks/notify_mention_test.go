package hooks_test

// Evidence item 3: two <mention-component> tags in description_html → 2
// issue_mentions rows (unique per user) + 2 notifications whose sender contains
// "mentioned"; re-saving the SAME html adds nothing (idempotent).

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// mentionTag renders the frontend's mention markup.
func mentionTag(entityName, uuid string) string {
	return fmt.Sprintf(`<mention-component entity_name="%s" entity_identifier="%s"></mention-component>`, entityName, uuid)
}

// TestNotifyMentionsOnIssueCreate drives one issue creation whose
// description_html mentions B and C (plus a duplicate B tag and a non-user
// entity tag that must be ignored). It asserts:
//
//   - 2 issue_mentions rows, unique per (issue, mention);
//   - 2 notifications, one each for B and C, sender containing "mentioned"
//     (the substring List/UnreadCount split on), entity_name = "issue",
//     entity_identifier = the issue's Plane UUID;
//   - re-saving the SAME html adds nothing (no new rows of either kind);
//   - a later edit that adds a NEW mention notifies exactly that user.
//
// Reverse-verification: remove the `notifyIssueMentions(e)` cases from
// internal/hooks/hooks.go → the mention-row assertions FAIL.
func TestNotifyMentionsOnIssueCreate(t *testing.T) {
	f := newNotifyFixture(t)
	d := f.user("actor-d", "Actor D")
	f.register()

	html := "<p>hi " + mentionTag("user_mention", serializers.UUID(f.b)) +
		" and " + mentionTag("user_mention", serializers.UUID(f.c)) +
		" again " + mentionTag("user_mention", serializers.UUID(f.b)) + // duplicate of B
		" plus " + mentionTag("project_mention", serializers.UUID(f.project)) + // not a user
		"</p>"

	issue := f.save("issues", func(r *core.Record) {
		r.Set("name", "with mentions")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("state", f.state.Id)
		r.Set("created_by", f.a.Id)
		r.Set("sequence_id", 1)
		r.Set("description_html", html)
	})

	// 2 issue_mentions rows, unique per user.
	mentions := f.mentionRows(issue)
	if len(mentions) != 2 {
		t.Fatalf("issue_mentions rows = %d, want 2 (unique per user)", len(mentions))
	}
	mentioned := map[string]bool{}
	for _, row := range mentions {
		mentioned[row.GetString("mention")] = true
	}
	if !mentioned[f.b.Id] || !mentioned[f.c.Id] {
		t.Errorf("issue_mentions users = %v, want B and C", mentioned)
	}

	// 2 notifications, both mention-flavoured.
	for _, receiver := range []*core.Record{f.b, f.c} {
		rows := f.notifications(receiver.Id)
		if len(rows) != 1 {
			t.Fatalf("%s has %d notifications, want 1", receiver.GetString("username"), len(rows))
		}
		row := rows[0]
		if !strings.Contains(row.GetString("sender"), "mentioned") {
			t.Errorf("sender = %q, want it to contain %q", row.GetString("sender"), "mentioned")
		}
		if got := row.GetString("entity_name"); got != "issue" {
			t.Errorf("entity_name = %q, want issue", got)
		}
		if got := row.GetString("entity_identifier"); got != serializers.UUID(issue) {
			t.Errorf("entity_identifier = %q, want the issue Plane UUID %q", got, serializers.UUID(issue))
		}
		if got := row.GetString("message_html"); got == "" {
			t.Error("message_html is empty")
		}
	}
	if total, mention := f.unread(f.b.Id); total != 0 || mention != 1 {
		t.Errorf("B unread split = (%d, %d), want (0, 1) — a mention is not an 'other'", total, mention)
	}

	// Idempotent: re-saving the SAME html adds nothing.
	reloaded, err := f.app.FindRecordById("issues", issue.Id)
	if err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	reloaded.Set("description_html", html)
	reloaded.Set("updated_by", f.a.Id)
	if err := f.app.Save(reloaded); err != nil {
		t.Fatalf("re-save issue: %v", err)
	}
	if got := len(f.mentionRows(issue)); got != 2 {
		t.Errorf("issue_mentions rows after re-save = %d, want 2 (idempotent)", got)
	}
	for _, receiver := range []*core.Record{f.b, f.c} {
		if got := len(f.notifications(receiver.Id)); got != 1 {
			t.Errorf("%s has %d notifications after re-save, want 1 (idempotent)",
				receiver.GetString("username"), got)
		}
	}

	// A later edit adding a NEW mention notifies exactly that user.
	reloaded2, err := f.app.FindRecordById("issues", issue.Id)
	if err != nil {
		t.Fatalf("reload issue again: %v", err)
	}
	reloaded2.Set("description_html", html+mentionTag("user_mention", serializers.UUID(d)))
	reloaded2.Set("updated_by", f.a.Id)
	if err := f.app.Save(reloaded2); err != nil {
		t.Fatalf("save issue with new mention: %v", err)
	}
	if got := len(f.mentionRows(issue)); got != 3 {
		t.Errorf("issue_mentions rows after adding a mention = %d, want 3", got)
	}
	if got := len(f.notifications(d.Id)); got != 1 {
		t.Errorf("D has %d notifications, want 1 (the newly added mention)", got)
	}
	for _, receiver := range []*core.Record{f.b, f.c} {
		if got := len(f.notifications(receiver.Id)); got != 1 {
			t.Errorf("%s has %d notifications after the edit, want 1 (no re-notify)",
				receiver.GetString("username"), got)
		}
	}
}

// TestNotifyMentionsInsideComment covers the comment half of the mention
// contract: a <mention-component> inside issue_comments.comment_html books an
// issue_mentions row and notifies the mentioned user with a "mentioned" sender.
func TestNotifyMentionsInsideComment(t *testing.T) {
	f := newNotifyFixture(t)
	f.register()
	issue := f.newIssue(f.a)

	f.comment(f.a, issue, "<p>ping "+mentionTag("user_mention", serializers.UUID(f.b))+"</p>")

	if got := len(f.mentionRows(issue)); got != 1 {
		t.Fatalf("issue_mentions rows from a comment = %d, want 1", got)
	}
	rows := f.notifications(f.b.Id)
	if len(rows) != 1 {
		t.Fatalf("B has %d notifications, want 1", len(rows))
	}
	if !strings.Contains(rows[0].GetString("sender"), "mentioned") {
		t.Errorf("sender = %q, want it to contain mentioned", rows[0].GetString("sender"))
	}
}
