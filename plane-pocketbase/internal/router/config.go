package router

import (
	"github.com/pocketbase/pocketbase/core"
)

// RegisterConfig wires the two top-level, non-workspace-scoped read endpoints
// the web app fetches on boot.
//
// Contract: spec/api-spec.yaml. Both are FRONTEND-ONLY call sites — there is no
// Django view or URL entry for either in this checkout (verified: neither
// `configs` nor `release-notes` appears in apps/api/plane/app/urls/*.py), which
// puts them in the same class as `issues-detail/` and `my-issues/`. Their
// response shapes must therefore be derived from the frontend's TypeScript
// types plus the Django serializers they mirror, NOT from a view.
//
// URL shapes resolved from the frontend (do NOT guess):
//
//	GET /api/configs/        <- plane-preview/apps/web/services/app_config.service.ts:21
//	GET /api/release-notes/  <- plane-preview/apps/web/services/workspace.service.ts:225
//
// NOTE (orchestrator): placeholder created ahead of Wave 2 so the domain exists
// in the registrars table before the implementing agent starts. The owning
// agent fills in the body; it must not need to touch router.go.
func RegisterConfig(e *core.ServeEvent, app core.App) error {
	_ = e
	_ = app
	return nil
}
