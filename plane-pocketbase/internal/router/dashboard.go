package router

import (
	"github.com/pocketbase/pocketbase/core"
)

// RegisterDashboard wires the workspace dashboard domain
// (`/api/workspaces/{slug}/dashboard/`, `/api/dashboard/{dashboard_id}/…`).
//
// Contract: spec/api-spec.yaml, group `dashboard`; frontend call sites in
// plane-preview/apps/web/services/dashboard.service.ts.
//
// NOTE (orchestrator): this file is a placeholder created ahead of Wave 2 so
// that the `dashboard` domain exists in the registrars table before the
// implementing agent starts. The agent that owns this file fills in the body;
// it must not need to touch router.go.
//
// URL shapes resolved from the frontend (do NOT guess):
//
//	GET  /api/workspaces/{slug}/dashboard/                    (query: dashboard_type=home)
//	GET  /api/workspaces/{slug}/dashboard/{dashboard_id}/
//	GET  /api/dashboard/{dashboard_id}/
//	PATCH /api/dashboard/{dashboard_id}/widgets/{widget_id}/
//
// The last two are deliberately NOT workspace-scoped.
func RegisterDashboard(e *core.ServeEvent, app core.App) error {
	_ = e
	_ = app
	return nil
}
