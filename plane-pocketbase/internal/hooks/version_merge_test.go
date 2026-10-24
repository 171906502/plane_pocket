package hooks

import (
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// mergeClock is the fixed start every merge-window test shares.
var mergeClock = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// TestMergeWindowSameUserRefreshesInPlace is the throttle contract: two saves
// by the same user inside PAGE_VERSION_TASK_TIMEOUT (600s) must land on ONE
// version row, refreshed in place — not two rows. Collaborative editing
// PATCHes description_html every ~1.5s, so without this the 20-version cap is
// exhausted in half a minute.
func TestMergeWindowSameUserRefreshesInPlace(t *testing.T) {
	g := newVersionGraph(t)
	Register(g.app)
	advance := useClock(t, mergeClock)

	g.editPage(t, g.user, func(r *core.Record) { r.Set("description_html", "<p>a</p>") })
	first := g.pageVersions(t)
	if len(first) != 1 {
		t.Fatalf("first save produced %d version rows, want 1", len(first))
	}

	advance(60 * time.Second) // well inside the 600s window
	g.editPage(t, g.user, func(r *core.Record) { r.Set("description_html", "<p>b</p>") })

	rows := g.pageVersions(t)
	if len(rows) != 1 {
		t.Fatalf("same-user save inside the merge window produced %d rows, want 1 (refreshed)", len(rows))
	}
	if rows[0].Id != first[0].Id {
		t.Fatalf("second save inserted a new row (%s) instead of updating %s", rows[0].Id, first[0].Id)
	}
	if got := rows[0].GetString("description_html"); got != "<p>b</p>" {
		t.Fatalf("merged row html = %q, want the refreshed content %q", got, "<p>b</p>")
	}
	if got := rows[0].GetDateTime("last_saved_at").Time(); !got.Equal(mergeClock.Add(60 * time.Second)) {
		t.Fatalf("merged row last_saved_at = %s, want the second save's timestamp", got)
	}
	if got := rows[0].GetString("owned_by"); got != g.user.Id {
		t.Fatalf("merged row owned_by = %q, want %q", got, g.user.Id)
	}
}

// TestMergeWindowBoundary is the exact edge: 600s counts as inside, 601s does
// not. Upstream's condition is `now - last_saved_at <= PAGE_VERSION_TASK_TIMEOUT`.
func TestMergeWindowBoundary(t *testing.T) {
	cases := []struct {
		name  string
		shift time.Duration
		want  int
	}{
		{"exactly 600s merges", versionMergeWindow, 1},
		{"601s inserts", versionMergeWindow + time.Second, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newVersionGraph(t)
			Register(g.app)
			advance := useClock(t, mergeClock)

			g.editPage(t, g.user, func(r *core.Record) { r.Set("description_html", "<p>a</p>") })
			advance(tc.shift)
			g.editPage(t, g.user, func(r *core.Record) { r.Set("description_html", "<p>b</p>") })

			if rows := g.pageVersions(t); len(rows) != tc.want {
				t.Fatalf("shift %s produced %d rows, want %d", tc.shift, len(rows), tc.want)
			}
		})
	}
}

// TestMergeWindowExpiryInsertsNewRow: once the window has passed, the next save
// by the same user starts a new version instead of refreshing the stale one.
func TestMergeWindowExpiryInsertsNewRow(t *testing.T) {
	g := newVersionGraph(t)
	Register(g.app)
	advance := useClock(t, mergeClock)

	g.editPage(t, g.user, func(r *core.Record) { r.Set("description_html", "<p>a</p>") })
	advance(601 * time.Second)
	g.editPage(t, g.user, func(r *core.Record) { r.Set("description_html", "<p>b</p>") })

	rows := g.pageVersions(t)
	if len(rows) != 2 {
		t.Fatalf("same-user save outside the merge window produced %d rows, want 2", len(rows))
	}
	// Newest first: the fresh row carries the new content, the old one keeps
	// its own.
	if got := rows[0].GetString("description_html"); got != "<p>b</p>" {
		t.Fatalf("newest row html = %q, want %q", got, "<p>b</p>")
	}
	if got := rows[1].GetString("description_html"); got != "<p>a</p>" {
		t.Fatalf("older row html = %q, want %q", got, "<p>a</p>")
	}
}

// TestMergeWindowDifferentUserInsertsNewRow: the merge is per user. A second
// editor saving inside the window must not overwrite the first editor's
// version — it starts its own.
func TestMergeWindowDifferentUserInsertsNewRow(t *testing.T) {
	g := newVersionGraph(t)
	Register(g.app)
	advance := useClock(t, mergeClock)

	g.editPage(t, g.user, func(r *core.Record) { r.Set("description_html", "<p>a</p>") })
	advance(60 * time.Second)
	g.editPage(t, g.other, func(r *core.Record) { r.Set("description_html", "<p>b</p>") })

	rows := g.pageVersions(t)
	if len(rows) != 2 {
		t.Fatalf("different-user save produced %d rows, want 2", len(rows))
	}
	if got := rows[0].GetString("owned_by"); got != g.other.Id {
		t.Fatalf("newest row owned_by = %q, want the second actor %q", got, g.other.Id)
	}
	if got := rows[1].GetString("owned_by"); got != g.user.Id {
		t.Fatalf("older row owned_by = %q, want %q", got, g.user.Id)
	}
	if got := rows[0].GetString("description_html"); got != "<p>b</p>" {
		t.Fatalf("second actor's row html = %q, want %q", got, "<p>b</p>")
	}
}

// TestIssueMergeWindowSameRule confirms the issue collection applies the same
// throttle (the shared pipeline, but worth pinning per collection).
func TestIssueMergeWindowSameRule(t *testing.T) {
	g := newVersionGraph(t)
	Register(g.app)
	advance := useClock(t, mergeClock)

	g.editIssue(t, g.user, func(r *core.Record) { r.Set("description_html", "<p>a</p>") })
	advance(120 * time.Second)
	g.editIssue(t, g.user, func(r *core.Record) { r.Set("description_html", "<p>b</p>") })

	rows := g.issueVersions(t)
	if len(rows) != 1 {
		t.Fatalf("issue merge window: %d rows, want 1", len(rows))
	}
	if got := rows[0].GetString("description_html"); got != "<p>b</p>" {
		t.Fatalf("issue merged row html = %q, want %q", got, "<p>b</p>")
	}
}
