// Package instance implements Plane's license module (`/api/instances/…`):
// the public instance-info endpoint, the god-mode admin console auth
// (separate `admin-session-id` cookie), instance admins, configurations and
// workspace administration. Contract: spec/api-spec.yaml group `instance`;
// Django source: apps/api/plane/license/**.
package instance

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Handler carries the app handle for the instance domain.
type Handler struct {
	App core.App
}

// New builds the handler.
func New(app core.App) *Handler { return &Handler{App: app} }

// configValue reads a configuration value from instance_configurations,
// falling back to the provided default (license/utils/instance_value.py).
func (h *Handler) configValue(key, def string) string {
	cfg, err := h.App.FindFirstRecordByFilter("instance_configurations", "key = {:key}", dbx.Params{"key": key})
	if err != nil || cfg == nil {
		return def
	}
	v := cfg.GetString("value")
	if v == "" {
		return def
	}
	return v
}

// instanceRow returns the singleton Instance record (or nil).
func (h *Handler) instanceRow() *core.Record {
	rec, err := h.App.FindFirstRecordByFilter("instances", "id != ''")
	if err != nil {
		return nil
	}
	return rec
}

// SeedInstance creates the singleton instance row if missing. This stands in
// for Django's `configure_instance` management command, which self-hosted
// deployments run at boot; without it the admin console has nothing to walk
// through. Returns the existing or newly created record.
func (h *Handler) SeedInstance() (*core.Record, error) {
	if rec := h.instanceRow(); rec != nil {
		return rec, nil
	}
	coll, err := h.App.FindCollectionByNameOrId("instances")
	if err != nil {
		return nil, err
	}
	rec := core.NewRecord(coll)
	rec.Set("uuid", serializers.NewUUID())
	rec.Set("instance_name", "Instance")
	rec.Set("instance_id", serializers.NewUUID())
	rec.Set("current_version", "0.1.0")
	rec.Set("last_checked_at", serializers.Now())
	rec.Set("edition", "PLANE_COMMUNITY")
	rec.Set("is_telemetry_enabled", true)
	rec.Set("is_support_required", true)
	rec.Set("is_setup_done", false)
	rec.Set("is_signup_screen_visited", false)
	rec.Set("is_verified", false)
	rec.Set("is_test", false)
	rec.Set("is_current_version_deprecated", false)
	if err := h.App.Save(rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// isAdminUser reports whether the user holds an instance_admins row.
func (h *Handler) isAdminUser(userID string) bool {
	rec, err := h.App.FindFirstRecordByFilter("instance_admins", "user = {:user}", dbx.Params{"user": userID})
	return err == nil && rec != nil
}

// adminGuard resolves the caller from the admin session cookie and requires
// instance-admin membership. Unlike workspace routes, Plane is deliberately
// explicit here: 403, not 404.
func (h *Handler) adminGuard(e *core.RequestEvent) *core.Record {
	user := middleware.CurrentUser(e)
	if user == nil {
		return nil
	}
	if !h.isAdminUser(user.Id) {
		return nil
	}
	return user
}

// Info serves GET /api/instances/ (AllowAny). Without an instance row it
// reports the un-activated shape (InstanceEndpoint.get).
func (h *Handler) Info(e *core.RequestEvent) error {
	inst := h.instanceRow()
	if inst == nil {
		return e.JSON(http.StatusOK, map[string]any{"is_activated": false, "is_setup_done": false})
	}

	data := map[string]any{
		"enable_signup":                  h.configValue("ENABLE_SIGNUP", "0") == "1",
		"is_workspace_creation_disabled": h.configValue("DISABLE_WORKSPACE_CREATION", "0") == "1",
		"is_google_enabled":              h.configValue("IS_GOOGLE_ENABLED", "0") == "1",
		"is_github_enabled":              h.configValue("IS_GITHUB_ENABLED", "0") == "1",
		"is_gitlab_enabled":              h.configValue("IS_GITLAB_ENABLED", "0") == "1",
		"is_gitea_enabled":               h.configValue("IS_GITEA_ENABLED", "0") == "1",
		"is_magic_login_enabled":         h.configValue("ENABLE_MAGIC_LINK_LOGIN", "1") == "1",
		"is_email_password_enabled":      h.configValue("ENABLE_EMAIL_PASSWORD", "1") == "1",
		"github_app_name":                h.configValue("GITHUB_APP_NAME", ""),
		"slack_client_id":                h.configValue("SLACK_CLIENT_ID", ""),
		"has_unsplash_configured":        h.configValue("UNSPLASH_ACCESS_KEY", "") != "",
		"has_llm_configured":             h.configValue("LLM_API_KEY", "") != "",
		"file_size_limit":                5242880.0,
		"is_smtp_configured":             h.configValue("EMAIL_HOST", "") != "",
		"admin_base_url":                 "",
		"space_base_url":                 "",
		"app_base_url":                   "",
		"instance_changelog_url":         "",
		"is_self_managed":                true,
	}

	workspaceCount, err := h.App.CountRecords("workspaces")
	if err != nil {
		workspaceCount = 0
	}
	instanceData := serializers.Instance(inst)
	instanceData["workspaces_exist"] = workspaceCount >= 1
	instanceData["primary_owner_details"] = nil

	return e.JSON(http.StatusOK, map[string]any{"config": data, "instance": instanceData})
}

// Update serves PATCH /api/instances/ (instance admin only).
func (h *Handler) Update(e *core.RequestEvent) error {
	if h.adminGuard(e) == nil {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	inst := h.instanceRow()
	if inst == nil {
		return serializers.NotFound(e, "Instance is not registered yet")
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	for _, key := range []string{"instance_name", "whitelist_emails", "latest_version", "domain",
		"namespace", "is_telemetry_enabled", "is_support_required", "is_verified", "is_test",
		"is_current_version_deprecated"} {
		if v, ok := body[key]; ok {
			inst.Set(key, v)
		}
	}
	if err := h.App.Save(inst); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.Instance(inst))
}

// SignUpScreenVisited serves POST /api/instances/admins/sign-up-screen-visited/.
func (h *Handler) SignUpScreenVisited(e *core.RequestEvent) error {
	inst := h.instanceRow()
	if inst == nil {
		return serializers.BadRequest(e, "Instance is not configured")
	}
	inst.Set("is_signup_screen_visited", true)
	if err := h.App.Save(inst); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.NoContent(http.StatusNoContent)
}

// AdminList serves GET /api/instances/admins/.
func (h *Handler) AdminList(e *core.RequestEvent) error {
	if h.adminGuard(e) == nil {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	if h.instanceRow() == nil {
		return serializers.Forbidden(e, "Instance is not registered yet")
	}
	recs, err := h.App.FindRecordsByFilter("instance_admins", "id != ''", "-created_at", 0, 0)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		out = append(out, serializers.InstanceAdmin(h.App, r))
	}
	return e.JSON(http.StatusOK, out)
}

// AdminCreate serves POST /api/instances/admins/ — promote an existing user.
func (h *Handler) AdminCreate(e *core.RequestEvent) error {
	if h.adminGuard(e) == nil {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	email, _ := body["email"].(string)
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return serializers.BadRequest(e, "Email is required")
	}
	inst := h.instanceRow()
	if inst == nil {
		return serializers.Forbidden(e, "Instance is not registered yet")
	}
	user, err := h.App.FindFirstRecordByFilter("users", "email = {:email}", dbx.Params{"email": email})
	if err != nil || user == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	role := 20
	if r, ok := body["role"].(float64); ok && r != 0 {
		role = int(r)
	}
	coll, err := h.App.FindCollectionByNameOrId("instance_admins")
	if err != nil {
		return err
	}
	rec := core.NewRecord(coll)
	rec.Set("uuid", serializers.NewUUID())
	rec.Set("instance", inst.Id)
	rec.Set("user", user.Id)
	rec.Set("role", role)
	rec.Set("is_verified", false)
	if err := h.App.Save(rec); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusCreated, serializers.InstanceAdmin(h.App, rec))
}

// AdminDelete serves DELETE /api/instances/admins/{pk}/.
func (h *Handler) AdminDelete(e *core.RequestEvent) error {
	if h.adminGuard(e) == nil {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	rec, _ := serializers.FindByUUIDOrID(h.App, "instance_admins", e.Request.PathValue("pk"))
	if rec != nil {
		if err := h.App.Delete(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
	}
	return e.NoContent(http.StatusNoContent)
}

// Me serves GET /api/instances/admins/me/.
func (h *Handler) Me(e *core.RequestEvent) error {
	user := h.adminGuard(e)
	if user == nil {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	return e.JSON(http.StatusOK, serializers.InstanceAdminMe(user))
}

// Session serves GET /api/instances/admins/session/ (AllowAny).
func (h *Handler) Session(e *core.RequestEvent) error {
	user := h.adminGuard(e)
	if user == nil {
		return e.JSON(http.StatusOK, map[string]any{"is_authenticated": false})
	}
	return e.JSON(http.StatusOK, map[string]any{
		"is_authenticated": true,
		"user":             serializers.InstanceAdminMe(user),
	})
}

// SignOut serves POST /api/instances/admins/sign-out/.
func (h *Handler) SignOut(e *core.RequestEvent) error {
	middleware.ClearSessionCookie(e, middleware.AdminSessionCookieName)
	return e.Redirect(http.StatusFound, "/")
}

// Configurations serves GET /api/instances/configurations/.
func (h *Handler) Configurations(e *core.RequestEvent) error {
	if h.adminGuard(e) == nil {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	recs, err := h.App.FindRecordsByFilter("instance_configurations", "id != ''", "key", 0, 0)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		out = append(out, serializers.Configuration(r))
	}
	return e.JSON(http.StatusOK, out)
}

// ConfigurationsUpdate serves PATCH /api/instances/configurations/ — bulk
// update of values by key.
func (h *Handler) ConfigurationsUpdate(e *core.RequestEvent) error {
	if h.adminGuard(e) == nil {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	updated := make([]map[string]any, 0, len(body))
	for key, raw := range body {
		cfg, err := h.App.FindFirstRecordByFilter("instance_configurations", "key = {:key}", dbx.Params{"key": key})
		if err != nil || cfg == nil {
			continue
		}
		value := ""
		switch v := raw.(type) {
		case string:
			value = strings.TrimSpace(v)
		case nil:
			value = ""
		default:
			value = strings.TrimSpace(fmt.Sprintf("%v", v))
		}
		cfg.Set("value", value)
		if err := h.App.Save(cfg); err != nil {
			continue
		}
		updated = append(updated, serializers.Configuration(cfg))
	}
	return e.JSON(http.StatusOK, updated)
}

// DisableEmailFeature serves DELETE /api/instances/configurations/disable-email-feature/.
func (h *Handler) DisableEmailFeature(e *core.RequestEvent) error {
	if h.adminGuard(e) == nil {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	keys := []string{"EMAIL_HOST", "EMAIL_HOST_USER", "EMAIL_HOST_PASSWORD", "ENABLE_SMTP", "EMAIL_PORT", "EMAIL_FROM"}
	for _, key := range keys {
		cfg, err := h.App.FindFirstRecordByFilter("instance_configurations", "key = {:key}", dbx.Params{"key": key})
		if err != nil || cfg == nil {
			continue
		}
		if key == "ENABLE_SMTP" {
			cfg.Set("value", "0")
		} else {
			cfg.Set("value", "")
		}
		_ = h.App.Save(cfg)
	}
	return e.JSON(http.StatusOK, map[string]any{})
}

// EmailCredentialsCheck serves POST /api/instances/email-credentials-check/.
// Without an SMTP server configured this always fails the check, matching a
// self-hosted instance with EMAIL_HOST unset.
func (h *Handler) EmailCredentialsCheck(e *core.RequestEvent) error {
	if h.adminGuard(e) == nil {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	receiver, _ := body["receiver_email"].(string)
	if receiver == "" {
		return serializers.BadRequest(e, "Receiver email is required")
	}
	return serializers.BadRequest(e, "SMTP is not configured")
}

// WorkspaceSlugCheck serves GET /api/instances/workspace-slug-check/?slug=.
func (h *Handler) WorkspaceSlugCheck(e *core.RequestEvent) error {
	if h.adminGuard(e) == nil {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	slug := e.Request.URL.Query().Get("slug")
	if slug == "" {
		return serializers.BadRequest(e, "Workspace Slug is required")
	}
	existing, err := h.App.FindFirstRecordByFilter("workspaces", "slug = {:slug}", dbx.Params{"slug": strings.ToLower(slug)})
	taken := err == nil && existing != nil
	return e.JSON(http.StatusOK, map[string]any{"status": !taken})
}

// WorkspaceList serves GET /api/instances/workspaces/ (admin, paginated).
func (h *Handler) WorkspaceList(e *core.RequestEvent) error {
	if h.adminGuard(e) == nil {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	perPage, cursor := serializers.ListParams(e, 10)
	search := e.Request.URL.Query().Get("search")
	filter := "id != ''"
	params := dbx.Params{}
	if search != "" {
		filter += " && name ~ {:search}"
		params["search"] = search
	}
	env, err := serializers.Paginate(h.App, serializers.ListQuery{
		Collection: "workspaces",
		Filter:     filter,
		Params:     params,
		Sort:       "-created_at",
		PerPage:    perPage,
		Cursor:     cursor,
	}, func(r *core.Record) any {
		out := serializers.WorkspaceLite(r)
		out["total_projects"] = h.countIn("projects", "workspace", r.Id)
		out["total_members"] = h.countIn("workspace_members", "workspace", r.Id)
		return out
	})
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, env)
}

func (h *Handler) countIn(collection, field, id string) int {
	n, err := h.App.CountRecords(collection, dbx.HashExp{field: id})
	if err != nil {
		return 0
	}
	return int(n)
}
