package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/dashboard"
)

// RegisterDashboard wires the workspace dashboard domain
// (`/api/workspaces/{slug}/dashboard/`, `/api/dashboard/{dashboard_id}/…`).
//
// Contract: spec/api-spec.yaml, group `dashboard`; frontend call sites in
// plane-preview/apps/web/services/dashboard.service.ts.
//
// URL shapes resolved from the frontend (do NOT guess):
//
//	GET  /api/workspaces/{slug}/dashboard/                    (query: dashboard_type=home)
//	GET  /api/workspaces/{slug}/dashboard/{dashboard_id}/
//	GET  /api/dashboard/{dashboard_id}/
//	PATCH /api/dashboard/{dashboard_id}/widgets/{widget_id}/
//
// The last two are deliberately NOT workspace-scoped: they hang off `e.Router`
// directly rather than `workspaceGroup`, so `ResolveWorkspace` does not run.
// The handler therefore performs authentication AND an explicit ownership check
// (a caller may only read/patch a dashboard whose `owned_by` is the caller).
//
// Every route ends in "/", so every one is `{$}`-anchored.
func RegisterDashboard(e *core.ServeEvent, app core.App) error {
	h := dashboard.New(app)

	ws := workspaceGroup(e, app)
	ws.GET("/dashboard/{$}", h.HomeDashboard)
	ws.GET("/dashboard/{dashboard_id}/{$}", h.WidgetStats)

	// NOT under /api/workspaces/… — registered directly, with the handler's own
	// auth + ownership checks.
	e.Router.GET("/api/dashboard/{dashboard_id}/{$}", h.WidgetStatsByID)
	e.Router.PATCH("/api/dashboard/{dashboard_id}/widgets/{widget_id}/{$}", h.UpdateWidget)

	return nil
}
