package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/estimate"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
)

// RegisterEstimate wires the estimate domain
// (`/api/workspaces/{slug}/estimates/…`, `/api/workspaces/{slug}/projects/{pid}/estimates/…`).
//
// Contract: spec/api-spec.yaml, group `estimate`; Django views in
// apps/api/plane/app/urls/estimate.py; frontend call sites in
// plane-preview/apps/web/services/estimate.service.ts.
//
// NOTE the SAME resource is reachable at two different scopes:
//
//	GET         /api/workspaces/{slug}/estimates/                                    (workspace-wide list)
//	GET, POST   /api/workspaces/{slug}/projects/{pid}/estimates/                     (project-scoped list/create)
//	GET, PATCH, DELETE /api/workspaces/{slug}/projects/{pid}/estimates/{estimate_id}/
//	GET, POST   /api/workspaces/{slug}/projects/{pid}/estimates/{estimate_id}/estimate-points/
//	GET, PATCH, DELETE /api/workspaces/{slug}/projects/{pid}/estimates/{estimate_id}/estimate-points/{point_id}/
//
// The estimate-points collection already exists (pb_migrations), so no new
// migration is required.
//
// Every route ends in "/", so every one is `{$}`-anchored (Go 1.22+ ServeMux
// treats a trailing-slash pattern as a subtree otherwise).
func RegisterEstimate(e *core.ServeEvent, app core.App) error {
	h := estimate.New(app)

	ws := workspaceGroup(e, app)
	// Workspace-wide list (WorkspaceEstimatesEndpoint).
	ws.GET("/estimates/{$}", h.WorkspaceList)

	proj := ws.Group("/projects/{project_id}").BindFunc(middleware.ResolveProject(app))

	// NOTE: the project-scoped `GET …/estimates/` is registered in
	// router/project.go (Wave 1, handlers/projects) and must NOT be re-registered
	// here — a duplicate pattern panics ServeMux at startup.
	proj.POST("/estimates/{$}", h.Create)
	proj.GET("/estimates/{estimate_id}/{$}", h.Detail)
	proj.PATCH("/estimates/{estimate_id}/{$}", h.Update)
	proj.DELETE("/estimates/{estimate_id}/{$}", h.Destroy)

	proj.GET("/estimates/{estimate_id}/estimate-points/{$}", h.PointsList)
	proj.POST("/estimates/{estimate_id}/estimate-points/{$}", h.PointCreate)
	proj.GET("/estimates/{estimate_id}/estimate-points/{point_id}/{$}", h.PointDetail)
	proj.PATCH("/estimates/{estimate_id}/estimate-points/{point_id}/{$}", h.PointUpdate)
	proj.DELETE("/estimates/{estimate_id}/estimate-points/{point_id}/{$}", h.PointDestroy)

	return nil
}
