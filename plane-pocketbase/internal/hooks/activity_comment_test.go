package hooks

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// TestActivityCommentCreated: creating an issue_comments row writes one
// verb="comment" activity carrying the comment html and UUID, and pointing at
// the comment via issue_comment. field="comment" keeps it out of every
// property feed (wave2.go / user_profile.go / activity_export.go all filter
// `field != "comment"`) — it only surfaces in the dashboard widget, same as
// upstream.
func TestActivityCommentCreated(t *testing.T) {
	g := newProbeGraph(t)
	Register(g.app)

	c, _ := g.app.FindCollectionByNameOrId("issue_comments")
	comment := core.NewRecord(c)
	comment.Set("uuid", serializers.NewUUID())
	comment.Set("issue", g.issue.Id)
	comment.Set("project", g.project.Id)
	comment.Set("workspace", g.ws.Id)
	comment.Set("actor", g.user.Id)
	comment.Set("created_by", g.user.Id)
	comment.Set("comment_html", "<p>hello</p>")
	comment.Set("comment_stripped", "hello")
	if err := g.app.Save(comment); err != nil {
		t.Fatalf("save: %v", err)
	}

	rows := activitiesFor(t, g.app, g.issue.Id)
	if len(rows) != 1 {
		t.Fatalf("got %d activities, want 1", len(rows))
	}
	got := rows[0]
	if got.GetString("verb") != "comment" {
		t.Errorf("verb = %q, want comment", got.GetString("verb"))
	}
	if got.GetString("field") != "comment" {
		t.Errorf("field = %q, want comment", got.GetString("field"))
	}
	if got.GetString("new_value") != "<p>hello</p>" {
		t.Errorf("new_value = %q, want the comment html", got.GetString("new_value"))
	}
	if got.GetString("new_identifier") != comment.GetString("uuid") {
		t.Errorf("new_identifier = %q, want %q", got.GetString("new_identifier"), comment.GetString("uuid"))
	}
	if got.GetString("issue_comment") != comment.Id {
		t.Errorf("issue_comment = %q, want %q", got.GetString("issue_comment"), comment.Id)
	}
	if got.GetString("actor") != g.user.Id {
		t.Errorf("actor = %q, want %q", got.GetString("actor"), g.user.Id)
	}
}

// TestActivityRelationCreatedBothSides: creating an issue_relations row writes
// two activities — one on each side — mirroring upstream
// create_issue_relation_activity. The stored row keeps the normalised relation
// type with the sides in issue/related_issue, which reconstructs both upstream
// orientations deterministically.
func TestActivityRelationCreatedBothSides(t *testing.T) {
	g := newProbeGraph(t)
	Register(g.app)

	other := seedIssue(t, g, "related", 2)

	c, _ := g.app.FindCollectionByNameOrId("issue_relations")
	row := core.NewRecord(c)
	row.Set("uuid", serializers.NewUUID())
	row.Set("issue", g.issue.Id)
	row.Set("related_issue", other.Id)
	row.Set("relation_type", "blocked_by")
	row.Set("project", g.project.Id)
	row.Set("workspace", g.ws.Id)
	row.Set("created_by", g.user.Id)
	if err := g.app.Save(row); err != nil {
		t.Fatalf("save: %v", err)
	}

	onIssue := activitiesFor(t, g.app, g.issue.Id)
	if len(onIssue) != 1 {
		t.Fatalf("got %d activities on the issue side, want 1", len(onIssue))
	}
	got := onIssue[0]
	if got.GetString("field") != "blocked_by" {
		t.Errorf("field = %q, want blocked_by (stored type)", got.GetString("field"))
	}
	if got.GetString("new_value") != "P-2" {
		t.Errorf("new_value = %q, want P-2 (related issue ref)", got.GetString("new_value"))
	}
	if got.GetString("old_identifier") != other.GetString("uuid") {
		t.Errorf("old_identifier = %q, want %q", got.GetString("old_identifier"), other.GetString("uuid"))
	}
	if got.GetString("comment") != "added blocked_by relation" {
		t.Errorf("comment = %q, want %q", got.GetString("comment"), "added blocked_by relation")
	}

	// The related side also carries its own "created" row (it was seeded after
	// Register), so assert on the relation row specifically.
	onOther := activitiesFor(t, g.app, other.Id)
	inverse := findActivity(onOther, "blocking")
	if inverse == nil {
		t.Fatalf("no blocking activity on the related side among %d rows", len(onOther))
	}
	if inverse.GetString("field") != "blocking" {
		t.Errorf("inverse field = %q, want blocking", inverse.GetString("field"))
	}
	if inverse.GetString("new_value") != "P-1" {
		t.Errorf("inverse new_value = %q, want P-1", inverse.GetString("new_value"))
	}
	if inverse.GetString("old_identifier") != g.issue.GetString("uuid") {
		t.Errorf("inverse old_identifier = %q, want %q", inverse.GetString("old_identifier"), g.issue.GetString("uuid"))
	}
}

// seedIssue creates an additional issue in the probe project with a sequence id.
func seedIssue(t *testing.T, g *probeGraph, name string, seq int) *core.Record {
	t.Helper()
	c, err := g.app.FindCollectionByNameOrId("issues")
	if err != nil {
		t.Fatalf("collection issues: %v", err)
	}
	r := core.NewRecord(c)
	r.Set("uuid", serializers.NewUUID())
	r.Set("name", name)
	r.Set("project", g.project.Id)
	r.Set("workspace", g.ws.Id)
	r.Set("state", g.state.Id)
	r.Set("created_by", g.user.Id)
	r.Set("sequence_id", seq)
	if err := g.app.Save(r); err != nil {
		t.Fatalf("save issue: %v", err)
	}
	return r
}
