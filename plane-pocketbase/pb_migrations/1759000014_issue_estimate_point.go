// L-2: give work items their estimate point — `issues.estimate_point`.
//
// WHY THIS FILE EXISTS
// -------------------
// The vertical slice deferred this relation on purpose
// (pb_migrations/1759000003_init_issue_core.go:28, "estimate_point ->
// estimate_points is outside this vertical slice"), and the deferral leaked into
// the API surface as silent data loss:
//
//   - internal/handlers/issues/wave1.go:405 documented `estimate_point` as
//     "ignored" in bulk updates, and neither the single-issue PATCH
//     (internal/handlers/issues/issues.go:313) nor the create path accepted it —
//     the frontend picks an estimate point (plane-preview/apps/web/components/
//     issues/issue-detail/sidebar.tsx:183-185) and the backend dropped it.
//   - internal/handlers/cycle/cycle.go:604-609 hardcoded the six
//     `*_estimate_points` aggregates to 0, so cycle/module cards always showed
//     "0" and `analytics/?type=points` degraded to issue counts.
//
// `draft_issues.estimate_point` has pointed at the same collection since wave 1
// (pb_migrations/1759000009_init_issue_subresources.go:471) with exactly the
// shape below; this file gives the real `issues` table the same column.
//
// CONTRACT (upstream authority)
// -----------------------------
// Django Issue.estimate_point is a nullable FK to EstimatePoint
// (apps/api/plane/db/models/issue.py:129, `estimate_point = models.ForeignKey(...)`);
// the column was added by migration 0067_issue_estimate.py. Both serializers
// validate it against the request's project on save
// (apps/api/plane/app/serializers/issue.py:188-195 and
// apps/api/plane/api/serializers/issue.py:153-161: "Estimate point is not valid
// please pass a valid estimate_point_id") — that project-scoping check lives in
// the handler layer here, not in the schema.
//
// IDEMPOTENCY
// -----------
// PocketBase records applied migrations in `_migrations`, so a normal `migrate
// up` runs this once. The field-existence guard below additionally makes a
// re-run on an already-migrated database (e.g. a restored `.fe-run` whose
// migration history was reset) a no-op instead of a duplicate-column error.
// The column is nullable with no default, so adding it to a populated `issues`
// table leaves every existing row's estimate unset — the correct backfill for
// data that never had the field.
package pb_migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		estimatePointsID, err := collectionID(app, "estimate_points")
		if err != nil {
			return err
		}
		return patchCollection(app, "issues", func(c *core.Collection) {
			if c.Fields.GetByName("estimate_point") != nil {
				return // already migrated
			}
			c.Fields.Add(relation("estimate_point", estimatePointsID, false, false)) // SET_NULL
		})
	}, func(app core.App) error {
		return patchCollection(app, "issues", func(c *core.Collection) {
			c.Fields.RemoveByName("estimate_point")
		})
	}, "1759000014_issue_estimate_point.go")
}
