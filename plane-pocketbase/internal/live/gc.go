package live

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/pocketbase/dbx"
)

// CollectOrphans deletes collaboration logs whose page no longer exists.
//
// Logs are written per page and are never touched by the REST layer, so deleting
// a page used to leak its log forever. Pages are soft-deleted (`deleted_at`), and
// a restore must bring the content back, so a row — however deleted — keeps its
// log; only a page that is gone from the database altogether is collected.
//
// Runs once at startup: the scan is a directory listing plus one indexed lookup
// per log, and pages do not disappear often enough to need a timer.
func (h *Hub) CollectOrphans() (removed int) {
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[live] orphan scan skipped: %v", err)
		}
		return 0
	}
	open := h.openLogPaths()

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		path := filepath.Join(h.dir, entry.Name())
		if _, busy := open[path]; busy {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		documentName := readNameSidecar(path)
		if documentName == "" {
			// Written before sidecars existed. Without a name the log cannot be
			// attributed, so only an empty one is safe to drop.
			if info.Size() == 0 {
				removeLogFiles(path)
				removed++
			}
			continue
		}
		if h.pageExists(documentName) {
			continue
		}
		log.Printf("[live] collecting %s: page %s no longer exists", entry.Name(), documentName)
		removeLogFiles(path)
		removed++
	}
	if removed > 0 {
		log.Printf("[live] orphan scan removed %d log(s)", removed)
	}
	return removed
}

// openLogPaths lists the logs currently backed by a room, so the scan cannot
// delete a file out from under a live connection.
func (h *Hub) openLogPaths() map[string]struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]struct{}, len(h.rooms))
	for _, r := range h.rooms {
		if r.log != nil {
			out[r.log.path] = struct{}{}
		}
	}
	return out
}

func (h *Hub) pageExists(documentName string) bool {
	_, err := h.app.FindFirstRecordByFilter("pages", "uuid = {:id}", dbx.Params{"id": documentName})
	return err == nil
}

func readNameSidecar(logPath string) string {
	data, err := os.ReadFile(nameSidecarPath(logPath))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func removeLogFiles(logPath string) {
	for _, suffix := range []string{"", ".name", ".bak", ".tmp"} {
		if err := os.Remove(logPath + suffix); err != nil && !os.IsNotExist(err) {
			log.Printf("[live] could not remove %s: %v", logPath+suffix, err)
		}
	}
}
