// Wave W2 (remainder) + W3: identity satellites, API tokens and assets.
//
// Spec §6.3:
//   W2 remainder — `api_activity_logs`. (`workspaces` is already created; `sessions`
//     is intentionally NOT created — PocketBase's own JWT auth supersedes it.)
//   W3 — `file_assets`, `profiles`, `accounts`, `api_tokens`.
//
// Deferred relations (§6.4) are appended in the W11 back-fill file:
//   D5 `file_assets.draft_issue`, D6 `.project`, D7 `.issue`, D8 `.comment`, D9 `.page`.
// D1/D2 (`users.avatar_asset`, `users.cover_image_asset`) and D3
// (`workspaces.logo_asset`) are also appended there, once `file_assets` exists.
package pb_migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// uuidRef returns a plain UUID-shaped text column for Plane's polymorphic
// `entity_identifier` / snapshot UUID columns (spec §2.5). Unlike uuidField()
// it is NOT the identity field and carries no help text about the API `id`.
func uuidRef(name string) *core.TextField {
	return &core.TextField{Name: name, Max: 36, Pattern: uuidPattern}
}

// ---------------------------------------------------------------------------
// api_activity_logs — Django `APIActivityLog` (spec §3.1). A-block.
// ---------------------------------------------------------------------------

func buildAPIActivityLogs(usersID string) *core.Collection {
	c := core.NewBaseCollection("api_activity_logs")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "token_identifier", Max: 255})
	c.Fields.Add(&core.TextField{Name: "path", Max: 255})
	c.Fields.Add(&core.TextField{Name: "method", Max: 10})
	c.Fields.Add(&core.TextField{Name: "query_params", Max: maxTextBlob})
	c.Fields.Add(&core.TextField{Name: "headers", Max: maxTextBlob})
	c.Fields.Add(&core.TextField{Name: "body", Max: maxTextBlob})
	c.Fields.Add(&core.NumberField{Name: "response_code", OnlyInt: true, Min: f64(0)})
	c.Fields.Add(&core.TextField{Name: "response_body", Max: maxTextBlob})
	// GenericIPAddressField -> text max 45 (spec §1.2); no exact IP pattern is
	// given by the spec, so none is invented here.
	c.Fields.Add(&core.TextField{Name: "ip_address", Max: 45})
	c.Fields.Add(&core.TextField{Name: "user_agent", Max: 512})

	addIdentityIndexes(c, true)

	return c
}

// ---------------------------------------------------------------------------
// file_assets — Django `FileAsset` (spec §3.4). A-block.
//
// The most circular collection in the schema; `draft_issue`, `project`, `issue`,
// `comment` and `page` are deferred to W11 (§6.4 D5–D9).
// ---------------------------------------------------------------------------

func buildFileAssets(usersID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("file_assets")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.JSONField{Name: "attributes"})
	c.Fields.Add(&core.FileField{Name: "asset", MaxSelect: 1, Required: true, Protected: true, MaxSize: maxEditor})
	c.Fields.Add(relation("user", usersID, false, true))           // CASCADE, nullable
	c.Fields.Add(relation("workspace", workspacesID, false, true)) // CASCADE, nullable
	// draft_issue/project/issue/comment/page deferred (§6.4 D5–D9).
	c.Fields.Add(&core.TextField{Name: "entity_type", Max: 255})
	c.Fields.Add(&core.TextField{Name: "entity_identifier", Max: 255})
	c.Fields.Add(&core.BoolField{Name: "is_deleted"})
	c.Fields.Add(&core.BoolField{Name: "is_archived"})
	c.Fields.Add(&core.TextField{Name: "external_id", Max: 255})
	c.Fields.Add(&core.TextField{Name: "external_source", Max: 255})
	c.Fields.Add(&core.NumberField{Name: "size"})
	c.Fields.Add(&core.BoolField{Name: "is_uploaded"})
	c.Fields.Add(&core.JSONField{Name: "storage_metadata"})

	addIdentityIndexes(c, true)
	c.Indexes = append(c.Indexes,
		"CREATE INDEX `asset_entity_type_idx` ON `file_assets` (`entity_type`)",
		"CREATE INDEX `asset_entity_identifier_idx` ON `file_assets` (`entity_identifier`)",
		"CREATE INDEX `asset_entity_idx` ON `file_assets` (`entity_type`, `entity_identifier`)",
		"CREATE INDEX `asset_asset_idx` ON `file_assets` (`asset`)",
	)

	return c
}

// ---------------------------------------------------------------------------
// profiles — Django `Profile` (spec §3.1). `TimeAuditModel` only: no
// created_by/updated_by and NO `deleted_at`.
// ---------------------------------------------------------------------------

func buildProfiles(usersID string) *core.Collection {
	c := core.NewBaseCollection("profiles")

	c.Fields.Add(uuidField())
	c.Fields.Add(&core.AutodateField{Name: "created_at", OnCreate: true})
	c.Fields.Add(&core.AutodateField{Name: "updated_at", OnUpdate: true})

	c.Fields.Add(relation("user", usersID, true, true)) // CASCADE + unique index
	c.Fields.Add(&core.JSONField{Name: "theme"})
	c.Fields.Add(&core.BoolField{Name: "is_app_rail_docked"})
	c.Fields.Add(&core.BoolField{Name: "is_tour_completed"})
	c.Fields.Add(&core.JSONField{Name: "onboarding_step"})
	c.Fields.Add(&core.TextField{Name: "use_case", Max: maxTextBlob})
	c.Fields.Add(&core.TextField{Name: "role", Max: 300})
	c.Fields.Add(&core.BoolField{Name: "is_onboarded"})
	c.Fields.Add(uuidRef("last_workspace_id"))
	c.Fields.Add(&core.TextField{Name: "billing_address_country", Max: 255})
	c.Fields.Add(&core.JSONField{Name: "billing_address"})
	c.Fields.Add(&core.BoolField{Name: "has_billing_address"})
	c.Fields.Add(&core.TextField{Name: "company_name", Max: 255})
	c.Fields.Add(&core.SelectField{Name: "notification_view_mode", Values: []string{"full", "compact"}, MaxSelect: 1})
	c.Fields.Add(&core.BoolField{Name: "is_smooth_cursor_enabled"})
	c.Fields.Add(&core.BoolField{Name: "is_mobile_onboarded"})
	c.Fields.Add(&core.JSONField{Name: "mobile_onboarding_step"})
	c.Fields.Add(&core.BoolField{Name: "mobile_timezone_auto_set"})
	c.Fields.Add(&core.TextField{Name: "language", Max: 255})
	c.Fields.Add(&core.NumberField{Name: "start_of_the_week", OnlyInt: true, Min: f64(0), Max: f64(6)})
	c.Fields.Add(&core.JSONField{Name: "goals"})
	c.Fields.Add(&core.TextField{Name: "background_color", Max: 255})
	c.Fields.Add(&core.BoolField{Name: "is_navigation_tour_completed"})
	c.Fields.Add(&core.BoolField{Name: "has_marketing_email_consent"})
	c.Fields.Add(&core.BoolField{Name: "is_subscribed_to_changelog"})
	c.Fields.Add(&core.JSONField{Name: "product_tour"})

	addIdentityIndexes(c, false)
	c.Indexes = append(c.Indexes, "CREATE UNIQUE INDEX `idx_profiles_user` ON `profiles` (`user`)")

	return c
}

// ---------------------------------------------------------------------------
// accounts — Django `Account` (spec §3.1). `TimeAuditModel` only.
// ---------------------------------------------------------------------------

func buildAccounts(usersID string) *core.Collection {
	c := core.NewBaseCollection("accounts")

	c.Fields.Add(uuidField())
	c.Fields.Add(&core.AutodateField{Name: "created_at", OnCreate: true})
	c.Fields.Add(&core.AutodateField{Name: "updated_at", OnUpdate: true})

	c.Fields.Add(relation("user", usersID, true, true)) // CASCADE
	c.Fields.Add(&core.TextField{Name: "provider_account_id", Max: 255})
	c.Fields.Add(&core.SelectField{Name: "provider", Values: []string{"google", "github", "gitlab"}, MaxSelect: 1})
	c.Fields.Add(&core.TextField{Name: "access_token", Max: maxTextBlob})
	c.Fields.Add(&core.DateField{Name: "access_token_expired_at"})
	c.Fields.Add(&core.TextField{Name: "refresh_token", Max: maxTextBlob})
	c.Fields.Add(&core.DateField{Name: "refresh_token_expired_at"})
	c.Fields.Add(&core.DateField{Name: "last_connected_at", Required: true})
	c.Fields.Add(&core.TextField{Name: "id_token", Max: maxTextBlob})
	c.Fields.Add(&core.JSONField{Name: "metadata"})

	addIdentityIndexes(c, false)
	c.Indexes = append(c.Indexes,
		"CREATE UNIQUE INDEX `idx_accounts_provider_account` ON `accounts` (`provider`, `provider_account_id`)",
	)

	return c
}

// ---------------------------------------------------------------------------
// api_tokens — Django `APIToken` (spec §3.1). A-block.
// ---------------------------------------------------------------------------

func buildAPITokens(usersID, workspacesID string) *core.Collection {
	c := core.NewBaseCollection("api_tokens")
	addAuditFields(c, usersID, true)

	c.Fields.Add(&core.TextField{Name: "label", Max: 255})
	c.Fields.Add(&core.TextField{Name: "description", Max: maxTextBlob})
	c.Fields.Add(&core.BoolField{Name: "is_active"})
	c.Fields.Add(&core.DateField{Name: "last_used"})
	c.Fields.Add(&core.TextField{Name: "token", Required: true, Max: 255})
	c.Fields.Add(relation("user", usersID, true, true)) // CASCADE
	c.Fields.Add(&core.NumberField{
		Name:    "user_type",
		OnlyInt: true,
		Min:     f64(0),
		Max:     f64(1),
		Help:    "0=Human, 1=Bot (spec §1.3(a)).",
	})
	c.Fields.Add(relation("workspace", workspacesID, false, true)) // CASCADE, nullable
	c.Fields.Add(&core.DateField{Name: "expired_at"})
	c.Fields.Add(&core.BoolField{Name: "is_service"})
	c.Fields.Add(&core.TextField{Name: "allowed_rate_limit", Max: 255})

	addIdentityIndexes(c, true)
	c.Indexes = append(c.Indexes, "CREATE UNIQUE INDEX `idx_api_tokens_token` ON `api_tokens` (`token`)")

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

		if err := app.Save(buildAPIActivityLogs(usersID)); err != nil {
			return fmt.Errorf("create api_activity_logs: %w", err)
		}
		if err := app.Save(buildFileAssets(usersID, workspacesID)); err != nil {
			return fmt.Errorf("create file_assets: %w", err)
		}
		if err := app.Save(buildProfiles(usersID)); err != nil {
			return fmt.Errorf("create profiles: %w", err)
		}
		if err := app.Save(buildAccounts(usersID)); err != nil {
			return fmt.Errorf("create accounts: %w", err)
		}
		if err := app.Save(buildAPITokens(usersID, workspacesID)); err != nil {
			return fmt.Errorf("create api_tokens: %w", err)
		}

		return nil
	}, func(app core.App) error {
		for _, name := range []string{"api_tokens", "accounts", "profiles", "file_assets", "api_activity_logs"} {
			c, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				return fmt.Errorf("down: find %q: %w", name, err)
			}
			if err := app.Delete(c); err != nil {
				return fmt.Errorf("down: delete %q: %w", name, err)
			}
		}
		return nil
	}, "1759000005_init_identity_tokens_assets.go")
}
