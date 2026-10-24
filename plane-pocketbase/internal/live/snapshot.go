package live

import (
	"io"
	"log"

	"github.com/pocketbase/pocketbase/core"
)

// seedFromStoredBinary rebuilds a room from the page's `description_binary`.
//
// The relay log is the only place page content lives between edits, and it is a
// plain file: a restored database, a wiped data directory or a deleted `.log`
// leaves the page empty in the editor. `description_binary` holds a complete yjs
// snapshot (`Y.encodeStateAsUpdate`) written by the editor, so it can stand in
// for the log until the user types again.
//
// Best-effort by design: no snapshot, no seeding, and the page simply opens
// empty as it did before.
func (h *Handler) seedFromStoredBinary(r *room, documentName string) {
	if r.hasContent() {
		return
	}
	page, err := findEntityByAPIID(h.app, "pages", documentName)
	if err != nil {
		return
	}
	snapshot := readDescriptionBinary(h.app, page)
	if len(snapshot) == 0 {
		return
	}
	r.seed(snapshot)
}

// readDescriptionBinary returns the raw bytes of the page's
// `description_binary` file field, or nil when the page never saved one.
//
// The column holds a *filename* inside the record's protected storage (that is
// how a PocketBase file field works), so the bytes have to be fetched through
// the app's filesystem rather than read from the column.
func readDescriptionBinary(app core.App, page *core.Record) []byte {
	filename := page.GetString("description_binary")
	if filename == "" {
		return nil
	}
	fsys, err := app.NewFilesystem()
	if err != nil {
		log.Printf("[live] filesystem unavailable: %v", err)
		return nil
	}
	defer fsys.Close()

	reader, err := fsys.GetReader(page.BaseFilesPath() + "/" + filename)
	if err != nil {
		log.Printf("[live] %s: description_binary %q is not in storage: %v",
			page.GetString("uuid"), filename, err)
		return nil
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		log.Printf("[live] %s: reading description_binary failed: %v", page.GetString("uuid"), err)
		return nil
	}
	return data
}
