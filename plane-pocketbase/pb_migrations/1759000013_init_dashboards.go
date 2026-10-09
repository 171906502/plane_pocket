// Wave 2 support: the home-dashboard persistence tables.
//
// WHY THIS FILE EXISTS / WHY THE ORCHESTRATOR WROTE IT
// ---------------------------------------------------
// Wave 2 needs `dashboards` + `dashboard_widgets`, and neither exists in the
// schema (grep for `NewBaseCollection("dashboard` across pb_migrations/ returns
// nothing). Adding a migration is the single highest-risk shared resource in a
// parallel run — migration *ordering* and the fact that Go compiles the whole
// `pb_migrations` package as one unit mean two agents writing migrations can
// break each other's builds. So the orchestrator writes this one up front and
// the agents only ever consume the resulting tables. See
// docs/08-IMPLEMENTATION-PLAN.md §5 and §9 rule 1.
//
// CONTRACT PROVENANCE (there is no Django model to mirror — read carefully)
// ------------------------------------------------------------------------
// Plane's Django checkout has NO `Dashboard` model and NO URL for the paths the
// frontend actually calls:
//
//   apps/api/plane/db/models/            -> no dashboard.py
//   apps/api/plane/app/urls/user.py:80   -> only "users/me/workspaces/<slug>/dashboard/"
//   spec/api-spec.yaml:2450              -> only `dashboard.list` at that same Django path
//
// but the web app calls:
//
//   GET   /api/workspaces/{slug}/dashboard/                       ?dashboard_type=home
//   GET   /api/workspaces/{slug}/dashboard/{dashboard_id}/
//   GET   /api/dashboard/{dashboard_id}/
//   PATCH /api/dashboard/{dashboard_id}/widgets/{widget_id}/
//
// (apps/web/services/dashboard.service.ts). The row shape below is therefore
// derived from the frontend's own types, not from a serializer:
//
//   packages/types/src/dashboard.ts:184  THomeDashboardResponse = { dashboard, widgets }
//   packages/types/src/dashboard.ts:170  TDeprecatedDashboard   = { id, name,
//       description_html, identifier, is_default, owned_by, type, created_at,
//       updated_at, created_by, updated_by }
//   packages/types/src/dashboard.ts:66   TWidget = { id, is_visible, key,
//       widget_filters (read), filters (write) }
//
// `type` is a plain TextField on purpose: the frontend type is just `string`,
// so inventing a select-enum here would be fabricating a constraint that the
// contract does not state.
package pb_migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// ---------------------------------------------------------------------------
// dashboards — the container the home page resolves to (TDeprecatedDashboard).
// ---------------------------------------------------------------------------

func buildDashboards(usersID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("dashboards")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 255})
	c.Fields.Add(&core.EditorField{Name: "description_html", MaxSize: maxEditor})
	c.Fields.Add(&core.TextField{Name: "identifier", Max: 255})
	// Deliberately NOT a SelectField — see the header note.
	c.Fields.Add(&core.TextField{Name: "type", Max: 255})
	c.Fields.Add(&core.BoolField{Name: "is_default"})

	// `owned_by` is the dashboard's owner (distinct from created_by/updated_by,
	// which addAuditFields already added). Non-null in the frontend type.
	c.Fields.Add(relation("owned_by", usersID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	addIdentityIndexes(c, true)

	// The home dashboard is resolved per (workspace, owner, type). Not UNIQUE:
	// the handler does get-or-create, and a UNIQUE here would turn a benign
	// concurrent create into a 400 instead of converging.
	c.Indexes = append(c.Indexes,
		fmt.Sprintf("CREATE INDEX `idx_%s_lookup` ON `%s` (`workspace`, `owned_by`, `type`)", c.Name, c.Name),
	)

	return c
}

// ---------------------------------------------------------------------------
// dashboard_widgets — one row per widget (TWidget). The `id` the frontend
// receives is this row's Plane `uuid`.
// ---------------------------------------------------------------------------

func buildDashboardWidgets(usersID, workspacesID, dashboardsID string) *core.Collection {
	c := core.NewBaseCollection("dashboard_widgets")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("dashboard", dashboardsID, true, true)) // CASCADE
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// TWidgetKeys: assigned_issues | created_issues | issues_by_state_groups |
	// issues_by_priority | recent_activity | recent_projects |
	// recent_collaborators. Left as text so a frontend-side key addition does
	// not require a migration.
	c.Fields.Add(&core.TextField{Name: "key", Required: true, Max: 255})
	c.Fields.Add(&core.BoolField{Name: "is_visible"})
	// TWidget.filters — written by PATCH, echoed back as `widget_filters`.
	c.Fields.Add(&core.JSONField{Name: "filters"})
	c.Fields.Add(&core.NumberField{Name: "sort_order", OnlyInt: true})

	addIdentityIndexes(c, true)

	c.Indexes = append(c.Indexes,
		fmt.Sprintf("CREATE INDEX `idx_%s_dashboard` ON `%s` (`dashboard`, `sort_order`)", c.Name, c.Name),
	)

	return c
}

func init() {
	m.Register(func(app core.App) error {
		usersID, err := collectionID(app, "users")
		if err != nil {
			return err
		}
		workspacesID, err := collectionID(app, "workspaces")
		if err != nil {
			return err
		}

		if err := app.Save(buildDashboards(usersID, workspacesID)); err != nil {
			return fmt.Errorf("create dashboards: %w", err)
		}
		dashboardsID, err := collectionID(app, "dashboards")
		if err != nil {
			return err
		}

		if err := app.Save(buildDashboardWidgets(usersID, workspacesID, dashboardsID)); err != nil {
			return fmt.Errorf("create dashboard_widgets: %w", err)
		}

		return nil
	}, func(app core.App) error {
		for _, name := range []string{"dashboard_widgets", "dashboards"} {
			c, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				return fmt.Errorf("down: find %q: %w", name, err)
			}
			if err := app.Delete(c); err != nil {
				return fmt.Errorf("down: delete %q: %w", name, err)
			}
		}
		return nil
	}, "1759000013_init_dashboards.go")
}
