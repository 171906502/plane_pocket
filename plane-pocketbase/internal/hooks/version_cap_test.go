package hooks

import (
	"fmt"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// TestVersionCapKeepsTwentyNewest is the disk-safety contract: after each
// insert the collection is capped at 20, evicting the oldest last_saved_at
// first. 25 saves by 25 different users (601s apart, so every save inserts)
// must leave exactly the 20 newest survivors.
//
// Without the cap the count would be 25; without the "oldest first" order the
// survivor set would not be the newest saves.
func TestVersionCapKeepsTwentyNewest(t *testing.T) {
	g := newVersionGraph(t)
	Register(g.app)
	advance := useClock(t, time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))

	const saves = 25
	users := make([]*core.Record, 0, saves)
	for i := 0; i < saves; i++ {
		users = append(users, saveVersionRecord(t, g.app, "users", func(r *core.Record) {
			r.Set("username", fmt.Sprintf("cap%02d", i))
			r.Set("email", fmt.Sprintf("cap%02d@example.com", i))
			r.Set("password", "1234567890")
			r.Set("last_active", "2024-01-01 00:00:00.000Z")
		}))
	}

	for i, actor := range users {
		html := fmt.Sprintf("<p>save-%02d</p>", i+1)
		g.editPage(t, actor, func(r *core.Record) { r.Set("description_html", html) })
		advance(601 * time.Second) // outside the merge window: every save inserts
	}

	rows := g.pageVersions(t) // newest first
	if len(rows) != versionKeep {
		t.Fatalf("cap left %d version rows, want %d", len(rows), versionKeep)
	}
	for i, row := range rows {
		// rows[0] is save 25, rows[19] is save 6: the newest 20 must survive.
		want := fmt.Sprintf("<p>save-%02d</p>", saves-i)
		if got := row.GetString("description_html"); got != want {
			t.Fatalf("survivor %d = %s, want %s (the newest %d saves must survive)", i, got, want, versionKeep)
		}
	}
}

// TestVersionCapIssueCollection pins the cap on the issue side too, with a
// smaller but still over-the-limit burst.
func TestVersionCapIssueCollection(t *testing.T) {
	g := newVersionGraph(t)
	Register(g.app)
	advance := useClock(t, time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))

	const saves = versionKeep + 3
	for i := 0; i < saves; i++ {
		html := fmt.Sprintf("<p>issue-%02d</p>", i+1)
		g.editIssue(t, g.other, func(r *core.Record) { r.Set("description_html", html) })
		advance(601 * time.Second)
	}

	rows := g.issueVersions(t)
	if len(rows) != versionKeep {
		t.Fatalf("issue cap left %d rows, want %d", len(rows), versionKeep)
	}
	for i, row := range rows {
		want := fmt.Sprintf("<p>issue-%02d</p>", saves-i)
		if got := row.GetString("description_html"); got != want {
			t.Fatalf("issue survivor %d = %s, want %s", i, got, want)
		}
	}
}

// TestVersionCapMergeDoesNotEvict guards the interaction that matters for
// collaborative editing: rapid same-user saves merge into one row and must NOT
// trip the cap (an eviction on every merge would eat the user's own history).
func TestVersionCapMergeDoesNotEvict(t *testing.T) {
	g := newVersionGraph(t)
	Register(g.app)
	advance := useClock(t, time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))

	for i := 0; i < 30; i++ {
		html := fmt.Sprintf("<p>keystroke-%02d</p>", i)
		g.editPage(t, g.user, func(r *core.Record) { r.Set("description_html", html) })
		advance(2 * time.Second) // the editor's ~1.5s cadence, inside the window
	}

	rows := g.pageVersions(t)
	if len(rows) != 1 {
		t.Fatalf("30 rapid same-user saves produced %d rows, want 1 (all merged)", len(rows))
	}
	if got := rows[0].GetString("description_html"); got != "<p>keystroke-29</p>" {
		t.Fatalf("merged row html = %q, want the last keystroke", got)
	}
}
