// Package router registers the Plane-compatible HTTP surface on top of PocketBase.
//
// Design contract: the routes registered here must reproduce Plane's URL shapes
// and JSON response envelopes exactly, because Plane's frontends call them
// verbatim (see ../spec/api-spec.yaml, field `called_by_frontend`).
//
// Each Plane domain gets its own file in this package. Registration order does
// not matter as long as no two files claim the same method+path.
package router

import (
	"fmt"
	"net/http"
	"os"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/health"
	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/meta"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
)

// Ids for the two root-level Plane middlewares. They are named (rather than
// anonymous via BindFunc) so the /health probe can Unbind them: a liveness
// endpoint must answer before the session/CSRF layer, otherwise a broken
// session store would look like a dead process.
const (
	authMiddlewareID = "planeAuth"
	csrfMiddlewareID = "planeCSRF"
	gzipMiddlewareID = "planeGzip"
)

// Register wires every Plane route group onto the PocketBase router.
func Register(app core.App) {
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		// Session resolution runs for every route; CSRF is enforced on unsafe
		// methods only. Both are transparent to a client that already behaves
		// like Plane's frontend.
		e.Router.Bind(&hook.Handler[*core.RequestEvent]{Id: authMiddlewareID, Func: middleware.Auth(app)})
		e.Router.Bind(&hook.Handler[*core.RequestEvent]{Id: csrfMiddlewareID, Func: middleware.CSRFProtect()})

		// Plane's frontends call the API cross-origin (web :3000 → API :8000)
		// with `axios withCredentials: true`, so every response — including
		// CORS preflights — must carry `Access-Control-Allow-Credentials:
		// true`. PocketBase's built-in CORS middleware (priority -1041) never
		// sets that header and, on preflight, returns 204 without invoking any
		// later middleware, so this header must be set BEFORE it runs. The
		// allowed origins themselves are echoed by the built-in middleware
		// from `serve --origins=...` (exact match only; a wildcard origin is
		// rejected by browsers in credentialed mode).
		e.Router.Bind(&hook.Handler[*core.RequestEvent]{
			Func: func(re *core.RequestEvent) error {
				re.Response.Header().Set("Access-Control-Allow-Credentials", "true")
				return re.Next()
			},
			Priority: apis.DefaultCorsMiddlewarePriority - 1,
		})

		// On-the-fly gzip for everything below (API JSON and the static
		// bundles served by RegisterStatic). Bound just inside CORS so the
		// OPTIONS preflight, which CORS answers with a bodiless 204 without
		// calling Next, never reaches it; the explicit priority also keeps it
		// off the activity logger's slot (-1040). PLANE_GZIP=0 is the escape
		// hatch for a reverse proxy that compresses on its own.
		if os.Getenv("PLANE_GZIP") != "0" {
			e.Router.Bind(&hook.Handler[*core.RequestEvent]{
				Id:       gzipMiddlewareID,
				Func:     middleware.Gzip(),
				Priority: apis.DefaultActivityLoggerMiddlewarePriority + 1,
			})
		}

		registerMeta(e, app)
		registerHealth(e, app)
		registerLive(e, app)

		// Plane route groups. Each domain owns its own file so that parallel
		// work never has to touch a shared file. Registration order does not
		// matter as long as no two groups claim the same method+path.
		registerAuth(e, app)      // /auth/*
		registerUsers(e, app)     // /api/users/*
		registerWorkspace(e, app) // /api/workspaces/*
		registerProject(e, app)
		registerIssue(e, app)

		// Domains registered through the table below. Each entry's function
		// lives in its own file and returns nil until that domain is built.
		registrars := []struct {
			name string
			fn   func(*core.ServeEvent, core.App) error
		}{
			{"cycle", RegisterCycle},
			{"module", RegisterModule},
			{"page", RegisterPage},
			{"intake", RegisterIntake},
			{"notification", RegisterNotification},
			{"view", RegisterView},
			{"asset", RegisterAsset},
			{"search", RegisterSearch},
			{"admin", RegisterAdmin},
			{"publicapi", RegisterPublicAPI},
			// Added ahead of Wave 2 by the orchestrator: the three domains
			// below had no registrar entry, and adding one is an
			// orchestrator-only change (this table is a shared file).
			{"dashboard", RegisterDashboard},
			{"estimate", RegisterEstimate},
			{"config", RegisterConfig},
		}
		for _, r := range registrars {
			if err := r.fn(e, app); err != nil {
				return fmt.Errorf("register %s routes: %w", r.name, err)
			}
		}

		return e.Next()
	})
}

// workspaceGroup returns a router group whose routes resolve the workspace from
// the `slug` path parameter before the handler runs. Every `/api/workspaces/{slug}/…`
// route must go through it, otherwise the handler would run without an
// authorized scope.
func workspaceGroup(e *core.ServeEvent, app core.App) *router.RouterGroup[*core.RequestEvent] {
	return e.Router.Group("/api/workspaces/{slug}").BindFunc(middleware.ResolveWorkspace(app))
}

// registerHealth exposes an unauthenticated readiness probe that also verifies
// the data database, so a reverse proxy or systemd can tell "process alive"
// from "database broken".
//
// PocketBase's own GET /api/health returns 200 unconditionally and never touches
// the database, so it cannot answer that question — this is a distinct path on
// purpose rather than a shadow of the built-in route.
//
// The route is unbound from the auth/CSRF middlewares (both bound on the root
// group above) so the probe stays reachable exactly when the session layer is
// the thing that is broken.
func registerHealth(e *core.ServeEvent, app core.App) {
	e.Router.GET("/health", health.New(app).Check).
		Unbind(authMiddlewareID, csrfMiddlewareID)
}

// registerMeta exposes liveness/version information for the compatibility layer.
// PocketBase already owns GET /api/health, so this deliberately uses a distinct
// path rather than shadowing it.
func registerMeta(e *core.ServeEvent, app core.App) {
	m := meta.New(app)

	// Static reference data used by the project settings screen.
	e.Router.GET("/api/timezones/{$}", m.Timezones)
	// Django answers the unsplash route even when the integration is
	// unconfigured; the frontend calls it for the cover-image picker.
	e.Router.GET("/api/unsplash/{$}", m.Unsplash)

	e.Router.GET("/api/plane-meta/health/{$}", func(re *core.RequestEvent) error {
		return re.JSON(http.StatusOK, map[string]any{
			"status":           "ok",
			"service":          "plane-pocketbase",
			"api_compat":       "plane-v1.4.2",
			"pocketbase_ready": true,
		})
	})
}
