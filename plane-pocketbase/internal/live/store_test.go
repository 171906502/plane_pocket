package live

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

func mustLog(t *testing.T, dir, documentName string) (*docLog, [][]byte) {
	t.Helper()
	l, records, err := openDocLog(dir, documentName)
	if err != nil {
		t.Fatalf("open doc log: %v", err)
	}
	t.Cleanup(func() { _ = l.close() })
	return l, records
}

func TestDocLogReplaceSwapsContent(t *testing.T) {
	dir := t.TempDir()
	l, records := mustLog(t, dir, "doc-1")
	if len(records) != 0 {
		t.Fatalf("new log has %d records, want 0", len(records))
	}
	for _, u := range [][]byte{{1, 1, 1}, {2, 2}, {3}} {
		if err := l.append(u); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	size, err := l.replace([][]byte{{9, 9, 9, 9}})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if size != int64(len(appendVarBytes(nil, []byte{9, 9, 9, 9}))) {
		t.Fatalf("replace reported %d bytes", size)
	}
	if got := l.currentSize(); got != size {
		t.Fatalf("currentSize=%d, want %d", got, size)
	}

	// Appends after a replace must land at the end of the new file.
	if err := l.append([]byte{7}); err != nil {
		t.Fatalf("append after replace: %v", err)
	}

	reopened, reread, err := openDocLog(dir, "doc-1")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.close()
	if len(reread) != 2 || reread[0][0] != 9 || reread[1][0] != 7 {
		t.Fatalf("replayed %v, want [[9 9 9 9] [7]]", reread)
	}
}

func TestDocLogReplaceKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	l, _ := mustLog(t, dir, "doc-2")
	if err := l.append([]byte{5, 5}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := l.replace([][]byte{{6}}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if _, err := os.Stat(l.path + ".bak"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
}

func TestOpenDocLogWritesNameSidecar(t *testing.T) {
	dir := t.TempDir()
	l, _ := mustLog(t, dir, "page-uuid-1")
	got := readNameSidecar(l.path)
	if got != "page-uuid-1" {
		t.Fatalf("sidecar=%q, want page-uuid-1", got)
	}
	// A different document must not reuse the file.
	other, _ := mustLog(t, dir, "page-uuid-2")
	if other.path == l.path {
		t.Fatal("distinct documents share a log file")
	}
}

// liveFixture is the minimum a page needs: a workspace it belongs to.
func liveFixture(t *testing.T) (*tests.TestApp, *core.Record) {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	save := func(collection string, set func(*core.Record)) *core.Record {
		c, err := app.FindCollectionByNameOrId(collection)
		if err != nil {
			t.Fatalf("collection %s: %v", collection, err)
		}
		r := core.NewRecord(c)
		set(r)
		if err := app.Save(r); err != nil {
			t.Fatalf("save %s: %v", collection, err)
		}
		return r
	}
	user := save("users", func(r *core.Record) {
		r.Set("uuid", "11111111-1111-1111-1111-111111111111")
		r.Set("username", "owner")
		r.Set("email", "owner@example.com")
		r.Set("password", "1234567890")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	ws := save("workspaces", func(r *core.Record) {
		r.Set("uuid", "33333333-3333-3333-3333-333333333333")
		r.Set("name", "WS")
		r.Set("slug", "ws")
		r.Set("owner", user.Id)
	})
	page := save("pages", func(r *core.Record) {
		r.Set("uuid", "22222222-2222-2222-2222-222222222222")
		r.Set("name", "doc")
		r.Set("workspace", ws.Id)
		r.Set("owned_by", user.Id)
	})
	return app, page
}

func TestCollectOrphansRemovesLogsOfDeletedPages(t *testing.T) {
	app, page := liveFixture(t)
	hub := NewHub(app, filepath.Join(app.DataDir(), "live"))
	documentName := page.GetString("uuid")

	r, err := hub.room(documentName)
	if err != nil {
		t.Fatalf("room: %v", err)
	}
	if err := r.log.append([]byte{1, 2, 3}); err != nil {
		t.Fatalf("append: %v", err)
	}
	logPath := r.log.path
	_ = r.log.close()
	hub.rooms = map[string]*room{}

	if err := app.Delete(page); err != nil {
		t.Fatalf("delete page: %v", err)
	}
	if removed := hub.CollectOrphans(); removed != 1 {
		t.Fatalf("removed=%d, want 1", removed)
	}
	for _, suffix := range []string{"", ".name"} {
		if _, err := os.Stat(logPath + suffix); !os.IsNotExist(err) {
			t.Fatalf("%s still exists (err=%v)", logPath+suffix, err)
		}
	}
}

func TestCollectOrphansKeepsLogsOfExistingPages(t *testing.T) {
	app, page := liveFixture(t)
	hub := NewHub(app, filepath.Join(app.DataDir(), "live"))

	r, err := hub.room(page.GetString("uuid"))
	if err != nil {
		t.Fatalf("room: %v", err)
	}
	if err := r.log.append([]byte{1, 2, 3}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if removed := hub.CollectOrphans(); removed != 0 {
		t.Fatalf("removed=%d, want 0 (page exists and the room is open)", removed)
	}
	_ = r.log.close()
	hub.rooms = map[string]*room{}
	if removed := hub.CollectOrphans(); removed != 0 {
		t.Fatalf("removed=%d, want 0 (page still exists)", removed)
	}
	if _, err := os.Stat(r.log.path); err != nil {
		t.Fatalf("log was removed: %v", err)
	}
}

func TestSeedInstallsSnapshotOnce(t *testing.T) {
	app, page := liveFixture(t)
	hub := NewHub(app, filepath.Join(app.DataDir(), "live"))
	r, err := hub.room(page.GetString("uuid"))
	if err != nil {
		t.Fatalf("room: %v", err)
	}
	defer r.log.close()

	r.seed([]byte{4, 5, 6})
	r.seed([]byte{7, 7, 7}) // second call must be ignored
	if len(r.updates) != 1 {
		t.Fatalf("updates=%d, want 1", len(r.updates))
	}
	r.seed(emptyUpdate) // and a no-op must not count either
	if len(r.updates) != 1 {
		t.Fatalf("updates=%d after noop seed, want 1", len(r.updates))
	}
}
