// Package pb_migrations contains the Plane -> PocketBase collection migrations.
//
// Every collection follows spec/schema-spec.md:
//   - §2.2  the normative UUID strategy: PocketBase keeps its native 15-char `id`
//     primary key, the Plane UUID lives in a dedicated unique `uuid` text field
//     at field position 1.
//   - §3.0  the standard blocks (A-block audit + soft delete, P-block, W-block).
//   - §6.3  the dependency-ordered create waves.
//
// This file is wave 1: `users`, `workspaces`, `workspace_members`, `projects`.
// It also holds the small helpers shared by the later waves.
package pb_migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

// defaultUsersCollectionID is the fixed id PocketBase assigns to its built-in
// `users` auth collection (see pocketbase-master/migrations/1640988000_init.go).
// The Plane `users` collection reuses it so that the system auth collections
// (_externalAuths, _authOrigins, _mfas, _otps) keep resolving their
// `collectionId` references.
const defaultUsersCollectionID = "_pb_users_auth_"

// uuidPattern is mandated verbatim by spec §2.2 for the `uuid` field of every
// collection.
const uuidPattern = `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`

// slugPattern is the Django SlugField validator pattern (spec §1.2).
const slugPattern = `^[-a-zA-Z0-9_]+$`

// Size caps used across the slice. PocketBase raises text/json/editor/file
// limits explicitly because their defaults are far below Plane's TextFields.
const (
	maxTextBlob = 1_000_000 // Django TextField()
	maxStripped = 2_000_000 // *_stripped columns
	maxJSONRich = 5 << 20   // rich description json (spec §3.6 "maxSize raised")
	maxEditor   = 5 << 20   // editor default is already 5 MB, set explicitly
)

// f64 is a helper for the *float64 Min/Max options of NumberField.
func f64(v float64) *float64 { return &v }

// uuidField returns the §2.2 identity field. It must be added as the first
// non-system field so that it lands at field index 1 (index 0 is PocketBase's
// auto-prepended `id`).
func uuidField() *core.TextField {
	return &core.TextField{
		Name:     "uuid",
		Required: true,
		Max:      36,
		Pattern:  uuidPattern,
		Help:     "Plane UUID primary key (Django <table>.id); the API `id` is projected from this field.",
	}
}

// relation builds a single-value relation field (spec §1.2 ForeignKey mapping).
func relation(name, targetCollectionID string, required, cascadeDelete bool) *core.RelationField {
	return &core.RelationField{
		Name:          name,
		CollectionId:  targetCollectionID,
		MaxSelect:     1,
		Required:      required,
		CascadeDelete: cascadeDelete,
	}
}

// addAuditFields appends the §3.0 A-block: `uuid` (position 1), the autodate
// audit columns, the two SET_NULL user relations and — when the model is a
// SoftDeleteModel — `deleted_at`.
func addAuditFields(c *core.Collection, usersID string, softDelete bool) {
	c.Fields.Add(uuidField())
	c.Fields.Add(&core.AutodateField{Name: "created_at", OnCreate: true})
	c.Fields.Add(&core.AutodateField{Name: "updated_at", OnUpdate: true})
	c.Fields.Add(relation("created_by", usersID, false, false)) // SET_NULL
	c.Fields.Add(relation("updated_by", usersID, false, false)) // SET_NULL
	if softDelete {
		c.Fields.Add(&core.DateField{Name: "deleted_at"})
	}
}

// addIdentityIndexes adds the two phase-1 indexes every collection carries:
// the unique `uuid` index and, for soft-deletable models, the `deleted_at`
// index every Plane list query filters on (§3.0 index note).
func addIdentityIndexes(c *core.Collection, softDelete bool) {
	c.Indexes = append(c.Indexes,
		fmt.Sprintf("CREATE UNIQUE INDEX `idx_%s_uuid` ON `%s` (`uuid`)", c.Name, c.Name),
	)
	if softDelete {
		c.Indexes = append(c.Indexes,
			fmt.Sprintf("CREATE INDEX `idx_%s_deleted_at` ON `%s` (`deleted_at`)", c.Name, c.Name),
		)
	}
}

// collectionID resolves a relation target's PocketBase collection id. Relation
// fields store PocketBase ids, so the target must be resolved at migration time
// (spec §6.1).
func collectionID(app core.App, name string) (string, error) {
	c, err := app.FindCollectionByNameOrId(name)
	if err != nil {
		return "", fmt.Errorf("relation target %q not found: %w", name, err)
	}
	return c.Id, nil
}

// ---------------------------------------------------------------------------
// users — Plane `User` (spec §3.1). The only auth collection in the schema.
//
// PocketBase ships a default `users` auth collection. It is not a system
// collection, so this migration drops it and creates the Plane one in its
// place (reusing the default id). See dropDefaultUsersCollection below.
// ---------------------------------------------------------------------------

func buildUsers() *core.Collection {
	c := core.NewAuthCollection("users", defaultUsersCollectionID)

	// NewAuthCollection() already appended PocketBase's system auth fields
	// (password, tokenKey, email, emailVisibility, verified), so insert `uuid`
	// explicitly at index 1 to honour the §2.3 "uuid is field index 1" rule.
	c.Fields.AddAt(1, uuidField())
	c.Fields.Add(&core.TextField{Name: "username", Required: true, Max: 128})
	c.Fields.Add(&core.TextField{Name: "mobile_number", Max: 255})
	// `email` is PocketBase's system auth field; declared here so it keeps
	// Plane's null=True semantics (Required=false, empty string == NULL).
	c.Fields.Add(&core.EmailField{
		Name:     "email",
		System:   true,
		Required: false,
		Help:     "Plane User.email (unique, nullable; '' == SQL NULL).",
	})
	c.Fields.Add(&core.TextField{Name: "display_name", Max: 255})
	c.Fields.Add(&core.TextField{Name: "first_name", Max: 255})
	c.Fields.Add(&core.TextField{Name: "last_name", Max: 255})
	c.Fields.Add(&core.TextField{Name: "avatar", Max: maxTextBlob})
	// avatar_asset / cover_image_asset are deferred in spec §6.4 (D1/D2): they
	// target `file_assets`, which is outside this vertical slice.
	c.Fields.Add(&core.URLField{Name: "cover_image", Help: "Plane cover_image URLField(max_length=800); length enforced in the Go layer."})
	c.Fields.Add(&core.AutodateField{Name: "date_joined", OnCreate: true})
	c.Fields.Add(&core.AutodateField{Name: "created_at", OnCreate: true})
	c.Fields.Add(&core.AutodateField{Name: "updated_at", OnUpdate: true})
	c.Fields.Add(&core.TextField{Name: "last_location", Max: 255})
	c.Fields.Add(&core.TextField{Name: "created_location", Max: 255})
	c.Fields.Add(&core.BoolField{Name: "is_superuser"})
	c.Fields.Add(&core.BoolField{Name: "is_managed"})
	c.Fields.Add(&core.BoolField{Name: "is_password_expired"})
	c.Fields.Add(&core.BoolField{Name: "is_active"})
	c.Fields.Add(&core.BoolField{Name: "is_staff"})
	c.Fields.Add(&core.BoolField{Name: "is_email_verified"})
	c.Fields.Add(&core.BoolField{Name: "is_password_autoset"})
	c.Fields.Add(&core.BoolField{Name: "is_password_reset_required"})
	c.Fields.Add(&core.TextField{Name: "token", Max: 64})
	c.Fields.Add(&core.DateField{Name: "last_active", Required: true})
	c.Fields.Add(&core.DateField{Name: "last_login_time"})
	c.Fields.Add(&core.DateField{Name: "last_logout_time"})
	c.Fields.Add(&core.TextField{Name: "last_login_ip", Max: 255})
	c.Fields.Add(&core.TextField{Name: "last_logout_ip", Max: 255})
	c.Fields.Add(&core.TextField{Name: "last_login_medium", Max: 20})
	c.Fields.Add(&core.TextField{Name: "last_login_uagent", Max: maxTextBlob})
	c.Fields.Add(&core.DateField{Name: "token_updated_at"})
	c.Fields.Add(&core.BoolField{Name: "is_bot"})
	c.Fields.Add(&core.TextField{Name: "bot_type", Max: 30})
	c.Fields.Add(&core.TextField{
		Name: "user_timezone",
		Max:  255,
		Help: "IANA timezone (spec §1.3(h) REVIEW: text, not a 600-value select).",
	})
	c.Fields.Add(&core.BoolField{Name: "is_email_valid"})
	c.Fields.Add(&core.DateField{Name: "masked_at"})

	// `users` is not a SoftDeleteModel (spec §3.1): no `deleted_at`.
	addIdentityIndexes(c, false)
	// `email` gets PocketBase's own partial unique index automatically.
	c.Indexes = append(c.Indexes, "CREATE UNIQUE INDEX `idx_users_username` ON `users` (`username`)")

	return c
}

// ---------------------------------------------------------------------------
// workspaces — Plane `Workspace` (spec §3.2). A-block.
// ---------------------------------------------------------------------------

func buildWorkspaces(usersID string) *core.Collection {
	c := core.NewBaseCollection("workspaces")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 80})
	c.Fields.Add(&core.TextField{Name: "logo", Max: maxTextBlob})
	// logo_asset deferred (spec §6.4 D3) — `file_assets` is outside this slice.
	c.Fields.Add(relation("owner", usersID, true, true)) // CASCADE
	c.Fields.Add(&core.TextField{Name: "slug", Required: true, Max: 48, Pattern: slugPattern})
	c.Fields.Add(&core.TextField{Name: "organization_size", Max: 20})
	c.Fields.Add(&core.TextField{
		Name: "timezone",
		Max:  255,
		Help: "IANA timezone (spec §1.3(h) REVIEW: text, not a select).",
	})
	c.Fields.Add(&core.TextField{Name: "background_color", Max: 255})

	addIdentityIndexes(c, true)
	c.Indexes = append(c.Indexes, "CREATE UNIQUE INDEX `idx_workspaces_slug` ON `workspaces` (`slug`)")

	return c
}

// ---------------------------------------------------------------------------
// workspace_members — join table (spec §3.2, J8). A-block.
// ---------------------------------------------------------------------------

func buildWorkspaceMembers(usersID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("workspace_members")
	addAuditFields(c, usersID, true)

	c.Fields.Add(relation("workspace", workspacesID, true, true))
	c.Fields.Add(relation("member", usersID, true, true))
	c.Fields.Add(&core.NumberField{
		Name:    "role",
		OnlyInt: true,
		Min:     f64(0),
		Help:    "20=Admin, 15=Member, 5=Guest (spec §1.3(a): number, not select).",
	})
	c.Fields.Add(&core.TextField{Name: "company_role", Max: maxTextBlob})
	c.Fields.Add(&core.JSONField{Name: "view_props"})
	c.Fields.Add(&core.JSONField{Name: "default_props"})
	c.Fields.Add(&core.JSONField{Name: "issue_props"})
	c.Fields.Add(&core.BoolField{Name: "is_active"})
	c.Fields.Add(&core.JSONField{Name: "getting_started_checklist"})
	c.Fields.Add(&core.JSONField{Name: "tips"})
	c.Fields.Add(&core.JSONField{Name: "explored_features"})

	// The partial UNIQUE (workspace, member) index is a phase-3 index (§6.5).
	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// projects — Plane `Project` (spec §3.3). A-block.
// ---------------------------------------------------------------------------

func buildProjects(usersID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("projects")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "name", Required: true, Max: 255})
	c.Fields.Add(&core.TextField{Name: "description", Max: maxTextBlob})
	c.Fields.Add(&core.JSONField{Name: "description_text"})
	c.Fields.Add(&core.JSONField{Name: "description_html"})
	c.Fields.Add(&core.NumberField{
		Name:    "network",
		OnlyInt: true,
		Min:     f64(0),
		Max:     f64(2),
		Help:    "0=Secret, 2=Public (spec §1.3(a)).",
	})
	c.Fields.Add(relation("workspace", workspacesID, true, true))
	c.Fields.Add(&core.TextField{Name: "identifier", Required: true, Max: 12})
	c.Fields.Add(relation("default_assignee", usersID, false, true)) // CASCADE
	c.Fields.Add(relation("project_lead", usersID, false, true))     // CASCADE
	c.Fields.Add(&core.TextField{Name: "emoji", Max: 255})
	c.Fields.Add(&core.JSONField{Name: "icon_prop"})
	c.Fields.Add(&core.BoolField{Name: "module_view"})
	c.Fields.Add(&core.BoolField{Name: "cycle_view"})
	c.Fields.Add(&core.BoolField{Name: "issue_views_view"})
	c.Fields.Add(&core.BoolField{Name: "page_view"})
	c.Fields.Add(&core.BoolField{Name: "intake_view"})
	c.Fields.Add(&core.BoolField{Name: "is_time_tracking_enabled"})
	c.Fields.Add(&core.BoolField{Name: "is_issue_type_enabled"})
	c.Fields.Add(&core.BoolField{Name: "guest_view_all_features"})
	c.Fields.Add(&core.TextField{Name: "cover_image", Max: maxTextBlob})
	// cover_image_asset deferred (D4); estimate deferred — `estimates` is
	// outside this slice.
	c.Fields.Add(&core.NumberField{Name: "archive_in", OnlyInt: true, Min: f64(0), Max: f64(12)})
	c.Fields.Add(&core.NumberField{Name: "close_in", OnlyInt: true, Min: f64(0), Max: f64(12)})
	c.Fields.Add(&core.JSONField{Name: "logo_props"})
	// default_state -> states is a phase-2 deferred relation (§6.4), added in
	// wave 4 once `states` exists.
	c.Fields.Add(&core.DateField{Name: "archived_at"})
	c.Fields.Add(&core.TextField{
		Name: "timezone",
		Max:  255,
		Help: "IANA timezone (spec §1.3(h) REVIEW: text, not a select).",
	})
	c.Fields.Add(&core.TextField{Name: "external_source", Max: 255})
	c.Fields.Add(&core.TextField{Name: "external_id", Max: 255})

	addIdentityIndexes(c, true)
	c.Indexes = append(c.Indexes, "CREATE INDEX `idx_projects_identifier` ON `projects` (`identifier`)")
	// The two partial UNIQUE (identifier|name, workspace) indexes are phase-3.

	return c
}

// dropDefaultUsersCollection removes PocketBase's built-in `users` collection if
// it is still present, so the Plane `users` collection can take its name and id.
func dropDefaultUsersCollection(app core.App) error {
	existing, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return nil // already gone
	}
	if err := app.Delete(existing); err != nil {
		return fmt.Errorf("drop PocketBase default users collection: %w", err)
	}
	return nil
}

// restoreDefaultUsersCollection recreates PocketBase's stock `users` collection
// so that a rollback returns the database to its pre-migration state. It mirrors
// createUsersCollection in pocketbase-master/migrations/1640988000_init.go.
func restoreDefaultUsersCollection(app core.App) error {
	users := core.NewAuthCollection("users", defaultUsersCollectionID)

	ownerRule := "id = @request.auth.id"
	users.ListRule = types.Pointer(ownerRule)
	users.ViewRule = types.Pointer(ownerRule)
	users.CreateRule = types.Pointer("")
	users.UpdateRule = types.Pointer(ownerRule)
	users.DeleteRule = types.Pointer(ownerRule)

	users.Fields.Add(&core.TextField{Name: "name", Max: 255})
	users.Fields.Add(&core.FileField{
		Name:      "avatar",
		MaxSelect: 1,
		MimeTypes: []string{"image/jpeg", "image/png", "image/svg+xml", "image/gif", "image/webp"},
	})
	users.Fields.Add(&core.AutodateField{Name: "created", OnCreate: true})
	users.Fields.Add(&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true})

	users.OAuth2.MappedFields.Name = "name"
	users.OAuth2.MappedFields.AvatarURL = "avatar"

	if err := app.Save(users); err != nil {
		return fmt.Errorf("restore PocketBase default users collection: %w", err)
	}
	return nil
}

func init() {
	m.Register(func(app core.App) error {
		if err := dropDefaultUsersCollection(app); err != nil {
			return err
		}
		if err := app.Save(buildUsers()); err != nil {
			return fmt.Errorf("create users: %w", err)
		}
		usersID, err := collectionID(app, "users")
		if err != nil {
			return err
		}

		if err := app.Save(buildWorkspaces(usersID)); err != nil {
			return fmt.Errorf("create workspaces: %w", err)
		}
		workspacesID, err := collectionID(app, "workspaces")
		if err != nil {
			return err
		}

		if err := app.Save(buildWorkspaceMembers(usersID, workspacesID)); err != nil {
			return fmt.Errorf("create workspace_members: %w", err)
		}
		if err := app.Save(buildProjects(usersID, workspacesID)); err != nil {
			return fmt.Errorf("create projects: %w", err)
		}

		return nil
	}, func(app core.App) error {
		// Reverse dependency order: nothing left may reference the collection
		// being deleted (PocketBase blocks deletes with live references).
		for _, name := range []string{"projects", "workspace_members", "workspaces", "users"} {
			c, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				return fmt.Errorf("down: find %q: %w", name, err)
			}
			if err := app.Delete(c); err != nil {
				return fmt.Errorf("down: delete %q: %w", name, err)
			}
		}
		return restoreDefaultUsersCollection(app)
	}, "1759000001_init_identity_workspace_project.go")
}
