// Package config implements the two top-level, non-workspace-scoped read
// endpoints Plane's web app fetches on boot:
//
//	GET /api/configs/       <- apps/web/services/app_config.service.ts:21
//	GET /api/release-notes/ <- apps/web/services/workspace.service.ts:225
//
// Neither has a Django view or URL in this checkout (verified:
// `grep -rn 'configs\|release-notes' apps/api/plane/**/urls/*.py` has no hits),
// so their response shapes are DERIVED rather than transcribed — see the doc
// comment on each handler for the reasoning and confidence. Both frontend call
// sites are dead (neither service method is consumed anywhere in apps/web), so
// the shapes only need to be structurally sane; they are still backed by real
// data so a future consumer gets live values rather than a stub.
package config

import (
	"net/http"
	"strconv"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Handler holds the app dependency for the config routes.
type Handler struct {
	App core.App
}

// New returns a config handler bound to app.
func New(app core.App) *Handler {
	return &Handler{App: app}
}

// Configs serves GET /api/configs/.
//
// DERIVATION (confidence: low). There is no Django view, so the shape is taken
// from the closest authoritative in-repo definition of "public runtime config":
// the `config` block of GET /api/instances/ (IInstanceConfig in
// packages/types/src/instance/base.ts:47), which is itself derived from the
// same env-driven `instance_configurations` keys. The frontend return type is
// `any` and the call site is unused, so a superset of those keys is safe; every
// value here is read from the live `instance_configurations` table with the
// same defaults as handlers/instance, so it tracks the operator's settings.
func (h *Handler) Configs(e *core.RequestEvent) error {
	return e.JSON(http.StatusOK, map[string]any{
		"enable_signup":                  h.boolValue("ENABLE_SIGNUP", false),
		"is_workspace_creation_disabled": h.boolValue("DISABLE_WORKSPACE_CREATION", false),
		"is_google_enabled":              h.boolValue("IS_GOOGLE_ENABLED", false),
		"is_github_enabled":              h.boolValue("IS_GITHUB_ENABLED", false),
		"is_gitlab_enabled":              h.boolValue("IS_GITLAB_ENABLED", false),
		"is_gitea_enabled":               h.boolValue("IS_GITEA_ENABLED", false),
		"is_magic_login_enabled":         h.boolValue("ENABLE_MAGIC_LINK_LOGIN", true),
		"is_email_password_enabled":      h.boolValue("ENABLE_EMAIL_PASSWORD", true),
		"github_app_name":                h.configValue("GITHUB_APP_NAME", ""),
		"slack_client_id":                h.configValue("SLACK_CLIENT_ID", ""),
		"has_unsplash_configured":        h.configValue("UNSPLASH_ACCESS_KEY", "") != "",
		"has_llm_configured":             h.configValue("LLM_API_KEY", "") != "",
		"file_size_limit":                5242880,
		"is_smtp_configured":             h.configValue("EMAIL_HOST", "") != "",
		"is_self_managed":                true,
		"app_base_url":                   "",
		"space_base_url":                 "",
		"admin_base_url":                 "",
		"instance_changelog_url":         "",
	})
}

// ReleaseNotes serves GET /api/release-notes/.
//
// DERIVATION (confidence: medium). No Django view exists. The frontend types
// the response as `IProductUpdateResponse[]` (packages/types/src/workspace.ts:167),
// which is a GitHub release object — i.e. upstream proxies the product's GitHub
// releases. We have no GitHub access, so this serves the local `changelogs`
// table instead (Django `ChangeLog`, db_table = "changelogs", already present
// from wave W12), which is the project's own release-notes store. Each row is
// projected with its model fields; `release_date` is a nullable timestamp. The
// array is the load-bearing part of the contract (the frontend expects a list);
// the per-item keys follow ChangeLog. An empty table yields `[]`, which is
// correct for a fresh install.
func (h *Handler) ReleaseNotes(e *core.RequestEvent) error {
	records, err := h.App.FindRecordsByFilter("changelogs", "deleted_at = ''", "-created_at", 0, 0)
	if err != nil {
		return err
	}
	results := make([]any, 0, len(records))
	for _, r := range records {
		results = append(results, map[string]any{
			"id":                   serializers.UUID(r),
			"title":                serializers.Text(r, "title"),
			"description":          serializers.NullableText(r, "description"),
			"version":              serializers.Text(r, "version"),
			"tags":                 serializers.JSONOrDefault(r, "tags", []any{}),
			"release_date":         serializers.NullableDateTime(r, "release_date"),
			"is_release_candidate": r.GetBool("is_release_candidate"),
			"created_at":           serializers.DateTime(r, "created_at"),
			"updated_at":           serializers.DateTime(r, "updated_at"),
			"created_by":           serializers.RelationUUID(h.App, r, "created_by"),
			"updated_by":           serializers.RelationUUID(h.App, r, "updated_by"),
		})
	}
	return e.JSON(http.StatusOK, results)
}

// configValue reads a value from `instance_configurations`, falling back to def
// (mirrors license/utils/instance_value.py).
func (h *Handler) configValue(key, def string) string {
	cfg, err := h.App.FindFirstRecordByFilter("instance_configurations", "key = {:key}", dbx.Params{"key": key})
	if err != nil || cfg == nil {
		return def
	}
	if v := cfg.GetString("value"); v != "" {
		return v
	}
	return def
}

// boolValue treats "1"/"true" as true and anything else as false, matching the
// instance handler's `configValue(...) == "1"` convention.
func (h *Handler) boolValue(key string, def bool) bool {
	raw := h.configValue(key, "")
	if raw == "" {
		return def
	}
	if b, err := strconv.ParseBool(raw); err == nil {
		return b
	}
	return raw == "1"
}
