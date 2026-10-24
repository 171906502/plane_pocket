// Command plane-pocketbase is a PocketBase-based backend that serves Plane's
// REST API, so that Plane's existing React frontends (web / space / admin) run
// unchanged on top of a single Go binary backed by SQLite.
//
// The compatibility contract lives in ../spec/api-spec.yaml and the collection
// definitions are derived from ../spec/schema-spec.md.
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"

	"github.com/plane-pocketbase/plane-pocketbase/internal/config"
	"github.com/plane-pocketbase/plane-pocketbase/internal/router"

	// Blank import so the Go collection migrations in pb_migrations are linked
	// into the binary and their init() registrations reach core.AppMigrations.
	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

func main() {
	// PLANE_PB_DEV=1 turns on PocketBase's dev mode, which prints handler
	// errors to stderr instead of only recording them in the _logs table. It
	// is the fastest way to debug a 500 during development.
	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDev: os.Getenv("PLANE_PB_DEV") == "1",
	})

	// Collection definitions live in pb_migrations and are applied on startup.
	// Automigrate keeps newly created collections written back to disk so the
	// schema spec and the running database stay in sync during development.
	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{
		Dir:         filepath.Join("pb_migrations"),
		Automigrate: true,
	})

	// Plane compatibility layer: every Plane REST route is registered here.
	router.Register(app)

	// Optional: serve the built web/admin SPAs straight from this binary
	// (PLANE_WEB_DIR / PLANE_ADMIN_DIR). No-op unless those env vars are set.
	router.RegisterStatic(app)

	// Fail fast on a misconfigured CORS --origins list. The check is bound to
	// OnServe, so it only affects the serve path and never `migrate`.
	config.RegisterServeOriginsCheck(app, app.RootCmd)

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
