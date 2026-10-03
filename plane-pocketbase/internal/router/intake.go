package router

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/intake"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
)

// RegisterIntake wires the intake/inbox and draft-issue domains.
// Contract: spec/api-spec.yaml, group `intake` (plus the workspace-level
// `draft-issues` / `draft-to-issue` routes the intake domain owns); Django views
// in apps/api/plane/app/views/intake/** and .../views/workspace/draft.py.
//
// Project routes run through ResolveProject; `{pk}` on the intake-issue detail
// routes is the underlying **issue UUID**. The `/inboxes/` and `/inbox-issues/`
// paths are aliases of `/intakes/` and `/intake-issues/` (same view classes).
func RegisterIntake(e *core.ServeEvent, app core.App) error {
	h := intake.New(app)

	ws := workspaceGroup(e, app)
	proj := ws.Group("/projects/{project_id}").BindFunc(middleware.ResolveProject(app))

	// Intakes (and the inbox alias).
	proj.GET("/intakes/{$}", h.IntakesList)
	proj.POST("/intakes/{$}", h.IntakesCreate)
	proj.GET("/intakes/{pk}/{$}", h.IntakeDetail)
	proj.PATCH("/intakes/{pk}/{$}", h.IntakeUpdate)
	proj.DELETE("/intakes/{pk}/{$}", h.IntakeDestroy)

	proj.GET("/inboxes/{$}", h.IntakesList)
	proj.POST("/inboxes/{$}", h.IntakesCreate)
	proj.GET("/inboxes/{pk}/{$}", h.IntakeDetail)
	proj.PATCH("/inboxes/{pk}/{$}", h.IntakeUpdate)
	proj.DELETE("/inboxes/{pk}/{$}", h.IntakeDestroy)

	// Intake issues (and the inbox-issue alias).
	proj.GET("/intake-issues/{$}", h.IssuesList)
	proj.POST("/intake-issues/{$}", h.IssuesCreate)
	proj.GET("/intake-issues/{pk}/{$}", h.IssueDetail)
	proj.PATCH("/intake-issues/{pk}/{$}", h.IssueUpdate)
	proj.DELETE("/intake-issues/{pk}/{$}", h.IssueDelete)

	proj.GET("/inbox-issues/{$}", h.IssuesList)
	proj.POST("/inbox-issues/{$}", h.IssuesCreate)
	proj.GET("/inbox-issues/{pk}/{$}", h.IssueDetail)
	proj.PATCH("/inbox-issues/{pk}/{$}", h.IssueUpdate)
	proj.DELETE("/inbox-issues/{pk}/{$}", h.IssueDelete)

	// Triage state.
	proj.GET("/intake-state/{$}", h.IntakeState)

	// Workspace-level draft issues (private to their creator).
	ws.GET("/draft-issues/{$}", h.DraftsList)
	ws.POST("/draft-issues/{$}", h.DraftsCreate)
	ws.GET("/draft-issues/{pk}/{$}", h.DraftDetail)
	ws.PATCH("/draft-issues/{pk}/{$}", h.DraftUpdate)
	ws.DELETE("/draft-issues/{pk}/{$}", h.DraftDestroy)
	ws.POST("/draft-to-issue/{draft_id}/{$}", h.DraftToIssue)

	return nil
}
