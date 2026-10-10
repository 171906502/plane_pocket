package cycle

import (
	"net/http"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// CycleProgress serves GET …/cycles/{cycle_id}/cycle-progress/
// (frontend `CycleService.workspaceActiveCyclesProgressPro`).
//
// DERIVED SHAPE: there is NO Django view and NO Django URL for this path (the
// only backend progress endpoint is `…/cycles/{cycle_id}/progress/`,
// CycleProgressEndpoint). The frontend types it identically to `progress/`
// (`TProgressSnapshot`, packages/types/src/cycle/cycle.ts:66), so it returns the
// same body as `progress/` — the flat issue/estimate-point counts. `distribution`
// and `estimate_distribution` are optional on TProgressSnapshot and omitted.
func (h *Handler) CycleProgress(e *core.RequestEvent) error {
	return h.Progress(e)
}

// ActiveCycles serves GET /api/workspaces/{slug}/active-cycles/
// (frontend `CycleService.workspaceActiveCycles`).
//
// DERIVED SHAPE: there is NO Django view and NO Django URL for this path either.
// The frontend types the response as `IWorkspaceActiveCyclesResponse`
// (packages/types/src/workspace.ts:219) — a cursor-paginated envelope whose
// `results` are `ICycle[]`. It mirrors the workspace-wide cycle list
// (WorkspaceCyclesEndpoint, served here by WorkspaceList) but paginated: every
// non-archived cycle in the workspace belonging to a project the caller is an
// active member of.
func (h *Handler) ActiveCycles(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	projectIDs := h.memberProjectIDs(user.Id, scope.Workspace.Id)
	if len(projectIDs) == 0 {
		return e.JSON(http.StatusOK, serializers.Envelope{
			TotalCount:      0,
			NextCursor:      "1000:1:0",
			PrevCursor:      "1000:-1:1",
			NextPageResults: false,
			PrevPageResults: false,
			Count:           0,
			TotalPages:      0,
			TotalResults:    0,
			Results:         []any{},
		})
	}

	params := dbx.Params{"workspace": scope.Workspace.Id}
	filter := "workspace = {:workspace} && deleted_at = '' && archived_at = '' && " +
		orIDFilter("project", projectIDs, params)

	perPage, cursor := serializers.ListParams(e, serializers.MaxPerPage)
	envelope, err := serializers.Paginate(h.App, serializers.ListQuery{
		Collection: "cycles",
		Filter:     filter,
		Params:     params,
		Sort:       "-created_at",
		PerPage:    perPage,
		Cursor:     cursor,
	}, func(r *core.Record) any {
		return serializers.Cycle(h.App, r, false)
	})
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, envelope)
}
