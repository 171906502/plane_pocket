// Wave W7 (remainder): project-scoped taxonomy and membership extras.
//
// Spec §6.3 W7 — `estimates`, `estimate_points`, `project_member_invites`,
// `project_public_members`, `project_user_properties`, `project_identifiers`,
// `project_issue_types`. (`states`, `project_members` and `labels` already exist
// from wave 1.)
//
// `project_identifiers` is the one collection in the schema with an integer PK
// (spec §2.7): it uses PB's native `id` plus a unique `legacy_pk` number and NO
// `uuid` field. `project_issue_types` depends on `issue_types` (created in W5).
// Partial UNIQUE indexes are appended in the W12 relation-index file (§6.5).
package pb_migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// ---------------------------------------------------------------------------
// estimates — Django `Estimate` (spec §3.5). P-block.
// ---------------------------------------------------------------------------

func buildEstimates(usersID, projectsID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("estimates")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "description", Max: maxTextBlob})
	c.Fields.Add(&core.SelectField{Name: "type", Values: []string{"categories", "points"}, MaxSelect: 1})
	c.Fields.Add(&core.BoolField{Name: "last_used"})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// Partial UNIQUE (name, project) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// estimate_points — Django `EstimatePoint` (spec §3.5). P-block.
// ---------------------------------------------------------------------------

func buildEstimatePoints(usersID, projectsID, workspacesID, estimatesID string) *core.Collection {
	c := core.NewBaseCollection("estimate_points")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("estimate", estimatesID, true, true)) // CASCADE
	c.Fields.Add(&core.NumberField{Name: "key", OnlyInt: true, Min: f64(0)})
	c.Fields.Add(&core.TextField{Name: "description", Max: maxTextBlob})
	c.Fields.Add(&core.TextField{Name: "value", Required: true, Max: 255})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// No unique constraint (spec §3.5).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// project_member_invites — Django `ProjectMemberInvite` (spec §3.3). P-block.
//
// No unique constraint on (email, project) — do not invent one (spec §3.3 REVIEW).
// ---------------------------------------------------------------------------

func buildProjectMemberInvites(usersID, projectsID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("project_member_invites")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "email", Required: true, Max: 255})
	c.Fields.Add(&core.BoolField{Name: "accepted"})
	c.Fields.Add(&core.TextField{Name: "token", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "message", Max: maxTextBlob})
	c.Fields.Add(&core.DateField{Name: "responded_at"})
	c.Fields.Add(&core.NumberField{Name: "role", OnlyInt: true})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// project_public_members — Django `ProjectPublicMember` (spec §3.3). P-block.
// ---------------------------------------------------------------------------

func buildProjectPublicMembers(usersID, projectsID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("project_public_members")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("member", usersID, true, true)) // CASCADE

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// Partial UNIQUE (project, member) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// project_user_properties — Django `ProjectUserProperty` (spec §3.3). P-block.
// ---------------------------------------------------------------------------

func buildProjectUserProperties(usersID, projectsID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("project_user_properties")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("user", usersID, true, true)) // CASCADE
	c.Fields.Add(&core.JSONField{Name: "filters"})
	c.Fields.Add(&core.JSONField{Name: "display_filters"})
	c.Fields.Add(&core.JSONField{Name: "display_properties"})
	c.Fields.Add(&core.JSONField{Name: "rich_filters"})
	c.Fields.Add(&core.JSONField{Name: "preferences"})
	c.Fields.Add(&core.NumberField{Name: "sort_order"})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// Partial UNIQUE (user, project) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// project_identifiers — Django `ProjectIdentifier` (spec §2.7, §3.3).
//
// A-block minus the UUID identity: PB native `id` + unique `legacy_pk` number.
// No `uuid` field (the only such collection besides the skipped `sessions`).
// ---------------------------------------------------------------------------

func buildProjectIdentifiers(usersID, projectsID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("project_identifiers")

	c.Fields.Add(&core.NumberField{
		Name:    "legacy_pk",
		OnlyInt: true,
		Help:    "Django BigAutoField primary key; the API `id` is this integer (spec §2.7).",
	})
	c.Fields.Add(&core.AutodateField{Name: "created_at", OnCreate: true})
	c.Fields.Add(&core.AutodateField{Name: "updated_at", OnUpdate: true})
	c.Fields.Add(relation("created_by", usersID, false, false)) // SET_NULL
	c.Fields.Add(relation("updated_by", usersID, false, false)) // SET_NULL
	c.Fields.Add(&core.DateField{Name: "deleted_at"})

	c.Fields.Add(relation("workspace", workspacesID, false, true)) // CASCADE, nullable
	c.Fields.Add(relation("project", projectsID, true, true))      // CASCADE + unique index
	c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 12})

	c.Indexes = append(c.Indexes,
		"CREATE UNIQUE INDEX `idx_project_identifiers_legacy_pk` ON `project_identifiers` (`legacy_pk`)",
		"CREATE INDEX `idx_project_identifiers_deleted_at` ON `project_identifiers` (`deleted_at`)",
	)
	// `unique_name_workspace_...` and `idx_project_identifiers_project` reference
	// relation columns -> phase-3 (§6.5, W12).

	return c
}

// ---------------------------------------------------------------------------
// project_issue_types — Django `ProjectIssueType` (spec §3.5). P-block.
// ---------------------------------------------------------------------------

func buildProjectIssueTypes(usersID, projectsID, workspacesID, issueTypesID string) *core.Collection {
	c := core.NewBaseCollection("project_issue_types")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("issue_type", issueTypesID, true, true)) // CASCADE
	c.Fields.Add(&core.NumberField{Name: "level", OnlyInt: true, Min: f64(0)})
	c.Fields.Add(&core.BoolField{Name: "is_default"})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// Partial UNIQUE (project, issue_type) is a phase-3 index (§6.5).
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
		issueTypesID, err := collectionID(app, "issue_types")
		if err != nil {
			return err
		}

		if err := app.Save(buildEstimates(usersID, projectsID, workspacesID)); err != nil {
			return fmt.Errorf("create estimates: %w", err)
		}
		estimatesID, err := collectionID(app, "estimates")
		if err != nil {
			return err
		}

		if err := app.Save(buildEstimatePoints(usersID, projectsID, workspacesID, estimatesID)); err != nil {
			return fmt.Errorf("create estimate_points: %w", err)
		}
		if err := app.Save(buildProjectMemberInvites(usersID, projectsID, workspacesID)); err != nil {
			return fmt.Errorf("create project_member_invites: %w", err)
		}
		if err := app.Save(buildProjectPublicMembers(usersID, projectsID, workspacesID)); err != nil {
			return fmt.Errorf("create project_public_members: %w", err)
		}
		if err := app.Save(buildProjectUserProperties(usersID, projectsID, workspacesID)); err != nil {
			return fmt.Errorf("create project_user_properties: %w", err)
		}
		if err := app.Save(buildProjectIdentifiers(usersID, projectsID, workspacesID)); err != nil {
			return fmt.Errorf("create project_identifiers: %w", err)
		}
		if err := app.Save(buildProjectIssueTypes(usersID, projectsID, workspacesID, issueTypesID)); err != nil {
			return fmt.Errorf("create project_issue_types: %w", err)
		}

		return nil
	}, func(app core.App) error {
		for _, name := range []string{
			"project_issue_types", "project_identifiers", "project_user_properties",
			"project_public_members", "project_member_invites", "estimate_points", "estimates",
		} {
			c, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				return fmt.Errorf("down: find %q: %w", name, err)
			}
			if err := app.Delete(c); err != nil {
				return fmt.Errorf("down: delete %q: %w", name, err)
			}
		}
		return nil
	}, "1759000007_init_project_extras.go")
}
