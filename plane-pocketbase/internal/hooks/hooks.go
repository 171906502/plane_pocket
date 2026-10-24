// Package hooks is Plane's missing "side-effect layer".
//
// Every cross-table write in this codebase used to live inside an HTTP handler
// (create issue -> also write assignees, create comment -> also write
// reactions...), so any derived data nobody remembered to hand-write simply
// never existed: the issue activity feed, the notification inbox, the version
// history. PocketBase record hooks close that gap — one place where
// "a record changed" becomes "the derived rows exist", for every write path
// (single PATCH, bulk operation, intake, cycle/module bridges) at once.
//
// This package currently owns three pipelines: the issue activity feed (L-1),
// the notification production path (L-4) and the page/issue version history
// (L-3), all driven from the same two record hooks below.
//
// Three premises verified by probe before this code was written (see
// docs/06-PITFALLS.md D-12 and premises_test.go):
//
//  1. Internal app.Save() calls DO fire these hooks (they are model-level, not
//     request-level), so a hook must never Save back into a watched collection
//     — that would recurse. Writing an unlistened collection (issue_activities)
//     fires the hook exactly once more, for the new row, and stops.
//  2. record.Original() carries the pre-save DB values ONLY when the record was
//     loaded from the database (all update handlers do find -> set -> save).
//     Records built with core.NewRecord have a blank original.
//  3. An error returned from an after-success hook propagates out of app.Save()
//     — AFTER the row was already written. Derived-data failures must therefore
//     be swallowed (logged only); returning them would fail a save that
//     actually succeeded.
package hooks

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

// Register binds the record hooks. Call it once at startup, before the app
// serves: the hooks are app-level (they fire for internal saves too), so they
// must not be bound per request.
func Register(app core.App) {
	app.OnRecordAfterCreateSuccess().BindFunc(func(e *core.RecordEvent) error {
		return contain(e, func(e *core.RecordEvent) {
			switch e.Record.Collection().Name {
			case "issues":
				activityIssueCreated(e)
				notifyIssueMentions(e)
				versionIssueChanged(e)
			case "issue_comments":
				activityCommentCreated(e)
				notifyCommentCreated(e)
			case "issue_relations":
				activityRelationCreated(e)
			case "issue_assignees":
				activityAssigneeAdded(e)
				notifyAssigneeCreated(e)
			case "issue_labels":
				activityLabelAdded(e)
			case "cycle_issues":
				activityCycleLinked(e)
			case "module_issues":
				activityModuleLinked(e)
			case "pages":
				// L-3: a page created with content gets its first version.
				versionPageChanged(e)
			}
		})
	})

	app.OnRecordAfterUpdateSuccess().BindFunc(func(e *core.RecordEvent) error {
		return contain(e, func(e *core.RecordEvent) {
			switch e.Record.Collection().Name {
			case "issues":
				activityIssueUpdated(e)
				notifyIssueMentions(e)
				versionIssueChanged(e)
			case "issue_comments":
				notifyCommentUpdated(e)
			case "cycle_issues":
				activityCycleLinkChanged(e)
			case "module_issues":
				activityModuleLinkChanged(e)
			case "pages":
				// L-3: version history (600s merge window + 20-version cap).
				versionPageChanged(e)
			}
		})
	})
}

// contain runs one hook body and guarantees the main save is never affected by
// it: the body always returns nil (premise 3) and a panic inside derived-data
// writing is recovered and logged instead of unwinding into PocketBase's save
// path, where it would surface as a 500 on an already-committed write.
func contain(e *core.RecordEvent, fn func(*core.RecordEvent)) (err error) {
	defer func() {
		if r := recover(); r != nil {
			e.App.Logger().Error(
				"hooks: panic while deriving side effects (main save unaffected)",
				"collection", e.Record.Collection().Name,
				"record", e.Record.Id,
				"panic", fmt.Sprintf("%v", r),
			)
		}
	}()
	fn(e)
	return nil
}
