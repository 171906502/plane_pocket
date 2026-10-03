package instance

import (
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/profiles"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// adminBaseHost mirrors plane/authentication/utils/host.py `base_host(is_admin=True)`:
// the admin origin (ADMIN_BASE_URL, else WEB_URL/APP_BASE_URL) joined with
// ADMIN_BASE_PATH, which defaults to "/god-mode/".
//
// Plane's admin endpoints answer with relative redirects because Django serves
// the API on the same origin as the console. Here the API runs on its own port,
// so the redirect has to be resolved against the admin origin explicitly —
// otherwise the browser lands on the API host and the console never loads.
func adminBaseHost() string {
	base := ""
	for _, key := range []string{"PLANE_ADMIN_BASE_URL", "ADMIN_BASE_URL", "ADMIN_WEB_URL"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			base = v
			break
		}
	}
	if base == "" {
		for _, key := range []string{"PLANE_APP_BASE_URL", "WEB_URL", "APP_BASE_URL"} {
			if v := strings.TrimSpace(os.Getenv(key)); v != "" {
				base = v
				break
			}
		}
	}

	path := strings.TrimSpace(os.Getenv("ADMIN_BASE_PATH"))
	if path == "" {
		path = "/god-mode/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}

	if base == "" {
		return path
	}
	return strings.TrimRight(base, "/") + path
}

// adminRedirect resolves target against the admin console base and issues the 302.
func adminRedirect(e *core.RequestEvent, target string) error {
	base := adminBaseHost()
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	u, err := url.Parse(base)
	if err != nil {
		return e.Redirect(http.StatusFound, base+target)
	}
	rel, err := url.Parse(target)
	if err != nil {
		return e.Redirect(http.StatusFound, base+target)
	}
	return e.Redirect(http.StatusFound, u.ResolveReference(rel).String())
}

// redirectError mirrors the Django admin auth views' failure contract: a 302
// back to the admin console carrying error_code / error_message query params
// (plane/authentication/adapter/error.py AUTHENTICATION_ERROR_CODES shape).
func redirectError(e *core.RequestEvent, code string) error {
	q := url.Values{}
	q.Set("error_code", code)
	q.Set("error_message", code)
	return adminRedirect(e, "?"+q.Encode())
}

// SignUp serves POST /api/instances/admins/sign-up/ — a native HTML form POST
// (InstanceAdminSignUpEndpoint). Only the very first admin can be created;
// afterwards the endpoint always redirects with ADMIN_ALREADY_EXIST.
func (h *Handler) SignUp(e *core.RequestEvent) error {
	inst := h.instanceRow()
	if inst == nil {
		return redirectError(e, "INSTANCE_NOT_CONFIGURED")
	}
	anyAdmin, _ := h.App.FindFirstRecordByFilter("instance_admins", "id != ''")
	if inst.GetBool("is_setup_done") || anyAdmin != nil {
		return redirectError(e, "ADMIN_ALREADY_EXIST")
	}

	email := strings.TrimSpace(strings.ToLower(e.Request.FormValue("email")))
	password := e.Request.FormValue("password")
	firstName := e.Request.FormValue("first_name")
	lastName := e.Request.FormValue("last_name")
	companyName := e.Request.FormValue("company_name")

	if email == "" || password == "" || firstName == "" {
		return redirectError(e, "REQUIRED_ADMIN_EMAIL_PASSWORD_FIRST_NAME")
	}
	if !strings.Contains(email, "@") || !strings.Contains(strings.SplitN(email, "@", 2)[1], ".") {
		return redirectError(e, "INVALID_ADMIN_EMAIL")
	}
	if existing, _ := h.App.FindFirstRecordByFilter("users", "email = {:email}", dbx.Params{"email": email}); existing != nil {
		return redirectError(e, "ADMIN_USER_ALREADY_EXIST")
	}
	// Stand-in for the zxcvbn score<3 gate (plane.utils zxcvbn): a length and
	// character-class check that rejects trivially weak passwords.
	if len(password) < 8 || password == strings.ToLower(password) || !strings.ContainsAny(password, "0123456789") {
		return redirectError(e, "PASSWORD_TOO_WEAK")
	}

	usersColl, err := h.App.FindCollectionByNameOrId("users")
	if err != nil {
		return err
	}
	user := core.NewRecord(usersColl)
	user.Set("uuid", serializers.NewUUID())
	user.Set("email", email)
	user.Set("password", password) // auth collection hashes on save
	user.Set("first_name", firstName)
	user.Set("last_name", lastName)
	user.Set("display_name", firstName)
	user.Set("username", serializers.NewUUID())
	user.Set("is_active", true)
	user.Set("is_password_autoset", false)
	user.Set("last_active", serializers.Now())
	// Django's User defaults: `user_timezone="UTC"`, `last_login_medium="email"`.
	user.Set("user_timezone", "UTC")
	user.Set("last_login_medium", "email")
	if err := h.App.Save(user); err != nil {
		e.App.Logger().Error("admin sign-up: user save failed", "error", err.Error())
		return redirectError(e, "REQUIRED_ADMIN_EMAIL_PASSWORD_FIRST_NAME")
	}

	// Django creates the profile in the same transaction:
	// `Profile.objects.create(user=user, company_name=company_name)`.
	if profile, err := profiles.Ensure(h.App, user); err != nil {
		e.App.Logger().Error("admin sign-up: profile create failed", "error", err.Error())
	} else if profile != nil && companyName != "" {
		profile.Set("company_name", companyName)
		if err := h.App.Save(profile); err != nil {
			e.App.Logger().Error("admin sign-up: profile company_name failed", "error", err.Error())
		}
	}

	adminColl, err := h.App.FindCollectionByNameOrId("instance_admins")
	if err != nil {
		return err
	}
	admin := core.NewRecord(adminColl)
	admin.Set("uuid", serializers.NewUUID())
	admin.Set("instance", inst.Id)
	admin.Set("user", user.Id)
	admin.Set("role", 20)
	admin.Set("is_verified", false)
	if err := h.App.Save(admin); err != nil {
		return err
	}

	inst.Set("is_setup_done", true)
	if companyName != "" {
		inst.Set("instance_name", companyName)
	}
	if v := e.Request.FormValue("is_telemetry_enabled"); v == "false" || v == "False" {
		inst.Set("is_telemetry_enabled", false)
	}
	if err := h.App.Save(inst); err != nil {
		return err
	}

	_ = middleware.SetSessionCookie(e, h.App, user, middleware.AdminSessionCookieName)
	return adminRedirect(e, "general/")
}

// SignIn serves POST /api/instances/admins/sign-in/ — native form POST
// (InstanceAdminSignInEndpoint). Only instance admins may sign in.
func (h *Handler) SignIn(e *core.RequestEvent) error {
	inst := h.instanceRow()
	if inst == nil {
		return redirectError(e, "INSTANCE_NOT_CONFIGURED")
	}
	email := strings.TrimSpace(strings.ToLower(e.Request.FormValue("email")))
	password := e.Request.FormValue("password")

	user, _ := h.App.FindFirstRecordByFilter("users", "email = {:email}", dbx.Params{"email": email})
	if user == nil || !user.ValidatePassword(password) {
		return redirectError(e, "INVALID_EMAIL_OR_PASSWORD")
	}
	if !user.GetBool("is_active") {
		return redirectError(e, "USER_ACCOUNT_DEACTIVATED")
	}
	if !h.isAdminUser(user.Id) {
		return redirectError(e, "ADMIN_USER_DOES_NOT_EXIST")
	}

	_ = middleware.SetSessionCookie(e, h.App, user, middleware.AdminSessionCookieName)
	return adminRedirect(e, "general/")
}
