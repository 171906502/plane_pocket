package hooks

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// TestVersionIgnoredWhenDescriptionUnchanged pins the "only description_html
// counts" half of the contract: a page rename (or any other property change)
// must not create a version, and re-saving the same html must not either.
// Without the Original() diff, every collaborative PATCH would at best be a
// no-op write and at worst a fresh version row.
func TestVersionIgnoredWhenDescriptionUnchanged(t *testing.T) {
	g := newVersionGraph(t)
	Register(g.app)

	g.editPage(t, g.user, func(r *core.Record) { r.Set("name", "renamed") })
	if rows := g.pageVersions(t); len(rows) != 0 {
		t.Fatalf("rename produced %d version rows, want 0", len(rows))
	}

	g.editPage(t, g.user, func(r *core.Record) { r.Set("description_html", "<p>one</p>") })
	rows := g.pageVersions(t)
	if len(rows) != 1 {
		t.Fatalf("description edit produced %d version rows, want 1", len(rows))
	}

	// Same html again: the diff is empty, so the hook writes nothing at all.
	g.editPage(t, g.user, func(r *core.Record) { r.Set("description_html", "<p>one</p>") })
	if rows := g.pageVersions(t); len(rows) != 1 {
		t.Fatalf("unchanged description produced %d version rows, want 1", len(rows))
	}

	// A rename on top of the description still must not add a row.
	g.editPage(t, g.user, func(r *core.Record) { r.Set("name", "renamed again") })
	if rows := g.pageVersions(t); len(rows) != 1 {
		t.Fatalf("rename after description edit produced %d version rows, want 1", len(rows))
	}
	if got := rows[0].GetString("description_html"); got != "<p>one</p>" {
		t.Fatalf("version html = %q, want %q (no refresh on an unrelated save)", got, "<p>one</p>")
	}
}

// TestIssueDescriptionVersionResolvesRelations covers the issue half: property
// changes are ignored, a description change writes issue_description_versions
// with project + workspace resolved from the issue record (the collection
// requires both).
func TestIssueDescriptionVersionResolvesRelations(t *testing.T) {
	g := newVersionGraph(t)
	Register(g.app)

	g.editIssue(t, g.user, func(r *core.Record) { r.Set("name", "renamed") })
	if rows := g.issueVersions(t); len(rows) != 0 {
		t.Fatalf("issue rename produced %d version rows, want 0", len(rows))
	}

	g.editIssue(t, g.user, func(r *core.Record) { r.Set("description_html", "<p>issue body</p>") })
	rows := g.issueVersions(t)
	if len(rows) != 1 {
		t.Fatalf("issue description edit produced %d version rows, want 1", len(rows))
	}
	row := rows[0]
	if row.GetString("issue") != g.issue.Id {
		t.Errorf("version issue = %q, want %q", row.GetString("issue"), g.issue.Id)
	}
	if row.GetString("project") != g.project.Id {
		t.Errorf("version project = %q, want %q (resolved from the issue)", row.GetString("project"), g.project.Id)
	}
	if row.GetString("workspace") != g.ws.Id {
		t.Errorf("version workspace = %q, want %q", row.GetString("workspace"), g.ws.Id)
	}
	if row.GetString("owned_by") != g.user.Id {
		t.Errorf("version owned_by = %q, want %q (the actor)", row.GetString("owned_by"), g.user.Id)
	}
	if got := row.GetString("description_html"); got != "<p>issue body</p>" {
		t.Errorf("version description_html = %q", got)
	}
}

// TestNewRecordWithContentGetsFirstVersion pins the create half: a page/issue
// created WITH description_html gets its first version (its Original() is
// blank, D-12 premise 2), one created empty gets none.
func TestNewRecordWithContentGetsFirstVersion(t *testing.T) {
	g := newVersionGraph(t)
	Register(g.app)

	empty := saveVersionRecord(t, g.app, "pages", func(r *core.Record) {
		r.Set("workspace", g.ws.Id)
		r.Set("name", "empty")
		r.Set("owned_by", g.user.Id)
		r.Set("created_by", g.user.Id)
		r.Set("access", 0)
	})
	withContent := saveVersionRecord(t, g.app, "pages", func(r *core.Record) {
		r.Set("workspace", g.ws.Id)
		r.Set("name", "seeded")
		r.Set("owned_by", g.user.Id)
		r.Set("created_by", g.user.Id)
		r.Set("access", 0)
		r.Set("description_html", "<p>created with body</p>")
	})

	if rows := g.versions(t, "page_versions", "page", empty.Id); len(rows) != 0 {
		t.Fatalf("page created without description produced %d versions, want 0", len(rows))
	}
	rows := g.versions(t, "page_versions", "page", withContent.Id)
	if len(rows) != 1 {
		t.Fatalf("page created with description produced %d versions, want 1", len(rows))
	}
	if got := rows[0].GetString("description_html"); !strings.Contains(got, "created with body") {
		t.Fatalf("first version html = %q", got)
	}
}

// TestVersionBinarySnapshotCopied checks that a version row carries the
// source record's description_binary bytes, not a filename borrowed from
// another record (the column is a protected file field — 06-PITFALLS).
func TestVersionBinarySnapshotCopied(t *testing.T) {
	g := newVersionGraph(t)
	Register(g.app)

	// Put a yjs-like snapshot on the page through the real file-field path.
	binary := []byte{0x00, 0x01, 0xfe, 0xff, 0x10}
	g.editPage(t, g.user, func(r *core.Record) {
		r.Set("description_html", "<p>with binary</p>")
		r.Set("description_binary", newVersionFile(t, binary))
	})

	rows := g.pageVersions(t)
	if len(rows) != 1 {
		t.Fatalf("edit produced %d version rows, want 1", len(rows))
	}
	name := rows[0].GetString("description_binary")
	if name == "" {
		t.Fatal("version row has no description_binary snapshot")
	}
	if strings.Contains(name, "=") || name == string(binary) {
		t.Fatalf("version column holds the payload, not a filename: %q", name)
	}
	if got := readRecordFile(t, g.app, rows[0]); string(got) != string(binary) {
		t.Fatalf("version binary = %v, want %v", got, binary)
	}
}
