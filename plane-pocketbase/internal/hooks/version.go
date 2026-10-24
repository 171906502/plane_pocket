package hooks

import (
	"io"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Version history for pages and issues (11-TASKLIST L-3).
//
// Upstream authority: apps/api/plane/bgtasks/page_version_task.py
//
//	PAGE_VERSION_TASK_TIMEOUT = 600 seconds
//	description_html changed  -> record a version
//	newest version is the same user AND <= 600s old -> update it in place
//	otherwise                                        -> insert a new one
//	keep at most 20; evict the oldest last_saved_at
//
// The merge rule is the point: the collaborative editor PATCHes
// description_html roughly every 1.5s, so without it a page exhausts its 20
// versions in half a minute and the "Version history" UI becomes noise.

const (
	// versionMergeWindow is upstream PAGE_VERSION_TASK_TIMEOUT.
	versionMergeWindow = 600 * time.Second
	// versionKeep is the per-page/issue version cap (upstream deletes the
	// oldest once the count passes 20).
	versionKeep = 20
	// versionBinaryName is the stored filename of a version's
	// description_binary snapshot. A constant name means a refreshed merge
	// replaces the previous snapshot instead of leaving a file per save.
	versionBinaryName = "description.bin"
)

// versionNow is the clock the merge window is measured against. It is a
// variable so tests can walk past the 600s window without sleeping.
var versionNow = time.Now

// versionPageChanged records a page version when the page's description_html
// changed. Bound for both create and update events (a page created with
// content gets its first version; a rename gets nothing).
func versionPageChanged(e *core.RecordEvent) {
	recordVersion(e, "pages", "page_versions", "page")
}

// versionIssueChanged is the issue twin, writing issue_description_versions.
func versionIssueChanged(e *core.RecordEvent) {
	recordVersion(e, "issues", "issue_description_versions", "issue")
}

// recordVersion is the shared pipeline for both version collections. It never
// returns an error: the caller wraps it in contain() (06-PITFALLS D-12
// premise 3 — a version-history failure must not fail the user's save).
func recordVersion(e *core.RecordEvent, sourceCollection, versionCollection, fk string) {
	src := e.Record
	if src.GetString("deleted_at") != "" {
		return // soft-deleted rows are not edited content
	}
	if src.GetString("description_html") == src.Original().GetString("description_html") {
		// Only description_html differences count: a page rename or an issue
		// property change must not create a version. On create the original is
		// blank (D-12 premise 2), so a record created with content still gets
		// its first version while an empty one gets none.
		return
	}

	app := e.App
	actor := src.GetString("updated_by")
	if actor == "" {
		actor = src.GetString("created_by")
	}
	if actor == "" {
		actor = src.GetString("owned_by") // pages carry an owner; issues do not
	}
	if actor == "" {
		return // owned_by is a required relation — nothing to attribute to
	}

	// Newest existing version for this page/issue. last_saved_at first,
	// created_at as the tiebreaker for saves inside the same millisecond.
	existing, err := app.FindRecordsByFilter(
		versionCollection,
		fk+" = {:entity} && deleted_at = ''",
		"-last_saved_at,-created_at", 1, 0,
		dbx.Params{"entity": src.Id},
	)
	if err != nil {
		app.Logger().Error(
			"hooks: version lookup failed (main save unaffected)",
			"collection", sourceCollection, "record", src.Id, "error", err,
		)
		return
	}

	now := versionNow()
	var target *core.Record
	if len(existing) == 1 && mergeable(existing[0], actor, now) {
		target = existing[0] // same user inside the window: refresh in place
	} else {
		collection, err := app.FindCollectionByNameOrId(versionCollection)
		if err != nil {
			app.Logger().Error(
				"hooks: version collection missing (main save unaffected)",
				"collection", versionCollection, "error", err,
			)
			return
		}
		target = core.NewRecord(collection)
		target.Set("uuid", serializers.NewUUID())
		target.Set(fk, src.Id)
		if sourceCollection == "issues" {
			// issue_description_versions carries the Plane denormalisation:
			// project + workspace resolved from the issue record.
			target.Set("project", src.GetString("project"))
		}
		target.Set("workspace", src.GetString("workspace"))
	}

	target.Set("owned_by", actor)
	target.Set("created_by", actor)
	target.Set("updated_by", actor)
	target.Set("description_html", src.GetString("description_html"))
	target.Set("description_json", src.Get("description_json"))
	target.Set("description_stripped", src.GetString("description_stripped"))
	setVersionBinary(app, target, src)
	target.Set("last_saved_at", now)

	inserted := target.Id == ""
	if err := app.Save(target); err != nil {
		app.Logger().Error(
			"hooks: version write failed (main save unaffected)",
			"collection", versionCollection, "record", src.Id, "error", err,
		)
		return
	}
	if inserted {
		capVersions(app, versionCollection, fk, src.Id)
	}
}

// mergeable reports whether the newest version row may be refreshed in place:
// upstream's rule is "same user and last saved within PAGE_VERSION_TASK_TIMEOUT".
func mergeable(candidate *core.Record, actor string, now time.Time) bool {
	if candidate.GetString("owned_by") != actor {
		return false
	}
	last := candidate.GetDateTime("last_saved_at")
	if last.IsZero() {
		return false
	}
	return now.Sub(last.Time()) <= versionMergeWindow
}

// setVersionBinary snapshots the source record's description_binary onto the
// version row. The column is a protected PocketBase file field, so the bytes
// must go in as a filesystem.File — assigning a filename from another record
// fails validation (06-PITFALLS, the description_binary bug). An empty source
// binary clears the snapshot so a merge does not keep stale bytes.
func setVersionBinary(app core.App, target, src *core.Record) {
	data := readRecordBinary(app, src)
	if len(data) == 0 {
		target.Set("description_binary", "")
		return
	}
	file, err := filesystem.NewFileFromBytes(data, versionBinaryName)
	if err != nil {
		app.Logger().Error(
			"hooks: version binary snapshot failed (main save unaffected)",
			"record", src.Id, "error", err,
		)
		return
	}
	target.Set("description_binary", file)
}

// readRecordBinary returns the stored description_binary file contents, or nil
// when none was saved. Same pattern as the page handler's readBinary.
func readRecordBinary(app core.App, r *core.Record) []byte {
	filename := r.GetString("description_binary")
	if filename == "" {
		return nil
	}
	fsys, err := app.NewFilesystem()
	if err != nil {
		return nil
	}
	defer fsys.Close()

	reader, err := fsys.GetReader(r.BaseFilesPath() + "/" + filename)
	if err != nil {
		return nil
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil
	}
	return data
}

// capVersions enforces the 20-version cap after an insert: the extras are
// hard-deleted oldest-last_saved_at first, which also drops each evicted
// version's stored file (PocketBase removes a deleted record's files prefix).
func capVersions(app core.App, versionCollection, fk, entityID string) {
	rows, err := app.FindRecordsByFilter(
		versionCollection,
		fk+" = {:entity} && deleted_at = ''",
		"last_saved_at,created_at", 0, 0,
		dbx.Params{"entity": entityID},
	)
	if err != nil {
		app.Logger().Error(
			"hooks: version cap lookup failed (main save unaffected)",
			"collection", versionCollection, "record", entityID, "error", err,
		)
		return
	}
	if len(rows) <= versionKeep {
		return
	}
	for _, row := range rows[:len(rows)-versionKeep] {
		if err := app.Delete(row); err != nil {
			app.Logger().Error(
				"hooks: version eviction failed (main save unaffected)",
				"collection", versionCollection, "record", row.Id, "error", err,
			)
		}
	}
}
