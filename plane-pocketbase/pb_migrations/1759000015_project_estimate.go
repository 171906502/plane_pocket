// L-11: let a project carry an estimate system — `projects.estimate`.
//
// WHY THIS FILE EXISTS
// -------------------
// The vertical slice deferred this relation on purpose
// (pb_migrations/1759000001_init_identity_workspace_project.go:275, "estimate
// deferred — `estimates` is outside this slice"), and the deferral leaked into
// the API surface as an invisible feature:
//
//   - internal/serializers/projects.go never projected `estimate`, so
//     GET …/projects/{id}/ answered without the field.
//   - internal/handlers/projects/projects.go's Update whitelist never accepted
//     it, so PATCH {"estimate":"<uuid>"} returned 200 and silently dropped the
//     value.
//
// With the column missing, the frontend's gates stay closed:
// plane-preview/apps/web/store/estimates/project-estimate.store.ts
// (`areEstimateEnabledByProjectId`) reads `projectDetails.estimate` — falsy
// means the issue sidebar's Estimate row never renders
// (components/issues/issue-detail/sidebar.tsx:180) and
// store/cycle.store.ts (`getEstimateTypeByCycleId`) hard-returns "issues", so
// the cycle detail's estimate-type dropdown cannot switch to Estimates even
// though L-2 made the backend aggregation real.
//
// CONTRACT (upstream authority)
// -----------------------------
// Django Project.estimate is a nullable FK to Estimate
// (apps/api/plane/db/models/project.py:109, `estimate = models.ForeignKey(
// "db.Estimate", on_delete=models.SET_NULL, related_name="projects",
// null=True)`). ProjectSerializer uses `fields = "__all__"`
// (apps/api/plane/app/serializers/project.py:36), so the wire shape is the
// flat FK UUID — the same decision L-2 took for `issues.estimate_point`
// (internal/serializers/issues.go). Upstream applies no project-scoping
// validation on this FK (DRF only checks existence); the handler layer here
// additionally requires the estimate to belong to the same project, mirroring
// the rule L-2's resolveEstimatePoint enforces for estimate points.
//
// IDEMPOTENCY
// -----------
// PocketBase records applied migrations in `_migrations`, so a normal
// `migrate up` runs this once. The field-existence guard below additionally
// makes a re-run on an already-migrated database (e.g. a restored `.fe-run`
// whose migration history was reset) a no-op instead of a duplicate-column
// error. The column is nullable with no default, so adding it to a populated
// `projects` table leaves every existing row without an estimate — the correct
// backfill for data that never had the field.
package pb_migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		estimatesID, err := collectionID(app, "estimates")
		if err != nil {
			return err
		}
		return patchCollection(app, "projects", func(c *core.Collection) {
			if c.Fields.GetByName("estimate") != nil {
				return // already migrated
			}
			c.Fields.Add(relation("estimate", estimatesID, false, false)) // SET_NULL
		})
	}, func(app core.App) error {
		return patchCollection(app, "projects", func(c *core.Collection) {
			c.Fields.RemoveByName("estimate")
		})
	}, "1759000015_project_estimate.go")
}
