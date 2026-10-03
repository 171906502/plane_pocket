// Wave 4: phase 2 (deferred relations) + phase 3 (relation-bearing indexes),
// per spec §6.2/§6.4/§6.5.
//
// Phase 2 — the three deferred relation fields in this vertical slice:
//   D4  projects.default_state -> states
//   D10 labels.parent          -> labels (self)
//   D11 issues.parent          -> issues (self)
//
// Phase 3 — every partial UNIQUE index that references a relation column. They
// are added last because the relation columns had to exist first.
//
// `issue_labels` intentionally gets no unique index (spec §5.1 J2).
package pb_migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// patchCollection fetches a collection by name, applies fn to it and saves it.
func patchCollection(app core.App, name string, fn func(c *core.Collection)) error {
	c, err := app.FindCollectionByNameOrId(name)
	if err != nil {
		return fmt.Errorf("patch %q: %w", name, err)
	}
	fn(c)
	if err := app.Save(c); err != nil {
		return fmt.Errorf("save %q: %w", name, err)
	}
	return nil
}

// relationIndexes is the phase-3 index set for this vertical slice. The index
// names are the Django constraint names preserved verbatim (spec §6.5).
var relationIndexes = map[string][]string{
	"projects": {
		"CREATE UNIQUE INDEX `project_unique_identifier_workspace_when_deleted_at_null` ON `projects` (`identifier`, `workspace`) WHERE deleted_at = ''",
		"CREATE UNIQUE INDEX `project_unique_name_workspace_when_deleted_at_null` ON `projects` (`name`, `workspace`) WHERE deleted_at = ''",
	},
	"workspace_members": {
		"CREATE UNIQUE INDEX `workspace_member_unique_workspace_member_when_deleted_at_null` ON `workspace_members` (`workspace`, `member`) WHERE deleted_at = ''",
	},
	"project_members": {
		"CREATE UNIQUE INDEX `project_member_unique_project_member_when_deleted_at_null` ON `project_members` (`project`, `member`) WHERE deleted_at = ''",
	},
	"states": {
		"CREATE UNIQUE INDEX `state_unique_name_project_when_deleted_at_null` ON `states` (`name`, `project`) WHERE deleted_at = ''",
	},
	"labels": {
		"CREATE UNIQUE INDEX `unique_name_when_project_null_and_not_deleted` ON `labels` (`name`) WHERE project = '' AND deleted_at = ''",
		"CREATE UNIQUE INDEX `unique_project_name_when_not_deleted` ON `labels` (`project`, `name`) WHERE project != '' AND deleted_at = ''",
	},
	"issue_assignees": {
		"CREATE UNIQUE INDEX `issue_assignee_unique_issue_assignee_when_deleted_at_null` ON `issue_assignees` (`issue`, `assignee`) WHERE deleted_at = ''",
	},
}

// relationIndexNames returns the index names (first token after INDEX) for a
// collection, so the down migration can remove exactly what the up created.
func relationIndexNames(collection string) []string {
	names := make([]string, 0, len(relationIndexes[collection]))
	for _, ddl := range relationIndexes[collection] {
		names = append(names, indexName(ddl))
	}
	return names
}

// indexName extracts the index name from a CREATE [UNIQUE] INDEX `name` ON ...
// DDL string.
func indexName(ddl string) string {
	for i := 0; i < len(ddl); i++ {
		if ddl[i] == '`' {
			for j := i + 1; j < len(ddl); j++ {
				if ddl[j] == '`' {
					return ddl[i+1 : j]
				}
			}
		}
	}
	return ""
}

func init() {
	m.Register(func(app core.App) error {
		statesID, err := collectionID(app, "states")
		if err != nil {
			return err
		}

		// Phase 2 — deferred relations.
		if err := patchCollection(app, "projects", func(c *core.Collection) {
			c.Fields.Add(relation("default_state", statesID, false, false)) // SET_NULL
		}); err != nil {
			return err
		}
		if err := patchCollection(app, "labels", func(c *core.Collection) {
			c.Fields.Add(relation("parent", c.Id, false, true)) // self, CASCADE
		}); err != nil {
			return err
		}
		if err := patchCollection(app, "issues", func(c *core.Collection) {
			c.Fields.Add(relation("parent", c.Id, false, true)) // self, CASCADE
		}); err != nil {
			return err
		}

		// Phase 3 — relation-bearing indexes.
		for name, ddls := range relationIndexes {
			if err := patchCollection(app, name, func(c *core.Collection) {
				c.Indexes = append(c.Indexes, ddls...)
			}); err != nil {
				return err
			}
		}

		return nil
	}, func(app core.App) error {
		// Remove the phase-3 indexes first, then the phase-2 fields.
		for name := range relationIndexes {
			names := relationIndexNames(name)
			if err := patchCollection(app, name, func(c *core.Collection) {
				for _, idxName := range names {
					c.RemoveIndex(idxName)
				}
			}); err != nil {
				return err
			}
		}

		if err := patchCollection(app, "issues", func(c *core.Collection) {
			c.Fields.RemoveByName("parent")
		}); err != nil {
			return err
		}
		if err := patchCollection(app, "labels", func(c *core.Collection) {
			c.Fields.RemoveByName("parent")
		}); err != nil {
			return err
		}
		if err := patchCollection(app, "projects", func(c *core.Collection) {
			c.Fields.RemoveByName("default_state")
		}); err != nil {
			return err
		}

		return nil
	}, "1759000004_deferred_relations_and_indexes.go")
}
