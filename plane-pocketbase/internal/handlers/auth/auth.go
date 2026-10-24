// Package auth implements Plane's /auth/* endpoints.
//
// These endpoints do not speak JSON like the rest of the API: sign-in, sign-up,
// sign-out and magic-sign-in are native HTML form POSTs that answer with a 302
// redirect (Plane's frontends submit a real <form> and let the browser follow
// the redirect). Failures redirect back with `?error_code=` / `?error_message=`
// query parameters, mirroring plane/authentication/adapter/error.py.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Plane's AUTHENTICATION_ERROR_CODES, limited to the codes this slice can emit.
// Source: plane/authentication/adapter/error.py.
const (
	codeInstanceNotConfigured = 5000
	codeInvalidEmail          = 5005
	codeEmailRequired         = 5010
	codeSignupDisabled        = 5015
	codeSMTPNotConfigured     = 5025

	codeUserAlreadyExists   = 5030
	codeRequiredSignUp      = 5040
	codeInvalidEmailSignUp  = 5045
	codeUserDoesNotExist    = 5060
	codeAuthFailedSignIn    = 5065
	codeRequiredSignIn      = 5070
	codeInvalidEmailSignIn  = 5075
	codeMagicRequiredSignIn = 5085
	codeMagicRequiredSignUp = 5055
	codeInvalidMagicSignIn  = 5090
	codeInvalidMagicSignUp  = 5092
	codeExpiredMagicSignIn  = 5095
	codeExpiredMagicSignUp  = 5097

	codePasswordAlreadySet = 5145
	codeInvalidPassword    = 5020
)

// magicCodeTTL matches Plane's Redis magic-code expiry (600s).
const magicCodeTTL = 600 * time.Second

// Handler holds the app dependency for the /auth/* routes.
type Handler struct {
	App core.App
}

// New returns an auth handler bound to app.
func New(app core.App) *Handler {
	return &Handler{App: app}
}

// CSRFToken serves GET /auth/get-csrf-token/. It mirrors Plane's
// CSRFTokenEndpoint: return the token in the body and store the same value in
// the `csrftoken` cookie so the double-submit check can validate it later.
func (h *Handler) CSRFToken(e *core.RequestEvent) error {
	token := middleware.NewCSRFToken()
	middleware.SetCSRFCookie(e, token)
	return e.JSON(http.StatusOK, map[string]any{"csrf_token": token})
}

// EmailCheck serves POST /auth/email-check/ and mirrors Plane's
// EmailCheckEndpoint. Without SMTP configured the status is always "CREDENTIAL",
// which is exactly what Plane returns when EMAIL_HOST is unset.
func (h *Handler) EmailCheck(e *core.RequestEvent) error {
	var body struct {
		Email string `json:"email"`
	}
	_ = e.BindBody(&body)
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if email == "" {
		return e.JSON(http.StatusBadRequest, errorDict(codeEmailRequired, "EMAIL_REQUIRED", nil))
	}
	if !validEmail(email) {
		return e.JSON(http.StatusBadRequest, errorDict(codeInvalidEmail, "INVALID_EMAIL", nil))
	}
	existing := false
	if user, _ := h.findUserByEmail(email); user != nil {
		existing = true
	}
	return e.JSON(http.StatusOK, map[string]any{
		"existing": existing,
		"status":   "CREDENTIAL",
	})
}

// SignIn serves POST /auth/sign-in/ (native form POST → 302).
func (h *Handler) SignIn(e *core.RequestEvent) error {
	nextPath := e.Request.FormValue("next_path")
	email := strings.ToLower(strings.TrimSpace(e.Request.FormValue("email")))
	password := e.Request.FormValue("password")

	if email == "" || password == "" {
		return h.redirect(e, nextPath, errorDict(codeRequiredSignIn, "REQUIRED_EMAIL_PASSWORD_SIGN_IN", map[string]any{"email": email}))
	}
	if !validEmail(email) {
		return h.redirect(e, nextPath, errorDict(codeInvalidEmailSignIn, "INVALID_EMAIL_SIGN_IN", map[string]any{"email": email}))
	}

	user, _ := h.findUserByEmail(email)
	if user == nil {
		return h.redirect(e, nextPath, errorDict(codeUserDoesNotExist, "USER_DOES_NOT_EXIST", map[string]any{"email": email}))
	}
	if !user.ValidatePassword(password) {
		return h.redirect(e, nextPath, errorDict(codeAuthFailedSignIn, "AUTHENTICATION_FAILED_SIGN_IN", map[string]any{"email": email}))
	}

	h.touchLogin(user)
	if err := middleware.SetSessionCookie(e, h.App, user, middleware.SessionCookieName); err != nil {
		return err
	}
	return h.redirect(e, h.redirectionPath(user, nextPath), nil)
}

// SignUp serves POST /auth/sign-up/ (native form POST → 302). It mirrors
// Adapter.complete_login_or_signup: username is a random hex string, the display
// name defaults to the email local part, and the password is set directly.
func (h *Handler) SignUp(e *core.RequestEvent) error {
	nextPath := e.Request.FormValue("next_path")
	email := strings.ToLower(strings.TrimSpace(e.Request.FormValue("email")))
	password := e.Request.FormValue("password")

	if email == "" || password == "" {
		return h.redirect(e, nextPath, errorDict(codeRequiredSignUp, "REQUIRED_EMAIL_PASSWORD_SIGN_UP", map[string]any{"email": email}))
	}
	if !validEmail(email) {
		return h.redirect(e, nextPath, errorDict(codeInvalidEmailSignUp, "INVALID_EMAIL_SIGN_UP", map[string]any{"email": email}))
	}
	if existing, _ := h.findUserByEmail(email); existing != nil {
		return h.redirect(e, nextPath, errorDict(codeUserAlreadyExists, "USER_ALREADY_EXIST", map[string]any{"email": email}))
	}

	collection, err := h.App.FindCollectionByNameOrId("users")
	if err != nil {
		return err
	}
	user := core.NewRecord(collection)
	user.Set("uuid", serializers.NewUUID())
	user.Set("username", randomHex(16))
	user.Set("email", email)
	user.Set("display_name", displayNameFor(email))
	user.Set("first_name", "")
	user.Set("last_name", "")
	user.Set("is_active", true)
	user.Set("is_password_autoset", false)
	user.Set("last_active", serializers.Now())
	// Django's User defaults: `user_timezone="UTC"`, `last_login_medium="email"`.
	// Plane's frontends pass user_timezone straight to Intl.DateTimeFormat, so a
	// null here crashes the workspace home (components/home/user-greetings.tsx).
	user.Set("user_timezone", "UTC")
	user.Set("last_login_medium", "email")
	user.SetPassword(password)
	if err := h.App.Save(user); err != nil {
		return h.redirect(e, nextPath, errorDict(codeAuthFailedSignIn, "AUTHENTICATION_FAILED_SIGN_UP", map[string]any{"email": email}))
	}
	h.touchLogin(user)

	if err := middleware.SetSessionCookie(e, h.App, user, middleware.SessionCookieName); err != nil {
		return err
	}
	return h.redirect(e, h.redirectionPath(user, nextPath), nil)
}

// SignOut serves POST /auth/sign-out/: clear the session cookie and redirect to
// the app root, matching SignOutAuthEndpoint.
func (h *Handler) SignOut(e *core.RequestEvent) error {
	middleware.ClearSessionCookie(e, middleware.SessionCookieName)
	return h.redirect(e, "", nil)
}

// Session serves GET /api/users/session/, the "am I logged in" probe
// (UserSessionEndpoint).
func (h *Handler) Session(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return e.JSON(http.StatusOK, map[string]any{"is_authenticated": false})
	}
	return e.JSON(http.StatusOK, map[string]any{
		"is_authenticated": true,
		"user":             serializers.UserMe(h.App, user),
	})
}

// Credential kinds written by deliverCredential. The `kind=` marker inside each
// line lets an operator tell a magic sign-in code from a password-reset token
// when both land in the same delivery file.
const (
	kindMagicSignIn   = "magic-sign-in"
	kindPasswordReset = "password-reset"
)

// mailLogPath returns the dedicated delivery file for emailed bearer
// credentials (magic-link codes and password-reset tokens), or "" when unset
// (credentials then fall back to the process log).
//
// PLANE_MAGIC_LOG is the primary/back-compat variable; PLANE_MAIL_LOG is an
// override that wins when both are set.
func mailLogPath() string {
	if v := strings.TrimSpace(os.Getenv("PLANE_MAIL_LOG")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("PLANE_MAGIC_LOG"))
}

// deliverMagicCode hands a magic sign-in code to the operator. Plane emails it;
// with no SMTP transport configured the code is written to a dedicated log file
// (or the process log) instead. It is a thin wrapper over deliverCredential so
// that magic codes and password-reset tokens share one channel.
func deliverMagicCode(email, code string) {
	deliverCredential(kindMagicSignIn, email, code)
}

// deliverResetToken hands a password-reset token to the operator through the
// same channel as magic codes, so an operator looks in one place for both.
func deliverResetToken(email, token string) {
	deliverCredential(kindPasswordReset, email, token)
}

// deliverCredential hands a bearer credential to the operator. Plane emails it;
// with no SMTP transport configured we append it to a dedicated log file so the
// credential stays out of the general application log. When the file is unset,
// or cannot be opened or written, the credential goes to the process log
// (matching Plane's dev-mode behaviour) — a credential must never be swallowed.
//
// The file is opened append-only with 0600 — these are bearer credentials and
// must not be world-readable.
func deliverCredential(kind, email, code string) {
	now := time.Now().UTC()
	line := fmt.Sprintf("%s\tkind=%s\t%s\t%s\texpires_at=%s\n",
		now.Format(time.RFC3339), kind, email, code,
		now.Add(magicCodeTTL).Format(time.RFC3339))

	path := mailLogPath()
	if path == "" {
		log.Printf("[plane] %s code for %s: %s", kind, email, code)
		return
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("[plane] mail log %s unavailable (%v); %s for %s: %s", path, err, kind, email, code)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		log.Printf("[plane] mail log write to %s failed (%v); %s for %s: %s", path, err, kind, email, code)
	}
}

// MagicGenerate serves POST /auth/magic-generate/. Email delivery is out of
// scope for this slice, so the code is stored in the app runtime store and
// delivered to a dedicated log file (or the process log) instead of being sent;
// the response shape matches Plane's MagicGenerateEndpoint
// (`{"key": "magic_<email>"}`).
func (h *Handler) MagicGenerate(e *core.RequestEvent) error {
	var body struct {
		Email string `json:"email"`
	}
	_ = e.BindBody(&body)
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if !validEmail(email) {
		return e.JSON(http.StatusBadRequest, errorDict(codeInvalidEmail, "INVALID_EMAIL", nil))
	}

	code := magicCode()
	key := "magic_" + email
	h.App.Store().Set(key, magicEntry{Code: code, Expires: time.Now().Add(magicCodeTTL)})
	deliverMagicCode(email, code)

	return e.JSON(http.StatusOK, map[string]any{"key": key})
}

// MagicSignIn serves POST /auth/magic-sign-in/ (native form POST → 302).
func (h *Handler) MagicSignIn(e *core.RequestEvent) error {
	nextPath := e.Request.FormValue("next_path")
	code := strings.TrimSpace(e.Request.FormValue("code"))
	email := strings.ToLower(strings.TrimSpace(e.Request.FormValue("email")))

	if code == "" || email == "" {
		return h.redirect(e, nextPath, errorDict(codeMagicRequiredSignIn, "MAGIC_SIGN_IN_EMAIL_CODE_REQUIRED", nil))
	}

	user, _ := h.findUserByEmail(email)
	if user == nil {
		return h.redirect(e, nextPath, errorDict(codeUserDoesNotExist, "USER_DOES_NOT_EXIST", map[string]any{"email": email}))
	}

	entry, ok := h.App.Store().GetOk("magic_" + email)
	if !ok {
		return h.redirect(e, nextPath, errorDict(codeExpiredMagicSignIn, "EXPIRED_MAGIC_CODE_SIGN_IN", map[string]any{"email": email}))
	}
	stored, _ := entry.(magicEntry)
	if time.Now().After(stored.Expires) {
		h.App.Store().Remove("magic_" + email)
		return h.redirect(e, nextPath, errorDict(codeExpiredMagicSignIn, "EXPIRED_MAGIC_CODE_SIGN_IN", map[string]any{"email": email}))
	}
	if stored.Code != code {
		return h.redirect(e, nextPath, errorDict(codeInvalidMagicSignIn, "INVALID_MAGIC_CODE_SIGN_IN", map[string]any{"email": email}))
	}
	h.App.Store().Remove("magic_" + email)

	h.touchLogin(user)
	if err := middleware.SetSessionCookie(e, h.App, user, middleware.SessionCookieName); err != nil {
		return err
	}
	return h.redirect(e, h.redirectionPath(user, nextPath), nil)
}

// ForgotPassword serves POST /auth/forgot-password/. SMTP is out of scope, so
// the reset token is generated and delivered through the same dedicated log
// file (or process-log fallback) as magic sign-in codes rather than emailed.
func (h *Handler) ForgotPassword(e *core.RequestEvent) error {
	var body struct {
		Email string `json:"email"`
	}
	_ = e.BindBody(&body)
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if !validEmail(email) {
		return e.JSON(http.StatusBadRequest, errorDict(codeInvalidEmail, "INVALID_EMAIL", nil))
	}
	user, _ := h.findUserByEmail(email)
	if user == nil {
		return e.JSON(http.StatusBadRequest, errorDict(codeUserDoesNotExist, "USER_DOES_NOT_EXIST", nil))
	}
	token := randomHex(20)
	h.App.Store().Set("password_reset_"+token, magicEntry{Code: email, Expires: time.Now().Add(magicCodeTTL)})
	deliverResetToken(email, token)
	return e.JSON(http.StatusOK, map[string]any{"message": "Check your email to reset your password"})
}

// SetPassword serves POST /auth/set-password/ (SetUserPasswordEndpoint).
func (h *Handler) SetPassword(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	var body struct {
		Password string `json:"password"`
	}
	_ = e.BindBody(&body)
	if user.GetBool("is_password_autoset") == false {
		return e.JSON(http.StatusBadRequest, errorDict(codePasswordAlreadySet, "PASSWORD_ALREADY_SET",
			map[string]any{"error": "Your password is already set please change your password from profile"}))
	}
	if body.Password == "" {
		return e.JSON(http.StatusBadRequest, errorDict(codeInvalidPassword, "INVALID_PASSWORD", nil))
	}
	user.SetPassword(body.Password)
	user.Set("is_password_autoset", false)
	if err := h.App.Save(user); err != nil {
		return err
	}
	if err := middleware.SetSessionCookie(e, h.App, user, middleware.SessionCookieName); err != nil {
		return err
	}
	return e.JSON(http.StatusOK, serializers.UserFull(h.App, user))
}

// magicEntry is the stored magic/reset code together with its expiry.
type magicEntry struct {
	Code    string
	Expires time.Time
}

// findUserByEmail resolves a user by (lowercased) email, returning (nil, nil)
// when absent.
func (h *Handler) findUserByEmail(email string) (*core.Record, error) {
	user, err := h.App.FindAuthRecordByEmail("users", email)
	if err != nil || user == nil {
		return nil, nil
	}
	return user, nil
}

// touchLogin records the login bookkeeping Plane's Adapter.save_user_data writes.
func (h *Handler) touchLogin(user *core.Record) {
	user.Set("last_active", serializers.Now())
	user.Set("last_login_time", serializers.Now())
	user.Set("last_login_medium", "email")
	_ = h.App.Save(user)
}

// redirectionPath mirrors plane/authentication/utils/redirection_path.py using
// the memberships that exist in this slice: the requested path wins, then the
// user's first workspace, otherwise the app decides (create-workspace).
func (h *Handler) redirectionPath(user *core.Record, nextPath string) string {
	if nextPath != "" {
		return nextPath
	}
	member, err := h.App.FindFirstRecordByFilter(
		"workspace_members",
		"member = {:member} && is_active = true && deleted_at = ''",
		dbx.Params{"member": user.Id},
	)
	if err == nil && member != nil {
		if ws, _ := h.App.FindRecordById("workspaces", member.GetString("workspace")); ws != nil {
			return ws.GetString("slug")
		}
	}
	return "create-workspace"
}

// redirect builds Plane's safe redirect URL and issues the 302.
func (h *Handler) redirect(e *core.RequestEvent, nextPath string, params map[string]any) error {
	return e.Redirect(http.StatusFound, safeRedirectURL(appBaseURL(), nextPath, params))
}

// appBaseURL is Plane's WEB_URL / APP_BASE_URL. It is read from the environment
// so the redirect can point at the frontend origin; when unset the redirect is
// relative, which a browser resolves against the API origin.
func appBaseURL() string {
	for _, key := range []string{"PLANE_APP_BASE_URL", "WEB_URL", "APP_BASE_URL"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

// safeRedirectURL is a port of plane/utils/path_validator.get_safe_redirect_url.
func safeRedirectURL(baseURL, nextPath string, params map[string]any) string {
	base := strings.TrimRight(baseURL, "/")

	queryParts := make([]string, 0, 2)
	if validated := validateNextPath(nextPath); validated != "" {
		queryParts = append(queryParts, "next_path="+url.QueryEscape(validated))
	}
	if len(params) > 0 {
		values := url.Values{}
		for k, v := range params {
			values.Set(k, toString(v))
		}
		queryParts = append(queryParts, values.Encode())
	}

	if len(queryParts) > 0 {
		return base + "/?" + strings.Join(queryParts, "&")
	}
	if base == "" {
		return "/"
	}
	return base
}

// validateNextPath is a port of plane/utils/path_validator.validate_next_path:
// only same-origin absolute paths survive.
func validateNextPath(nextPath string) string {
	if nextPath == "" || len(nextPath) > 500 {
		return ""
	}
	nextPath = strings.ReplaceAll(nextPath, "\\", "")
	nextPath = strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(nextPath)

	if parsed, err := url.Parse(nextPath); err == nil && (parsed.Scheme != "" || parsed.Host != "") {
		nextPath = parsed.Path
	}
	if nextPath == "" || !strings.HasPrefix(nextPath, "/") {
		return ""
	}
	if strings.HasPrefix(nextPath, "//") {
		return ""
	}
	if strings.Contains(nextPath, "..") {
		return ""
	}
	return nextPath
}

// errorDict mirrors AuthenticationException.get_error_dict.
func errorDict(code int, message string, extra map[string]any) map[string]any {
	out := map[string]any{"error_code": code, "error_message": message}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int:
		return strconv.Itoa(t)
	default:
		return ""
	}
}

// validEmail is a deliberately small check; Plane relies on Django's
// EmailValidator and the frontends validate first, so this guards the API.
func validEmail(email string) bool {
	if email == "" || strings.ContainsAny(email, " \t\r\n") {
		return false
	}
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return false
	}
	return strings.Contains(email[at+1:], ".")
}

// displayNameFor mirrors User.get_display_name: the email local part.
func displayNameFor(email string) string {
	if parts := strings.Split(email, "@"); len(parts) == 2 {
		return parts[0]
	}
	return randomHex(3)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// magicCode returns a 6-digit code in [100000, 999999], matching
// MagicCodeProvider.initiate.
func magicCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		return "100000"
	}
	return strconv.FormatInt(n.Int64()+100000, 10)
}
