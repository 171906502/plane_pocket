package router

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// RegisterStatic optionally serves the built frontend bundles straight from the
// Go binary, so a single process can host the SPAs without a separate web
// server in front.
//
// Three environment variables control it:
//
//	PLANE_WEB_DIR    path to apps/web/build/client    -> mounted at /
//	PLANE_ADMIN_DIR  path to apps/admin/build/client  -> mounted at /god-mode
//	PLANE_SPACE_DIR  path to apps/space/build/client  -> mounted at /spaces
//
// All are opt-in: when unset nothing is registered and the deployment keeps
// using nginx (or the Vite dev server), so this is safe to leave in the binary.
//
// `space` is only static when built with PLANE_SPACE_SSR unset (the default);
// with PLANE_SPACE_SSR=1 it stays server-rendered and needs a Node process,
// in which case leave PLANE_SPACE_DIR empty and proxy /spaces to that process.
//
// `live` is not covered: N-10 is deferred.
//
// The catch-all is bound at Priority 999 so every real route (/api/*, /auth/*,
// PocketBase's own /_/) is already registered. Go 1.22's ServeMux always
// prefers the more specific pattern, so the catch-all only sees leftovers.
func RegisterStatic(app core.App) {
	webDir := os.Getenv("PLANE_WEB_DIR")
	adminDir := os.Getenv("PLANE_ADMIN_DIR")
	spaceDir := os.Getenv("PLANE_SPACE_DIR")
	if webDir == "" && adminDir == "" && spaceDir == "" {
		return
	}

	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Func: func(e *core.ServeEvent) error {
			if adminDir != "" {
				if err := assertIndex(adminDir); err != nil {
					return fmt.Errorf("PLANE_ADMIN_DIR: %w", err)
				}
				// admin is built with basename=/god-mode, so its assets are
				// already referenced as /god-mode/assets/... -- mounting the
				// same prefix keeps the two in sync.
				fsys := os.DirFS(adminDir)
				e.Router.GET("/god-mode/{path...}", cachedStatic(fsys, "/god-mode", nil))
			}

			if spaceDir != "" {
				if err := assertIndex(spaceDir); err != nil {
					return fmt.Errorf("PLANE_SPACE_DIR: %w", err)
				}
				// Same reasoning as admin: basename=/spaces, so the bundle
				// requests /spaces/assets/... on its own.
				//
				// The headers below used to come from `headers` exports in
				// space's root.tsx / page.tsx. SPA mode forbids that export, so
				// they moved here.
				fsys := os.DirFS(spaceDir)
				e.Router.GET("/spaces/{path...}", cachedStatic(fsys, "/spaces", spaceSecurityHeaders))
			}

			if webDir != "" {
				if err := assertIndex(webDir); err != nil {
					return fmt.Errorf("PLANE_WEB_DIR: %w", err)
				}
				// HasRoute guard: a user-provided "/{path...}" takes precedence.
				if !e.Router.HasRoute(http.MethodGet, "/{path...}") {
					fsys := os.DirFS(webDir)
					e.Router.GET("/{path...}", cachedStatic(fsys, "", nil))
				}
			}

			return e.Next()
		},
		Priority: 999,
	})
}

// immutableMaxAge is one year, the maximum browsers honour for
// `Cache-Control: immutable`. Safe only for content-addressed filenames.
const immutableMaxAge = "public, max-age=31536000, immutable"

// spaceSecurityHeaders replaces the `headers` route export that react-router's
// SPA mode refuses. Keep this list in sync with the comment left in
// plane-preview/apps/space/app/root.tsx.
var spaceSecurityHeaders = map[string]string{
	"Referrer-Policy":           "origin-when-cross-origin",
	"X-Content-Type-Options":    "nosniff",
	"X-DNS-Prefetch-Control":    "on",
	"Strict-Transport-Security": "max-age=31536000; includeSubDomains; preload",
	"X-Frame-Options":           "SAMEORIGIN",
}

// cachedStatic wraps apis.Static with a Cache-Control policy and optional fixed
// headers.
//
// The cache decision is made by looking at the filesystem, not at the URL
// prefix, because apis.Static answers unknown paths with the SPA's index.html.
// A naive "everything under /assets/ is immutable" rule would then let a deep
// link like /assets/typo cache index.html for a year -- and that HTML would
// keep pointing at the old bundle hashes long after a redeploy.
//
// So: a request gets the long-lived policy only when the file it names really
// exists AND lives in Vite's content-hashed assets directory. Everything else
// (index.html, favicon, manifest, and every SPA fallback) is `no-cache`, which
// still allows a 304 through Last-Modified but forbids reuse without
// revalidation.
func cachedStatic(fsys fs.FS, urlPrefix string, headers map[string]string) func(*core.RequestEvent) error {
	handler := apis.Static(fsys, true)

	return func(e *core.RequestEvent) error {
		for k, v := range headers {
			e.Response.Header().Set(k, v)
		}

		if isHashedAsset(fsys, urlPrefix, e.Request.URL.Path) {
			e.Response.Header().Set("Cache-Control", immutableMaxAge)
		} else {
			e.Response.Header().Set("Cache-Control", "no-cache")
		}

		return handler(e)
	}
}

// isHashedAsset reports whether the request resolves to an existing file inside
// the bundle's `assets/` directory. Vite emits those names with a content hash
// (app-D3f9x1.js), so a given URL can never change contents.
func isHashedAsset(fsys fs.FS, urlPrefix, urlPath string) bool {
	rel := strings.TrimPrefix(urlPath, urlPrefix)
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return false
	}

	clean := path.Clean(rel)
	if clean != rel || !strings.HasPrefix(clean, "assets/") {
		return false
	}

	fi, err := fs.Stat(fsys, clean)
	return err == nil && !fi.IsDir()
}

// assertIndex fails fast on a mistyped path instead of answering 404 to every
// asset request, which is painful to diagnose behind a reverse proxy.
func assertIndex(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		return fmt.Errorf("%s has no index.html (run the frontend build first)", dir)
	}
	return nil
}
