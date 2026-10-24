package hooks

import (
	"io"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// versionGraph is the minimal Plane graph the version-history tests edit:
// workspace -> project -> page/issue, plus a second user so the merge rule's
// "same user" half can be exercised. It builds on the L-1 probe graph (same
// seeding style: plain app.Save calls, no HTTP) and adds a page. The page and
// issue are created WITHOUT description_html, so seeding produces no versions
// even once the hooks are registered.
type versionGraph struct {
	app     core.App
	ws      *core.Record
	project *core.Record
	user    *core.Record
	other   *core.Record
	page    *core.Record
	issue   *core.Record
}

func newVersionGraph(t *testing.T) *versionGraph {
	t.Helper()
	probe := newProbeGraph(t)

	other := saveVersionRecord(t, probe.app, "users", func(r *core.Record) {
		r.Set("username", "other")
		r.Set("email", "other@example.com")
		r.Set("password", "1234567890")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	page := saveVersionRecord(t, probe.app, "pages", func(r *core.Record) {
		r.Set("workspace", probe.ws.Id)
		r.Set("name", "page")
		r.Set("owned_by", probe.user.Id)
		r.Set("created_by", probe.user.Id)
		r.Set("access", 0)
		r.Set("description_json", map[string]any{})
	})

	return &versionGraph{
		app:     probe.app,
		ws:      probe.ws,
		project: probe.project,
		user:    probe.user,
		other:   other,
		page:    page,
		issue:   probe.issue,
	}
}

// saveVersionRecord seeds one row. Callers must Register the hooks BEFORE any
// save whose side effects are under test.
func saveVersionRecord(t *testing.T, app core.App, collection string, set func(*core.Record)) *core.Record {
	t.Helper()
	c, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		t.Fatalf("collection %s: %v", collection, err)
	}
	r := core.NewRecord(c)
	r.Set("uuid", serializers.NewUUID())
	set(r)
	if err := app.Save(r); err != nil {
		t.Fatalf("save %s: %v", collection, err)
	}
	return r
}

// editPage reloads the page from the database, applies the mutation as `actor`
// and saves — the find -> set -> save shape every handler uses, which is also
// what makes record.Original() readable inside the hook (06-PITFALLS D-12
// premise 2). A nil actor leaves updated_by untouched.
func (g *versionGraph) editPage(t *testing.T, actor *core.Record, set func(*core.Record)) {
	t.Helper()
	page, err := g.app.FindRecordById("pages", g.page.Id)
	if err != nil {
		t.Fatalf("reload page: %v", err)
	}
	if actor != nil {
		page.Set("updated_by", actor.Id)
	}
	set(page)
	if err := g.app.Save(page); err != nil {
		t.Fatalf("save page: %v", err)
	}
}

// editIssue is the issue twin of editPage.
func (g *versionGraph) editIssue(t *testing.T, actor *core.Record, set func(*core.Record)) {
	t.Helper()
	issue, err := g.app.FindRecordById("issues", g.issue.Id)
	if err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	if actor != nil {
		issue.Set("updated_by", actor.Id)
	}
	set(issue)
	if err := g.app.Save(issue); err != nil {
		t.Fatalf("save issue: %v", err)
	}
}

// pageVersions lists the page's live version rows, newest first.
func (g *versionGraph) pageVersions(t *testing.T) []*core.Record {
	t.Helper()
	return g.versions(t, "page_versions", "page", g.page.Id)
}

// issueVersions lists the issue's live description-version rows, newest first.
func (g *versionGraph) issueVersions(t *testing.T) []*core.Record {
	t.Helper()
	return g.versions(t, "issue_description_versions", "issue", g.issue.Id)
}

func (g *versionGraph) versions(t *testing.T, collection, fk, entityID string) []*core.Record {
	t.Helper()
	rows, err := g.app.FindRecordsByFilter(
		collection,
		fk+" = {:entity} && deleted_at = ''",
		"-last_saved_at,-created_at", 0, 0,
		dbx.Params{"entity": entityID},
	)
	if err != nil {
		t.Fatalf("find %s: %v", collection, err)
	}
	return rows
}

// useClock replaces the hook's clock with one the test advances by hand, and
// returns the advance function. Restores the real clock on cleanup.
func useClock(t *testing.T, start time.Time) func(time.Duration) {
	t.Helper()
	clock := start
	versionNow = func() time.Time { return clock }
	t.Cleanup(func() { versionNow = time.Now })
	return func(d time.Duration) { clock = clock.Add(d) }
}

// newVersionFile wraps bytes the way the page handler does for a protected
// file field: assigning raw bytes or a borrowed filename fails validation.
func newVersionFile(t *testing.T, data []byte) *filesystem.File {
	t.Helper()
	file, err := filesystem.NewFileFromBytes(data, "description.bin")
	if err != nil {
		t.Fatalf("new file: %v", err)
	}
	return file
}

// readRecordFile returns the bytes stored behind a record's
// description_binary column (protected storage, per-record path).
func readRecordFile(t *testing.T, app core.App, r *core.Record) []byte {
	t.Helper()
	name := r.GetString("description_binary")
	if name == "" {
		t.Fatalf("record %s has no description_binary", r.Id)
	}
	fsys, err := app.NewFilesystem()
	if err != nil {
		t.Fatalf("new filesystem: %v", err)
	}
	defer fsys.Close()
	reader, err := fsys.GetReader(r.BaseFilesPath() + "/" + name)
	if err != nil {
		t.Fatalf("read %s/%s: %v", r.BaseFilesPath(), name, err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	return data
}
