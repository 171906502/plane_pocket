// Wave 2: `project_members`, `states`, `labels` (spec §6.3 wave W7).
//
// All three depend on `projects` and `workspaces` from wave 1. `labels.parent`
// is a self-relation and is deferred to wave 4 (§6.4 D10).
package pb_migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// ---------------------------------------------------------------------------
// project_members — join table (spec §3.3, J9). P-block.
// ---------------------------------------------------------------------------

func buildProjectMembers(usersID, projectsID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("project_members")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("member", usersID, false, true)) // CASCADE, nullable
	c.Fields.Add(&core.TextField{Name: "comment", Max: maxTextBlob})
	c.Fields.Add(&core.NumberField{
		Name:    "role",
		OnlyInt: true,
		Help:    "20=Admin, 15=Member, 5=Guest (spec §1.3(a)).",
	})
	c.Fields.Add(&core.JSONField{Name: "view_props"})
	c.Fields.Add(&core.JSONField{Name: "default_props"})
	c.Fields.Add(&core.JSONField{Name: "preferences"})
	c.Fields.Add(&core.NumberField{Name: "sort_order"})
	c.Fields.Add(&core.BoolField{Name: "is_active"})

	// P-block tail.
	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// The partial UNIQUE (project, member) index is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// states — Plane `State` (spec §3.5). P-block.
// ---------------------------------------------------------------------------

func buildStates(usersID, projectsID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("states")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "description", Max: maxTextBlob})
	c.Fields.Add(&core.TextField{Name: "color", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "slug", Max: 100, Pattern: slugPattern})
	c.Fields.Add(&core.NumberField{Name: "sequence"})
	c.Fields.Add(&core.SelectField{
		Name:      "group",
		Values:    []string{"backlog", "unstarted", "started", "completed", "cancelled", "triage"},
		MaxSelect: 1,
	})
	c.Fields.Add(&core.BoolField{Name: "is_triage"})
	c.Fields.Add(&core.BoolField{Name: "default"})
	c.Fields.Add(&core.TextField{Name: "external_source", Max: 255})
	c.Fields.Add(&core.TextField{Name: "external_id", Max: 255})

	// P-block tail.
	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// The partial UNIQUE (name, project) index is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// labels — Plane `Label` (spec §3.5). W-block (project is nullable).
// ---------------------------------------------------------------------------

func buildLabels(usersID, projectsID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("labels")
	addAuditFields(c, usersID, true)

	// `parent` -> labels is a self-relation, added in wave 4 (§6.4 D10).
	c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "description", Max: maxTextBlob})
	c.Fields.Add(&core.TextField{Name: "color", Max: 255})
	c.Fields.Add(&core.NumberField{Name: "sort_order"})
	c.Fields.Add(&core.TextField{Name: "external_source", Max: 255})
	c.Fields.Add(&core.TextField{Name: "external_id", Max: 255})

	// W-block tail.
	c.Fields.Add(relation("workspace", workspacesID, true, true))
	c.Fields.Add(relation("project", projectsID, false, true))

	// The two conditional UNIQUE indexes are phase-3 indexes (§6.5).
	addIdentityIndexes(c, true)

	return c
}

func init() {
	m.Register(func(app core.App) error {
		usersID, err := collectionID(app, "users")
		if err != nil {
			return err
		}
		projectsID, err := collectionID(app, "projects")
		if err != nil {
			return err
		}
		workspacesID, err := collectionID(app, "workspaces")
		if err != nil {
			return err
		}

		if err := app.Save(buildProjectMembers(usersID, projectsID, workspacesID)); err != nil {
			return fmt.Errorf("create project_members: %w", err)
		}
		if err := app.Save(buildStates(usersID, projectsID, workspacesID)); err != nil {
			return fmt.Errorf("create states: %w", err)
		}
		if err := app.Save(buildLabels(usersID, projectsID, workspacesID)); err != nil {
			return fmt.Errorf("create labels: %w", err)
		}

		return nil
	}, func(app core.App) error {
		for _, name := range []string{"labels", "project_members", "states"} {
			c, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				return fmt.Errorf("down: find %q: %w", name, err)
			}
			if err := app.Delete(c); err != nil {
				return fmt.Errorf("down: delete %q: %w", name, err)
			}
		}
		return nil
	}, "1759000002_init_project_taxonomy.go")
}
