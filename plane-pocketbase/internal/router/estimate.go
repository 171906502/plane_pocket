package router

import (
	"github.com/pocketbase/pocketbase/core"
)

// RegisterEstimate wires the estimate domain
// (`/api/workspaces/{slug}/estimates/…`, `/api/workspaces/{slug}/projects/{pid}/estimates/…`).
//
// Contract: spec/api-spec.yaml, group `estimate`; Django views in
// apps/api/plane/app/urls/estimate.py; frontend call sites in
// plane-preview/apps/web/services/estimate.service.ts.
//
// NOTE (orchestrator): placeholder created ahead of Wave 2 so the `estimate`
// domain exists in the registrars table before the implementing agent starts.
//
// URL shapes resolved from the frontend (do NOT guess) — note the SAME
// resource is reachable at two different scopes:
//
//	GET         /api/workspaces/{slug}/estimates/                                    (workspace-wide list)
//	GET, POST   /api/workspaces/{slug}/projects/{pid}/estimates/                     (project-scoped list/create)
//	GET, PATCH, DELETE /api/workspaces/{slug}/projects/{pid}/estimates/{estimate_id}/
//	GET, POST   /api/workspaces/{slug}/projects/{pid}/estimates/{estimate_id}/estimate-points/
//	GET, PATCH, DELETE /api/workspaces/{slug}/projects/{pid}/estimates/{estimate_id}/estimate-points/{point_id}/
//
// The estimate-points collection already exists (pb_migrations), so no new
// migration is required.
func RegisterEstimate(e *core.ServeEvent, app core.App) error {
	_ = e
	_ = app
	return nil
}
