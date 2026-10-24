package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

// issuedCookies runs fn against a plain-HTTP test request and returns the
// cookies it set.
func issuedCookies(t *testing.T, fn func(e *core.RequestEvent)) []*http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: nil}
	e.Request = req
	e.Response = rec
	fn(e)
	return rec.Result().Cookies()
}

func cookieByName(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("cookie %s not set (got %v)", name, cookies)
	return nil
}

// TestCookieSameSiteConfig pins the PLANE_COOKIE_SAMESITE contract: Lax by
// default, Strict opt-in, and None for cross-site deployments — where browsers
// require Secure, so it is forced on even though the test request is plain
// HTTP.
func TestCookieSameSiteConfig(t *testing.T) {
	t.Setenv("PLANE_COOKIE_SAMESITE", "")

	cookies := issuedCookies(t, func(e *core.RequestEvent) {
		SetCSRFCookie(e, "token")
	})
	c := cookieByName(t, cookies, CSRFCookieName)
	if c.SameSite != http.SameSiteLaxMode || c.Secure {
		t.Fatalf("default: want Lax over plain HTTP, got SameSite=%v Secure=%v", c.SameSite, c.Secure)
	}

	t.Setenv("PLANE_COOKIE_SAMESITE", "strict")
	cookies = issuedCookies(t, func(e *core.RequestEvent) {
		SetCSRFCookie(e, "token")
	})
	c = cookieByName(t, cookies, CSRFCookieName)
	if c.SameSite != http.SameSiteStrictMode || c.Secure {
		t.Fatalf("strict: want Strict, plain-HTTP Secure=false, got SameSite=%v Secure=%v", c.SameSite, c.Secure)
	}

	t.Setenv("PLANE_COOKIE_SAMESITE", "none")
	// SetSessionCookie signs a token, so it needs a real user record.
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)
	coll, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection: %v", err)
	}
	user := core.NewRecord(coll)
	user.Set("uuid", "00000000-0000-0000-0000-0000000000a1")
	user.Set("username", "me")
	user.Set("email", "me@example.com")
	user.Set("password", "1234567890")
	user.Set("display_name", "Me")
	user.Set("last_active", "2024-01-01 00:00:00.000Z")
	if err := app.Save(user); err != nil {
		t.Fatalf("save user: %v", err)
	}

	cookies = issuedCookies(t, func(e *core.RequestEvent) {
		e.App = app
		if err := SetSessionCookie(e, app, user, SessionCookieName); err != nil {
			t.Errorf("set session cookie: %v", err)
		}
		SetCSRFCookie(e, "token")
	})
	c = cookieByName(t, cookies, SessionCookieName)
	if c.SameSite != http.SameSiteNoneMode {
		t.Fatalf("none session: want SameSite=None, got %v", c.SameSite)
	}
	if !c.Secure {
		t.Fatalf("none session: Secure must be forced (browsers reject SameSite=None without it)")
	}
	csrf := cookieByName(t, cookies, CSRFCookieName)
	if csrf.SameSite != http.SameSiteNoneMode || !csrf.Secure {
		t.Fatalf("none csrf: want None+Secure, got SameSite=%v Secure=%v", csrf.SameSite, csrf.Secure)
	}

	// The clearing cookie repeats the same attributes so the browser actually
	// replaces the session cookie.
	clear := issuedCookies(t, func(e *core.RequestEvent) {
		ClearSessionCookie(e, SessionCookieName)
	})
	c = cookieByName(t, clear, SessionCookieName)
	if c.SameSite != http.SameSiteNoneMode || !c.Secure || c.MaxAge != -1 {
		t.Fatalf("clear under none: want None+Secure+MaxAge=-1, got SameSite=%v Secure=%v MaxAge=%d",
			c.SameSite, c.Secure, c.MaxAge)
	}

	// Unknown values fall back to Lax.
	t.Setenv("PLANE_COOKIE_SAMESITE", "nonsense")
	cookies = issuedCookies(t, func(e *core.RequestEvent) {
		SetCSRFCookie(e, "token")
	})
	c = cookieByName(t, cookies, CSRFCookieName)
	if c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unknown value: want Lax fallback, got %v", c.SameSite)
	}
}
