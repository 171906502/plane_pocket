package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/live"
)

// registerLive mounts the page editor's collaboration socket.
//
// `/live/collaboration` is the path the web app derives at runtime from
// `LIVE_BASE_URL` + `LIVE_BASE_PATH` (`apps/web/components/pages/editor/editor-body.tsx`),
// which in single-port mode resolves to the same origin as the API. Until this
// route existed, the WebSocket upgrade fell through to the SPA catch-all and
// was answered with `200 text/html` instead of `101 Switching Protocols` — the
// editor then retried forever and never synced.
//
// The gzip middleware is unbound here because it swaps `e.Response` for a
// wrapper that does not implement http.Hijacker, which is what the WebSocket
// handshake needs. There is nothing to compress on an upgraded connection
// anyway.
func registerLive(e *core.ServeEvent, app core.App) {
	handler := live.New(app)
	// Logs outlive the pages they belong to unless something collects them; the
	// scan is a directory listing, so it runs off the serving goroutine.
	go handler.CollectOrphans()

	e.Router.GET("/live/collaboration", handler.Serve).
		Unbind(gzipMiddlewareID)
}
