package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// ctxAPIKeyUser stores the token-authenticated user on the request.
const ctxAPIKeyUser = "apiKeyUser"

// Plane's open API authenticates with an `X-Api-Key` header against the
// api_tokens table (plane/api/middleware/api_authentication.py): the token
// must be active, unexpired, and belong to an active user.
func APIKey(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		header := e.Request.Header.Get("X-Api-Key")
		token := strings.TrimSpace(header)
		if token == "" {
			return e.Next()
		}
		rec, err := app.FindFirstRecordByFilter("api_tokens",
			"token = {:token} && is_active = 1",
			dbx.Params{"token": token})
		if err != nil || rec == nil {
			return e.JSON(http.StatusUnauthorized, map[string]any{"error": "Given API token is not valid"})
		}
		if exp := rec.GetString("expired_at"); exp != "" {
			if t, err := time.Parse(time.RFC3339, exp); err == nil && !t.After(time.Now()) {
				return e.JSON(http.StatusUnauthorized, map[string]any{"error": "Given API token is not valid"})
			}
		}
		user, _ := serializers.FindByUUIDOrID(app, "users", rec.GetString("user"))
		if user == nil || !user.GetBool("is_active") {
			return e.JSON(http.StatusUnauthorized, map[string]any{"error": "Given API token is not valid"})
		}
		// Save last_used (best effort, mirrors Django).
		rec.Set("last_used", serializers.Now())
		_ = app.Save(rec)
		e.Set(ctxAPIKeyUser, user)
		return e.Next()
	}
}

// APIKeyUser returns the token-authenticated user, or nil.
func APIKeyUser(e *core.RequestEvent) *core.Record {
	if v, ok := e.Get(ctxAPIKeyUser).(*core.Record); ok {
		return v
	}
	return nil
}
