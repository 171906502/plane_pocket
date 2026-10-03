// Wave 3: `issues`, `issue_assignees`, `issue_labels` (spec §6.3 wave W9/W10).
//
// `issues` is the core table (spec §3.6); `issues.parent` is a self-relation and
// is deferred to wave 4 (§6.4 D11). The two join tables are the M2M through
// models of `Issue.assignees` and `Issue.labels` (spec §5, J1/J2).
package pb_migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// ---------------------------------------------------------------------------
// issues — Plane `Issue` (spec §3.6). P-block.
// ---------------------------------------------------------------------------

func buildIssues(usersID, projectsID, workspacesID, statesID string) *core.Collection {
	c := core.NewBaseCollection("issues")
	addAuditFields(c, usersID, true)

	// `parent` -> issues is a self-relation, added in wave 4 (§6.4 D11).
	c.Fields.Add(relation("state", statesID, false, true)) // CASCADE, nullable
	// `point` is json, not number: 0 is a legal estimate point AND the "unset"
	// marker, so the two would collide (spec §1.3(e)).
	c.Fields.Add(&core.JSONField{Name: "point", MaxSize: 64})
	// estimate_point -> estimate_points is outside this vertical slice.
	c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 255})
	c.Fields.Add(&core.JSONField{Name: "description_json", MaxSize: maxJSONRich})
	c.Fields.Add(&core.EditorField{Name: "description_html", MaxSize: maxEditor})
	c.Fields.Add(&core.TextField{Name: "description_stripped", Max: maxStripped})
	// BinaryField -> file (single, protected); spec §1.3(c).
	c.Fields.Add(&core.FileField{Name: "description_binary", MaxSelect: 1, Protected: true})
	c.Fields.Add(&core.SelectField{
		Name:      "priority",
		Values:    []string{"urgent", "high", "medium", "low", "none"},
		MaxSelect: 1,
	})
	c.Fields.Add(&core.DateField{Name: "start_date"})
	c.Fields.Add(&core.DateField{Name: "target_date"})
	c.Fields.Add(&core.NumberField{Name: "sequence_id", OnlyInt: true, Min: f64(1)})
	c.Fields.Add(&core.NumberField{Name: "sort_order"})
	c.Fields.Add(&core.DateField{Name: "completed_at"})
	c.Fields.Add(&core.DateField{Name: "archived_at"}) // DateField, unlike Project.archived_at
	c.Fields.Add(&core.BoolField{Name: "is_draft"})
	c.Fields.Add(&core.TextField{Name: "external_source", Max: 255})
	c.Fields.Add(&core.TextField{Name: "external_id", Max: 255})
	// `type` -> issue_types is outside this vertical slice.

	// P-block tail.
	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	addIdentityIndexes(c, true)
	// Phase-1 safe indexes (spec §6.5 explicitly lists these as scalar-safe).
	c.Indexes = append(c.Indexes,
		"CREATE INDEX `idx_issues_project_state` ON `issues` (`project`, `state`)",
		"CREATE INDEX `idx_issues_sequence_id` ON `issues` (`project`, `sequence_id`)",
	)

	return c
}

// ---------------------------------------------------------------------------
// issue_assignees — `Issue.assignees` through model (spec §3.6, J1). P-block.
// ---------------------------------------------------------------------------

func buildIssueAssignees(usersID, projectsID, workspacesID, issuesID string) *core.Collection {
	c := core.NewBaseCollection("issue_assignees")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("issue", issuesID, true, true))
	c.Fields.Add(relation("assignee", usersID, true, true))

	// P-block tail.
	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// The partial UNIQUE (issue, assignee) index is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// issue_labels — `Issue.labels` through model (spec §3.6, J2). P-block.
//
// Deliberately has NO unique index: Plane declares none, so duplicate
// (issue, label) rows are possible and must stay possible (spec §5.1).
// ---------------------------------------------------------------------------

func buildIssueLabels(usersID, projectsID, workspacesID, issuesID, labelsID string) *core.Collection {
	c := core.NewBaseCollection("issue_labels")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("issue", issuesID, true, true))
	c.Fields.Add(relation("label", labelsID, true, true))

	// P-block tail.
	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

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
		statesID, err := collectionID(app, "states")
		if err != nil {
			return err
		}
		labelsID, err := collectionID(app, "labels")
		if err != nil {
			return err
		}

		if err := app.Save(buildIssues(usersID, projectsID, workspacesID, statesID)); err != nil {
			return fmt.Errorf("create issues: %w", err)
		}
		issuesID, err := collectionID(app, "issues")
		if err != nil {
			return err
		}

		if err := app.Save(buildIssueAssignees(usersID, projectsID, workspacesID, issuesID)); err != nil {
			return fmt.Errorf("create issue_assignees: %w", err)
		}
		if err := app.Save(buildIssueLabels(usersID, projectsID, workspacesID, issuesID, labelsID)); err != nil {
			return fmt.Errorf("create issue_labels: %w", err)
		}

		return nil
	}, func(app core.App) error {
		for _, name := range []string{"issue_labels", "issue_assignees", "issues"} {
			c, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				return fmt.Errorf("down: find %q: %w", name, err)
			}
			if err := app.Delete(c); err != nil {
				return fmt.Errorf("down: delete %q: %w", name, err)
			}
		}
		return nil
	}, "1759000003_init_issue_core.go")
}
