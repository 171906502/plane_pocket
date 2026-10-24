package hooks

import (
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/page"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

// restoreFixture seeds a page the way the page handlers expect to find it:
// workspace + project + both membership rows + the ProjectPage link. The page
// is public (access=0) so the restore guard's owner shortcut is not needed.
type restoreFixture struct {
	app     core.App
	ws      *core.Record
	project *core.Record
	user    *core.Record
	page    *core.Record
}

func newRestoreFixture(t *testing.T) *restoreFixture {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	f := &restoreFixture{app: app}
	f.user = saveVersionRecord(t, app, "users", func(r *core.Record) {
		r.Set("username", "restorer")
		r.Set("email", "restorer@example.com")
		r.Set("password", "1234567890")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	f.ws = saveVersionRecord(t, app, "workspaces", func(r *core.Record) {
		r.Set("name", "WS")
		r.Set("slug", "ws")
		r.Set("owner", f.user.Id)
	})
	f.project = saveVersionRecord(t, app, "projects", func(r *core.Record) {
		r.Set("name", "P")
		r.Set("identifier", "P")
		r.Set("workspace", f.ws.Id)
	})
	saveVersionRecord(t, app, "workspace_members", func(r *core.Record) {
		r.Set("workspace", f.ws.Id)
		r.Set("member", f.user.Id)
		r.Set("is_active", true)
		r.Set("role", middleware.RoleMember)
	})
	saveVersionRecord(t, app, "project_members", func(r *core.Record) {
		r.Set("project", f.project.Id)
		r.Set("member", f.user.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("is_active", true)
		r.Set("role", middleware.RoleMember)
	})
	f.page = saveVersionRecord(t, app, "pages", func(r *core.Record) {
		r.Set("workspace", f.ws.Id)
		r.Set("name", "restore-me")
		r.Set("owned_by", f.user.Id)
		r.Set("created_by", f.user.Id)
		r.Set("access", 0)
		r.Set("description_json", map[string]any{})
	})
	saveVersionRecord(t, app, "project_pages", func(r *core.Record) {
		r.Set("workspace", f.ws.Id)
		r.Set("project", f.project.Id)
		r.Set("page", f.page.Id)
		r.Set("created_by", f.user.Id)
	})
	return f
}

// scope mirrors what ResolveWorkspace + ResolveProject produce for the member.
func (f *restoreFixture) scope() *middleware.Scope {
	wsMember, _ := f.app.FindFirstRecordByFilter(
		"workspace_members",
		"workspace = {:w} && member = {:m} && is_active = true",
		map[string]any{"w": f.ws.Id, "m": f.user.Id},
	)
	pjMember, _ := f.app.FindFirstRecordByFilter(
		"project_members",
		"project = {:p} && member = {:m} && is_active = true",
		map[string]any{"p": f.project.Id, "m": f.user.Id},
	)
	return &middleware.Scope{
		Workspace:       f.ws,
		Project:         f.project,
		WorkspaceMember: wsMember,
		ProjectMember:   pjMember,
		WorkspaceRole:   middleware.RoleMember,
		ProjectRole:     middleware.RoleMember,
	}
}

// restoreEvent builds the RequestEvent RestoreVersion expects.
func (f *restoreFixture) restoreEvent(t *testing.T, pageUUID, versionUUID string) *core.RequestEvent {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/pages/"+pageUUID+"/versions/"+versionUUID+"/restore/", nil)
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: f.app}
	e.Request = req
	e.Response = rec
	e.Auth = f.user
	e.Set("plane.scope", f.scope())
	e.Request.SetPathValue("page_id", pageUUID)
	e.Request.SetPathValue("version_id", versionUUID)
	return e
}

// TestRestoreVersionWritesContentBack drives the real handler: a page whose
// current description is "current", a version holding "old" (html + json +
// stripped + a yjs-like binary), and a restore. Afterwards the page must carry
// the version's whole payload — the binary included, as file bytes on the page
// record, not a filename borrowed from the version row.
func TestRestoreVersionWritesContentBack(t *testing.T) {
	f := newRestoreFixture(t)
	Register(f.app)

	// Edit the page so the hook records the current state as a version.
	edit := func(html string) {
		p, err := f.app.FindRecordById("pages", f.page.Id)
		if err != nil {
			t.Fatalf("reload page: %v", err)
		}
		p.Set("updated_by", f.user.Id)
		p.Set("description_html", html)
		p.Set("description_stripped", strings.TrimPrefix(html, "<p>"))
		if err := f.app.Save(p); err != nil {
			t.Fatalf("save page: %v", err)
		}
	}
	edit("<p>current</p>")

	// The version to restore, seeded directly (the read side already exists;
	// this test is about the restore write path).
	binary := []byte{0x00, 0x0a, 0xfe, 0xff, 0x42}
	version := saveVersionRecord(t, f.app, "page_versions", func(r *core.Record) {
		r.Set("page", f.page.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("owned_by", f.user.Id)
		r.Set("last_saved_at", time.Now().Add(-time.Hour))
		r.Set("description_html", "<p>old</p>")
		r.Set("description_stripped", "old")
		r.Set("description_json", map[string]any{"type": "doc"})
		r.Set("description_binary", newVersionFile(t, binary))
	})

	h := page.New(f.app)
	e := f.restoreEvent(t, f.page.GetString("uuid"), version.GetString("uuid"))
	if err := h.RestoreVersion(e); err != nil {
		t.Fatalf("RestoreVersion: %v", err)
	}
	if e.Response.(*httptest.ResponseRecorder).Code != http.StatusNoContent {
		t.Fatalf("restore status = %d, want 204", e.Response.(*httptest.ResponseRecorder).Code)
	}

	reloaded, err := f.app.FindRecordById("pages", f.page.Id)
	if err != nil {
		t.Fatalf("reload page: %v", err)
	}
	if got := reloaded.GetString("description_html"); got != "<p>old</p>" {
		t.Fatalf("page html after restore = %q, want %q", got, "<p>old</p>")
	}
	if got := reloaded.GetString("description_stripped"); got != "old" {
		t.Fatalf("page stripped after restore = %q, want %q", got, "old")
	}
	if got := reloaded.GetString("description_json"); got != `{"type":"doc"}` {
		t.Fatalf("page json after restore = %q, want %q", got, `{"type":"doc"}`)
	}
	if got := readRecordFile(t, f.app, reloaded); string(got) != string(binary) {
		t.Fatalf("page binary after restore = %v, want the version's %v", got, binary)
	}

	// The restore save itself fires the pages update hook: the restored content
	// is recorded as the newest version (upstream behaves the same), and the
	// write lands in the unwatched page_versions collection so it stops there.
	rows, err := f.app.FindRecordsByFilter(
		"page_versions", "page = {:p} && deleted_at = ''", "-last_saved_at,-created_at", 0, 0,
		map[string]any{"p": f.page.Id},
	)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("restore left %d version rows, want 2 (current + restored)", len(rows))
	}
	if got := rows[0].GetString("description_html"); got != "<p>old</p>" {
		t.Fatalf("newest version after restore = %q, want %q", got, "<p>old</p>")
	}
}

// TestRestoreVersionClearsLiveLog proves the relay's stored updates are gone
// after a restore. The log is written the way the live package names it
// (internal/live/store.go safeFileName + openDocLog: <sanitised>-<hash>.log
// plus a .name sidecar, and compaction's .bak); after the restore none of
// them may survive, otherwise the relay would replay the pre-restore CRDT
// updates over the restored content.
func TestRestoreVersionClearsLiveLog(t *testing.T) {
	f := newRestoreFixture(t)
	Register(f.app)

	version := saveVersionRecord(t, f.app, "page_versions", func(r *core.Record) {
		r.Set("page", f.page.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("owned_by", f.user.Id)
		r.Set("last_saved_at", time.Now().Add(-time.Hour))
		r.Set("description_html", "<p>old</p>")
		r.Set("description_stripped", "old")
		r.Set("description_binary", newVersionFile(t, []byte("yjs-snapshot")))
	})

	// The relay's document name is the page's uuid (internal/live handler.go
	// authorize + store.go openDocLog). liveLogName mirrors store.go's
	// safeFileName — if that naming ever changes this setup fails loudly
	// instead of passing vacuously.
	pageUUID := f.page.GetString("uuid")
	dir := filepath.Join(f.app.DataDir(), "live")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir live dir: %v", err)
	}
	logPath := filepath.Join(dir, liveLogName(pageUUID)+".log")
	for _, path := range []string{logPath, logPath + ".name", logPath + ".bak"} {
		if err := os.WriteFile(path, []byte("pre-restore relay state"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	h := page.New(f.app)
	e := f.restoreEvent(t, pageUUID, version.GetString("uuid"))
	if err := h.RestoreVersion(e); err != nil {
		t.Fatalf("RestoreVersion: %v", err)
	}

	for _, suffix := range []string{"", ".name", ".bak"} {
		if _, err := os.Stat(logPath + suffix); !os.IsNotExist(err) {
			t.Fatalf("live log %s survived the restore (err=%v) — the relay would replay the old content", logPath+suffix, err)
		}
	}
}

// liveLogName mirrors internal/live/store.go's safeFileName: sanitise the
// document name, cap it at 64 runes, append a 4-byte sha1 prefix. Reproduced
// here because the helper is unexported and the test must write the log at the
// exact path PurgeDocument targets.
func liveLogName(documentName string) string {
	var b strings.Builder
	for _, r := range documentName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 64 {
			break
		}
	}
	sum := sha1.Sum([]byte(documentName))
	if b.Len() == 0 {
		b.WriteString("doc")
	}
	return b.String() + "-" + hex.EncodeToString(sum[:4])
}
