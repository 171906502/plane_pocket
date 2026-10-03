// Wave W9 (remainder): cycles, modules, intake, descriptions, notifications and
// workspace-level user links.
//
// Spec §6.3 W9 — `cycles`, `modules`, `intakes`, `descriptions`, `notifications`,
// `user_notification_preferences`, `workspace_user_links`, `deploy_boards`.
// (`issues` already exists from wave 1; `issues.parent` is deferred to W11.)
//
// `deploy_boards.intake` needs `intakes`, so `intakes` is created first in this
// file. The nine `notifications` indexes and the `deploy_boards.anchor` unique
// index reference only scalar columns and are created inline (§6.5 "safe in
// phase 1"). Partial UNIQUE indexes over relation columns go to W12.
package pb_migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// ---------------------------------------------------------------------------
// cycles — Django `Cycle` (spec §3.7). P-block.
// ---------------------------------------------------------------------------

func buildCycles(usersID, projectsID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("cycles")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "description", Max: maxTextBlob})
	c.Fields.Add(&core.DateField{Name: "start_date"})
	c.Fields.Add(&core.DateField{Name: "end_date"})
	c.Fields.Add(relation("owned_by", usersID, true, true)) // CASCADE
	c.Fields.Add(&core.JSONField{Name: "view_props"})
	c.Fields.Add(&core.NumberField{Name: "sort_order"})
	c.Fields.Add(&core.TextField{Name: "external_source", Max: 255})
	c.Fields.Add(&core.TextField{Name: "external_id", Max: 255})
	c.Fields.Add(&core.JSONField{Name: "progress_snapshot"})
	c.Fields.Add(&core.DateField{Name: "archived_at"})
	c.Fields.Add(&core.JSONField{Name: "logo_props"})
	c.Fields.Add(&core.TextField{
		Name: "timezone",
		Max:  255,
		Help: "IANA timezone (spec §1.3(h) REVIEW: text, not a select).",
	})
	c.Fields.Add(&core.NumberField{Name: "version", OnlyInt: true})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// No unique constraint (spec §3.7).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// modules — Django `Module` (spec §3.8). P-block.
// ---------------------------------------------------------------------------

func buildModules(usersID, projectsID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("modules")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "description", Max: maxTextBlob})
	c.Fields.Add(&core.JSONField{Name: "description_text"})
	c.Fields.Add(&core.JSONField{Name: "description_html"})
	c.Fields.Add(&core.DateField{Name: "start_date"})
	c.Fields.Add(&core.DateField{Name: "target_date"})
	c.Fields.Add(&core.SelectField{
		Name:      "status",
		Values:    []string{"backlog", "planned", "in-progress", "paused", "completed", "cancelled"},
		MaxSelect: 1,
	})
	c.Fields.Add(relation("lead", usersID, false, false)) // SET_NULL
	c.Fields.Add(&core.JSONField{Name: "view_props"})
	c.Fields.Add(&core.NumberField{Name: "sort_order"})
	c.Fields.Add(&core.TextField{Name: "external_source", Max: 255})
	c.Fields.Add(&core.TextField{Name: "external_id", Max: 255})
	c.Fields.Add(&core.DateField{Name: "archived_at"})
	c.Fields.Add(&core.JSONField{Name: "logo_props"})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// Partial UNIQUE (name, project) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// intakes — Django `Intake` (spec §3.9). P-block. Created before `deploy_boards`.
// ---------------------------------------------------------------------------

func buildIntakes(usersID, projectsID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("intakes")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "description", Max: maxTextBlob})
	c.Fields.Add(&core.BoolField{Name: "is_default"})
	c.Fields.Add(&core.JSONField{Name: "view_props"})
	c.Fields.Add(&core.JSONField{Name: "logo_props"})

	c.Fields.Add(relation("project", projectsID, true, true))
	c.Fields.Add(relation("workspace", workspacesID, true, true))

	// Partial UNIQUE (name, project) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// descriptions — Django `Description` (spec §3.12). W-block. 1:1 satellite of
// `issue_comments`.
// ---------------------------------------------------------------------------

func buildDescriptions(usersID, projectsID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("descriptions")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.JSONField{Name: "description_json"})
	c.Fields.Add(&core.EditorField{Name: "description_html", MaxSize: maxEditor})
	c.Fields.Add(&core.FileField{Name: "description_binary", MaxSelect: 1, Protected: true})
	c.Fields.Add(&core.TextField{Name: "description_stripped", Max: maxStripped})

	c.Fields.Add(relation("workspace", workspacesID, true, true))
	c.Fields.Add(relation("project", projectsID, false, true))

	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// notifications — Django `Notification` (spec §3.14). A-block.
// ---------------------------------------------------------------------------

func buildNotifications(usersID, projectsID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("notifications")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("workspace", workspacesID, true, true))  // CASCADE
	c.Fields.Add(relation("project", projectsID, false, true))     // CASCADE, nullable
	c.Fields.Add(&core.JSONField{Name: "data"})
	c.Fields.Add(uuidRef("entity_identifier"))
	c.Fields.Add(&core.TextField{Name: "entity_name", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "title", Max: maxTextBlob})
	c.Fields.Add(&core.JSONField{Name: "message"})
	c.Fields.Add(&core.EditorField{Name: "message_html", MaxSize: maxEditor})
	c.Fields.Add(&core.TextField{Name: "message_stripped", Max: maxStripped})
	c.Fields.Add(&core.TextField{Name: "sender", Required: true, Max: 255})
	c.Fields.Add(relation("triggered_by", usersID, false, false)) // SET_NULL
	c.Fields.Add(relation("receiver", usersID, true, true))       // CASCADE
	c.Fields.Add(&core.DateField{Name: "read_at"})
	c.Fields.Add(&core.DateField{Name: "snoozed_till"})
	c.Fields.Add(&core.DateField{Name: "archived_at"})

	addIdentityIndexes(c, true)
	// The nine declared notification indexes (spec §3.14) — scalar/relation mix
	// but all columns exist here, and §6.5 lists them as phase-1-safe.
	c.Indexes = append(c.Indexes,
		"CREATE INDEX `notif_entity_identifier_idx` ON `notifications` (`entity_identifier`)",
		"CREATE INDEX `notif_entity_name_idx` ON `notifications` (`entity_name`)",
		"CREATE INDEX `notif_read_at_idx` ON `notifications` (`read_at`)",
		"CREATE INDEX `notif_entity_idx` ON `notifications` (`receiver`, `read_at`)",
		"CREATE INDEX `notif_receiver_status_idx` ON `notifications` (`receiver`, `workspace`, `read_at`, `created_at`)",
		"CREATE INDEX `notif_receiver_entity_idx` ON `notifications` (`receiver`, `workspace`, `entity_name`, `read_at`)",
		"CREATE INDEX `notif_receiver_state_idx` ON `notifications` (`receiver`, `workspace`, `snoozed_till`, `archived_at`)",
		"CREATE INDEX `notif_receiver_sender_idx` ON `notifications` (`receiver`, `workspace`, `sender`)",
		"CREATE INDEX `notif_entity_lookup_idx` ON `notifications` (`workspace`, `entity_identifier`, `entity_name`)",
	)

	return c
}

// ---------------------------------------------------------------------------
// user_notification_preferences — Django `UserNotificationPreference` (spec §3.14).
// A-block. No unique constraint.
// ---------------------------------------------------------------------------

func buildUserNotificationPreferences(usersID, projectsID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("user_notification_preferences")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("user", usersID, true, true))           // CASCADE
	c.Fields.Add(relation("workspace", workspacesID, false, true)) // CASCADE, nullable
	c.Fields.Add(relation("project", projectsID, false, true))     // CASCADE, nullable
	c.Fields.Add(&core.BoolField{Name: "property_change"})
	c.Fields.Add(&core.BoolField{Name: "state_change"})
	c.Fields.Add(&core.BoolField{Name: "comment"})
	c.Fields.Add(&core.BoolField{Name: "mention"})
	c.Fields.Add(&core.BoolField{Name: "issue_completed"})

	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// workspace_user_links — Django `WorkspaceUserLink` (spec §3.2). W-block.
// `url` is a Django TextField, so it stays `text` (spec §3.2).
// ---------------------------------------------------------------------------

func buildWorkspaceUserLinks(usersID, projectsID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("workspace_user_links")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "title", Max: 255})
	c.Fields.Add(&core.TextField{Name: "url", Max: maxTextBlob})
	c.Fields.Add(&core.JSONField{Name: "metadata"})
	c.Fields.Add(relation("owner", usersID, true, true)) // CASCADE

	c.Fields.Add(relation("workspace", workspacesID, true, true))
	c.Fields.Add(relation("project", projectsID, false, true))

	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// deploy_boards — Django `DeployBoard` (spec §3.15). W-block.
// ---------------------------------------------------------------------------

func buildDeployBoards(usersID, projectsID, workspacesID, intakesID string) *core.Collection {
	c := core.NewBaseCollection("deploy_boards")
	addAuditFields(c, usersID, true)

	c.Fields.Add(uuidRef("entity_identifier"))
	c.Fields.Add(&core.TextField{Name: "entity_name", Max: 30})
	c.Fields.Add(&core.TextField{Name: "anchor", Required: true, Max: 255})
	c.Fields.Add(&core.BoolField{Name: "is_comments_enabled"})
	c.Fields.Add(&core.BoolField{Name: "is_reactions_enabled"})
	c.Fields.Add(relation("intake", intakesID, false, false)) // SET_NULL
	c.Fields.Add(&core.BoolField{Name: "is_votes_enabled"})
	c.Fields.Add(&core.JSONField{Name: "view_props"})
	c.Fields.Add(&core.BoolField{Name: "is_activity_enabled"})
	c.Fields.Add(&core.BoolField{Name: "is_disabled"})

	c.Fields.Add(relation("workspace", workspacesID, true, true))
	c.Fields.Add(relation("project", projectsID, false, true))

	addIdentityIndexes(c, true)
	c.Indexes = append(c.Indexes,
		"CREATE UNIQUE INDEX `idx_deploy_boards_anchor` ON `deploy_boards` (`anchor`)",
	)
	// `deploy_board_unique_entity_name_entity_identifier_...` is a phase-3 index (§6.5).

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

		if err := app.Save(buildCycles(usersID, projectsID, workspacesID)); err != nil {
			return fmt.Errorf("create cycles: %w", err)
		}
		if err := app.Save(buildModules(usersID, projectsID, workspacesID)); err != nil {
			return fmt.Errorf("create modules: %w", err)
		}
		if err := app.Save(buildIntakes(usersID, projectsID, workspacesID)); err != nil {
			return fmt.Errorf("create intakes: %w", err)
		}
		intakesID, err := collectionID(app, "intakes")
		if err != nil {
			return err
		}

		if err := app.Save(buildDescriptions(usersID, projectsID, workspacesID)); err != nil {
			return fmt.Errorf("create descriptions: %w", err)
		}
		if err := app.Save(buildNotifications(usersID, projectsID, workspacesID)); err != nil {
			return fmt.Errorf("create notifications: %w", err)
		}
		if err := app.Save(buildUserNotificationPreferences(usersID, projectsID, workspacesID)); err != nil {
			return fmt.Errorf("create user_notification_preferences: %w", err)
		}
		if err := app.Save(buildWorkspaceUserLinks(usersID, projectsID, workspacesID)); err != nil {
			return fmt.Errorf("create workspace_user_links: %w", err)
		}
		if err := app.Save(buildDeployBoards(usersID, projectsID, workspacesID, intakesID)); err != nil {
			return fmt.Errorf("create deploy_boards: %w", err)
		}

		return nil
	}, func(app core.App) error {
		for _, name := range []string{
			"deploy_boards", "workspace_user_links", "user_notification_preferences",
			"notifications", "descriptions", "intakes", "modules", "cycles",
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
	}, "1759000008_init_cycles_modules_intake_notifications.go")
}
