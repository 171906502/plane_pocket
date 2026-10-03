// Wave W5: workspace-scoped extras and the two workspace-level taxonomy tables.
//
// Spec §6.3 W5 — `workspace_member_invites`, `teams`, `workspace_themes`,
// `workspace_user_properties`, `workspace_home_preferences`,
// `workspace_user_preferences`, `analytic_views`, `issue_types`, `stickies`,
// `email_notification_logs`. (`workspace_members` already exists from wave 1.)
//
// All relation targets (`users`, `workspaces`) exist, so nothing is deferred.
// Their partial UNIQUE indexes reference relation columns and are appended in
// the W12 relation-index file (§6.5).
package pb_migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// ---------------------------------------------------------------------------
// workspace_member_invites — Django `WorkspaceMemberInvite` (spec §3.2). A-block.
// ---------------------------------------------------------------------------

func buildWorkspaceMemberInvites(usersID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("workspace_member_invites")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("workspace", workspacesID, true, true)) // CASCADE
	c.Fields.Add(&core.TextField{Name: "email", Required: true, Max: 255})
	c.Fields.Add(&core.BoolField{Name: "accepted"})
	c.Fields.Add(&core.TextField{Name: "token", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "message", Max: maxTextBlob})
	c.Fields.Add(&core.DateField{Name: "responded_at"})
	c.Fields.Add(&core.NumberField{
		Name:    "role",
		OnlyInt: true,
		Help:    "20=Admin, 15=Member, 5=Guest (spec §1.3(a)).",
	})

	// Partial UNIQUE (email, workspace) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// teams — Django `Team` (spec §3.2). A-block. Dormant in v1.4.2 but kept.
// ---------------------------------------------------------------------------

func buildTeams(usersID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("teams")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "description", Max: maxTextBlob})
	c.Fields.Add(relation("workspace", workspacesID, true, true)) // CASCADE
	c.Fields.Add(&core.JSONField{Name: "logo_props"})

	// Partial UNIQUE (name, workspace) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// workspace_themes — Django `WorkspaceTheme` (spec §3.2). A-block.
// ---------------------------------------------------------------------------

func buildWorkspaceThemes(usersID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("workspace_themes")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("workspace", workspacesID, true, true)) // CASCADE
	c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 300})
	c.Fields.Add(relation("actor", usersID, true, true)) // CASCADE
	c.Fields.Add(&core.JSONField{Name: "colors"})

	// Partial UNIQUE (workspace, name) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// workspace_user_properties — Django `WorkspaceUserProperties` (spec §3.2). A-block.
// ---------------------------------------------------------------------------

func buildWorkspaceUserProperties(usersID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("workspace_user_properties")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("workspace", workspacesID, true, true)) // CASCADE
	c.Fields.Add(relation("user", usersID, true, true))           // CASCADE
	c.Fields.Add(&core.JSONField{Name: "filters"})
	c.Fields.Add(&core.JSONField{Name: "display_filters"})
	c.Fields.Add(&core.JSONField{Name: "display_properties"})
	c.Fields.Add(&core.JSONField{Name: "rich_filters"})
	c.Fields.Add(&core.NumberField{Name: "navigation_project_limit", OnlyInt: true})
	c.Fields.Add(&core.SelectField{
		Name:      "navigation_control_preference",
		Values:    []string{"ACCORDION", "TABBED"},
		MaxSelect: 1,
	})

	// Partial UNIQUE (workspace, user) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// workspace_home_preferences — Django `WorkspaceHomePreference` (spec §3.2). A-block.
// ---------------------------------------------------------------------------

func buildWorkspaceHomePreferences(usersID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("workspace_home_preferences")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("workspace", workspacesID, true, true)) // CASCADE
	c.Fields.Add(relation("user", usersID, true, true))           // CASCADE
	c.Fields.Add(&core.TextField{Name: "key", Required: true, Max: 255})
	c.Fields.Add(&core.BoolField{Name: "is_enabled"})
	c.Fields.Add(&core.JSONField{Name: "config"})
	c.Fields.Add(&core.NumberField{Name: "sort_order"})

	// Partial UNIQUE (workspace, user, key) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// workspace_user_preferences — Django `WorkspaceUserPreference` (spec §3.2). A-block.
// ---------------------------------------------------------------------------

func buildWorkspaceUserPreferences(usersID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("workspace_user_preferences")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("workspace", workspacesID, true, true)) // CASCADE
	c.Fields.Add(relation("user", usersID, true, true))           // CASCADE
	c.Fields.Add(&core.TextField{Name: "key", Required: true, Max: 255})
	c.Fields.Add(&core.BoolField{Name: "is_pinned"})
	c.Fields.Add(&core.NumberField{Name: "sort_order"})

	// Partial UNIQUE (workspace, user, key) is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// analytic_views — Django `AnalyticView` (spec §3.13). A-block.
// ---------------------------------------------------------------------------

func buildAnalyticViews(usersID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("analytic_views")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("workspace", workspacesID, true, true)) // CASCADE
	c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "description", Max: maxTextBlob})
	c.Fields.Add(&core.JSONField{Name: "query", MaxSize: maxJSONRich})
	c.Fields.Add(&core.JSONField{Name: "query_dict"})

	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// issue_types — Django `IssueType` (spec §3.5). A-block.
// ---------------------------------------------------------------------------

func buildIssueTypes(usersID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("issue_types")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("workspace", workspacesID, true, true)) // CASCADE
	c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "description", Max: maxTextBlob})
	c.Fields.Add(&core.JSONField{Name: "logo_props"})
	c.Fields.Add(&core.BoolField{Name: "is_epic"})
	c.Fields.Add(&core.BoolField{Name: "is_default"})
	c.Fields.Add(&core.BoolField{Name: "is_active"})
	c.Fields.Add(&core.NumberField{Name: "level"})
	c.Fields.Add(&core.TextField{Name: "external_source", Max: 255})
	c.Fields.Add(&core.TextField{Name: "external_id", Max: 255})

	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// stickies — Django `Sticky` (spec §3.13). A-block.
// ---------------------------------------------------------------------------

func buildStickies(usersID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("stickies")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "name", Max: maxTextBlob})
	c.Fields.Add(&core.JSONField{Name: "description"})
	c.Fields.Add(&core.EditorField{Name: "description_html", MaxSize: maxEditor})
	c.Fields.Add(&core.TextField{Name: "description_stripped", Max: maxStripped})
	c.Fields.Add(&core.FileField{Name: "description_binary", MaxSelect: 1, Protected: true})
	c.Fields.Add(&core.JSONField{Name: "logo_props"})
	c.Fields.Add(&core.TextField{Name: "color", Max: 255})
	c.Fields.Add(&core.TextField{Name: "background_color", Max: 255})
	c.Fields.Add(relation("workspace", workspacesID, true, true)) // CASCADE
	c.Fields.Add(relation("owner", usersID, true, true))          // CASCADE
	c.Fields.Add(&core.NumberField{Name: "sort_order"})

	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// email_notification_logs — Django `EmailNotificationLog` (spec §3.14). A-block.
//
// Two distinct relations to `users` (receiver, triggered_by) — permitted.
// ---------------------------------------------------------------------------

func buildEmailNotificationLogs(usersID string) *core.Collection {
	c := core.NewBaseCollection("email_notification_logs")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("receiver", usersID, true, true))     // CASCADE
	c.Fields.Add(relation("triggered_by", usersID, true, true)) // CASCADE
	c.Fields.Add(uuidRef("entity_identifier"))
	c.Fields.Add(&core.TextField{Name: "entity_name", Required: true, Max: 255})
	c.Fields.Add(&core.JSONField{Name: "data"})
	c.Fields.Add(&core.DateField{Name: "processed_at"})
	c.Fields.Add(&core.DateField{Name: "sent_at"})
	c.Fields.Add(&core.TextField{Name: "entity", Required: true, Max: 200})
	c.Fields.Add(&core.TextField{Name: "old_value", Max: 300})
	c.Fields.Add(&core.TextField{Name: "new_value", Max: 300})

	addIdentityIndexes(c, true)

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

		builders := []struct {
			name string
			fn   func() *core.Collection
		}{
			{"workspace_member_invites", func() *core.Collection { return buildWorkspaceMemberInvites(usersID, workspacesID) }},
			{"teams", func() *core.Collection { return buildTeams(usersID, workspacesID) }},
			{"workspace_themes", func() *core.Collection { return buildWorkspaceThemes(usersID, workspacesID) }},
			{"workspace_user_properties", func() *core.Collection { return buildWorkspaceUserProperties(usersID, workspacesID) }},
			{"workspace_home_preferences", func() *core.Collection { return buildWorkspaceHomePreferences(usersID, workspacesID) }},
			{"workspace_user_preferences", func() *core.Collection { return buildWorkspaceUserPreferences(usersID, workspacesID) }},
			{"analytic_views", func() *core.Collection { return buildAnalyticViews(usersID, workspacesID) }},
			{"issue_types", func() *core.Collection { return buildIssueTypes(usersID, workspacesID) }},
			{"stickies", func() *core.Collection { return buildStickies(usersID, workspacesID) }},
			{"email_notification_logs", func() *core.Collection { return buildEmailNotificationLogs(usersID) }},
		}
		for _, b := range builders {
			if err := app.Save(b.fn()); err != nil {
				return fmt.Errorf("create %s: %w", b.name, err)
			}
		}

		return nil
	}, func(app core.App) error {
		for _, name := range []string{
			"email_notification_logs", "stickies", "issue_types", "analytic_views",
			"workspace_user_preferences", "workspace_home_preferences",
			"workspace_user_properties", "workspace_themes", "teams",
			"workspace_member_invites",
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
	}, "1759000006_init_workspace_extras.go")
}
