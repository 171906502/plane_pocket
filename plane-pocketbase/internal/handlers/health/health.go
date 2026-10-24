// Package health exposes the unauthenticated liveness/readiness probe that a
// reverse proxy or systemd can poll.
//
// PocketBase already owns GET /api/health, but its handler returns 200
// unconditionally and never touches the database (see pocketbase/apis/health.go),
// so it cannot distinguish "process alive" from "process alive but database
// broken". This endpoint runs a trivial query against the data database and
// reports 503 when that query fails.
package health

import (
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// Handler holds the app dependency so it can verify the database.
type Handler struct {
	App core.App
}

// New returns a health handler bound to app.
func New(app core.App) *Handler {
	return &Handler{App: app}
}

// Check serves GET /health.
//
// The response deliberately carries no paths, connection strings or table
// names: a probe endpoint is frequently world-readable, so it must not become
// an information-disclosure surface.
func (h *Handler) Check(e *core.RequestEvent) error {
	if !h.dbOK() {
		return e.JSON(http.StatusServiceUnavailable, map[string]any{
			"status": "degraded",
			"db":     "error",
		})
	}

	return e.JSON(http.StatusOK, map[string]any{
		"status":  "ok",
		"version": Version(),
		"db":      "ok",
	})
}

// dbOK reports whether the data database answers a trivial query.
//
// It is defensive by design: a health probe must never take the process down,
// so a nil builder (unbootstrapped or closed app) and any panic raised by a
// half-torn-down driver both resolve to "not ok" instead of propagating.
func (h *Handler) dbOK() (ok bool) {
	if h.App == nil {
		return false
	}

	defer func() {
		if recover() != nil {
			ok = false
		}
	}()

	db := h.App.DB()
	if db == nil {
		return false
	}

	var n int
	if err := db.NewQuery("SELECT 1").Row(&n); err != nil {
		return false
	}

	return true
}

// Version returns the build version of the running binary, or "unknown" when
// build information is unavailable (e.g. under `go run` / `go test`, where the
// main module version is "(devel)").
func Version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil {
		return "unknown"
	}

	v := strings.TrimSpace(info.Main.Version)
	if v == "" || v == "(devel)" {
		return "unknown"
	}

	return v
}
