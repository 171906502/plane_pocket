// Package middleware provides the request-scoped context that every Plane
// handler depends on: the authenticated user, the resolved workspace/project,
// and the caller's role within them.
package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

const (
	// SessionCookieName matches Plane's SESSION_COOKIE_NAME default. Plane's
	// frontends authenticate with a cookie (axios is configured with
	// `withCredentials: true`), so the name must match exactly.
	SessionCookieName = "session-id"

	// AdminSessionCookieName matches Plane's ADMIN_SESSION_COOKIE_NAME, used by
	// the instance admin app ("god-mode").
	AdminSessionCookieName = "admin-session-id"

	// CSRFCookieName / CSRFHeaderName / CSRFFormField mirror Django's defaults.
	// Plane's frontend sends the header for XHR calls and the form field for the
	// native form POSTs used by sign-in/sign-up/sign-out.
	CSRFCookieName = "csrftoken"
	CSRFHeaderName = "X-CSRFTOKEN"
	CSRFFormField  = "csrfmiddlewaretoken"

	// SessionCookieAge matches Plane's SESSION_COOKIE_AGE (604800s = 7 days).
	SessionCookieAge = 604800

	// usersCollection is the Plane auth collection holding user records.
	usersCollection = "users"
)

// ContextKey type avoids collisions in the request store.
type contextKey string

const (
	ctxUserKey = contextKey("plane.user")
	ctxRoleKey = contextKey("plane.role")
)

// Auth resolves the current user from the session cookie and stores it on the
// request. It never rejects a request — callers decide whether authentication
// is required, because Plane serves some routes (sign-in, public boards,
// shared spaces) anonymously.
//
// PocketBase's own `loadAuthToken` middleware already populates `e.Auth` from
// an Authorization header, so a token-authenticated request is left untouched;
// the cookie is only consulted when nothing else authenticated the request.
func Auth(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if e.Auth == nil {
			if user := userFromCookie(app, e, SessionCookieName); user != nil {
				e.Auth = user
				e.Set(string(ctxUserKey), user)
			}
		}
		// The instance-admin console authenticates with its own cookie but the
		// same user record (Plane's BaseSessionAuthentication resolves both
		// session surfaces), so fall back to it before giving up.
		if e.Auth == nil {
			if user := userFromCookie(app, e, AdminSessionCookieName); user != nil {
				e.Auth = user
				e.Set(string(ctxUserKey), user)
			}
		}
		return e.Next()
	}
}

// AdminAuth resolves an instance administrator from the admin session cookie.
func AdminAuth(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if e.Auth == nil {
			if user := userFromCookie(app, e, AdminSessionCookieName); user != nil {
				e.Auth = user
				e.Set(string(ctxUserKey), user)
			}
		}
		return e.Next()
	}
}

// RequireAuth rejects anonymous requests with Plane's 401 shape.
func RequireAuth() func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if CurrentUser(e) == nil {
			return e.JSON(http.StatusUnauthorized, map[string]any{"detail": "Authentication credentials were not provided."})
		}
		return e.Next()
	}
}

// CurrentUser returns the authenticated user, or nil for an anonymous request.
func CurrentUser(e *core.RequestEvent) *core.Record {
	if e.Auth != nil {
		return e.Auth
	}
	if v, ok := e.Get(string(ctxUserKey)).(*core.Record); ok {
		return v
	}
	return nil
}

// userFromCookie reads a session cookie and resolves it to a user record.
// The cookie carries a PocketBase auth token, which lets us reuse PocketBase's
// JWT signing, expiry and token-key invalidation instead of hand-rolling a
// session store.
func userFromCookie(app core.App, e *core.RequestEvent, cookieName string) *core.Record {
	cookie, err := e.Request.Cookie(cookieName)
	if err != nil || cookie == nil || cookie.Value == "" {
		return nil
	}
	record, err := app.FindAuthRecordByToken(cookie.Value, core.TokenTypeAuth)
	if err != nil || record == nil {
		return nil
	}
	return record
}

// cookieSameSite resolves the SameSite attribute for all auth cookies from
// `PLANE_COOKIE_SAMESITE`: `lax` (default), `strict`, or `none` for cross-site
// deployments (`app.foo.com` + `api.bar.com`). The second return value forces
// `Secure`: browsers reject `SameSite=None` cookies without it, so selecting
// none implies HTTPS unconditionally — the cookie would not survive plain
// HTTP anyway.
func cookieSameSite() (http.SameSite, bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PLANE_COOKIE_SAMESITE"))) {
	case "none":
		return http.SameSiteNoneMode, true
	case "strict":
		return http.SameSiteStrictMode, false
	default:
		return http.SameSiteLaxMode, false
	}
}

// SetSessionCookie issues the session cookie for a user.
//
// Plane's cookie is HttpOnly, so JavaScript never reads it; the frontends rely
// on the browser sending it automatically. SameSite=Lax keeps the cookie on
// top-level form POSTs (sign-in) while blocking cross-site XHR; set
// PLANE_COOKIE_SAMESITE=none for cross-site deployments (forces Secure).
func SetSessionCookie(e *core.RequestEvent, app core.App, user *core.Record, cookieName string) error {
	token, err := user.NewAuthToken()
	if err != nil {
		return err
	}
	sameSite, forceSecure := cookieSameSite()
	e.SetCookie(&http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   SessionCookieAge,
		HttpOnly: true,
		SameSite: sameSite,
		Secure:   forceSecure || isSecureRequest(e),
	})
	return nil
}

// ClearSessionCookie expires the session cookie on sign-out. The clearing
// cookie must repeat the SameSite/Secure attributes of the one being replaced,
// so it resolves them the same way.
func ClearSessionCookie(e *core.RequestEvent, cookieName string) {
	sameSite, forceSecure := cookieSameSite()
	e.SetCookie(&http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: sameSite,
		Secure:   forceSecure || isSecureRequest(e),
	})
}

// NewCSRFToken generates a random CSRF token.
func NewCSRFToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}

// SetCSRFCookie stores the CSRF token in a readable cookie. Plane's frontends
// obtain the token from GET /auth/get-csrf-token/ rather than reading the
// cookie, but the cookie is what we validate against (double-submit).
func SetCSRFCookie(e *core.RequestEvent, token string) {
	sameSite, forceSecure := cookieSameSite()
	e.SetCookie(&http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   SessionCookieAge,
		HttpOnly: false,
		SameSite: sameSite,
		Secure:   forceSecure || isSecureRequest(e),
	})
}

// CSRFProtect validates the double-submit CSRF token on unsafe methods.
//
// Plane's frontend already sends the token on every unsafe request, so this is
// transparent to the UI. Set PLANE_PB_CSRF=off to disable enforcement while
// debugging a client that does not yet send it.
func CSRFProtect() func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if strings.EqualFold(os.Getenv("PLANE_PB_CSRF"), "off") {
			return e.Next()
		}
		switch e.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
			return e.Next()
		}

		cookie, err := e.Request.Cookie(CSRFCookieName)
		if err != nil || cookie == nil || cookie.Value == "" {
			// No cookie yet (e.g. the very first sign-in). Nothing to compare
			// against, so allow it — the request is still origin-checked by CORS.
			return e.Next()
		}

		supplied := e.Request.Header.Get(CSRFHeaderName)
		if supplied == "" {
			supplied = e.Request.FormValue(CSRFFormField)
		}
		if subtle.ConstantTimeCompare([]byte(supplied), []byte(cookie.Value)) != 1 {
			return e.JSON(http.StatusForbidden, map[string]any{"detail": "CSRF verification failed."})
		}
		return e.Next()
	}
}

// isSecureRequest reports whether the request arrived over TLS, so cookies get
// the Secure flag only when it is safe to do so (Plane's SESSION_COOKIE_SECURE
// is tied to the configured origins).
func isSecureRequest(e *core.RequestEvent) bool {
	if e.Request.TLS != nil {
		return true
	}
	return strings.EqualFold(e.Request.Header.Get("X-Forwarded-Proto"), "https")
}
