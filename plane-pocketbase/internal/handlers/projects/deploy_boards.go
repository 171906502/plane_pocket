package projects

import (
	"net/http"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// deployBoardFilter is the singleton lookup every deploy-board path shares.
// `deleted_at = ”` is PocketBase's "not soft-deleted" (the column is a plain
// DateField, not a NULLable one) and mirrors Django's
// `deleted_at__isnull=True` half of the unique constraint.
const deployBoardFilter = "project = {:project} && workspace = {:ws} && entity_name = 'project' && deleted_at = ''"

// DeployBoards serves GET/POST
// /api/workspaces/{slug}/projects/{project_id}/project-deploy-boards/
// (spec id `project.project-deploy-boards.list_create`, Django
// ProjectDeployBoardViewSet).
//
// GET returns a **single** object. Django's `list` is
// `Response(DeployBoardSerializer(board.first()).data)` — no `many=True` — and
// the web publish modal assigns `response.data` straight into
// `publishSettingsMap[projectID]` (project-publish.store.ts:92), which the modal
// then reads as `?.anchor` (publish-project/modal.tsx:83). Returning an array
// left `anchor` undefined, so the modal believed an already-published project
// was unpublished and re-POSTed on every open.
//
// POST publishes the project: it creates (or revives) the singleton
// `deploy_boards` row with a fresh anchor.
func (h *Handler) DeployBoards(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}

	params := mapToParams(map[string]string{
		"project": scope.Project.Id,
		"ws":      scope.Workspace.Id,
	})

	if e.Request.Method == http.MethodGet {
		board, _ := h.App.FindFirstRecordByFilter("deploy_boards", deployBoardFilter, params)
		if board == nil {
			// Django would blow up on `DeployBoardSerializer(None)` (DRF
			// dereferences `instance.id`); the modal only reads `?.anchor`, so
			// an empty object renders the "unpublished" state cleanly instead
			// of a 500 + unhandled rejection.
			return e.JSON(http.StatusOK, map[string]any{})
		}
		return e.JSON(http.StatusOK, serializers.DeployBoard(h.App, board))
	}

	// POST: publish. Plane allows project admins.
	if scope.ProjectRole < middleware.RoleAdmin {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	body := map[string]any{}
	_ = e.BindBody(&body)

	rec, _ := h.App.FindFirstRecordByFilter("deploy_boards", deployBoardFilter, params)
	if rec == nil {
		coll, err := h.App.FindCollectionByNameOrId("deploy_boards")
		if err != nil {
			return err
		}
		rec = core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("entity_name", "project")
		// entity_identifier carries the Plane UUID of the published entity and
		// is unique per entity_name (spec §2.5); anchor is the public token.
		rec.Set("entity_identifier", scope.Project.GetString("uuid"))
		rec.Set("anchor", serializers.NewUUID())
		rec.Set("workspace", scope.Workspace.Id)
		rec.Set("project", scope.Project.Id)
	}
	// Django's create() defaults (project/base.py:549-562): comments, reactions
	// and votes default to False; activity is untouched and therefore keeps the
	// model default True.
	rec.Set("is_comments_enabled", false)
	rec.Set("is_reactions_enabled", false)
	rec.Set("is_votes_enabled", false)
	rec.Set("is_activity_enabled", true)
	rec.Set("is_disabled", false)
	rec.Set("deleted_at", "")
	applyBoardFlags(rec, body)
	applyViewProps(rec, body)
	if err := h.App.Save(rec); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	// Django answers 200 here (`status.HTTP_200_OK`), not 201.
	return e.JSON(http.StatusOK, serializers.DeployBoard(h.App, rec))
}

// DeployBoardDetail serves GET/PATCH/DELETE on a single deploy board
// (spec id `project.project-deploy-boards.detail`). DELETE unpublishes.
func (h *Handler) DeployBoardDetail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	board, _ := serializers.FindByUUIDOrID(h.App, "deploy_boards", e.Request.PathValue("pk"))
	if board == nil || board.GetString("project") != scope.Project.Id || board.GetString("deleted_at") != "" {
		return serializers.NotFound(e, "")
	}
	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, serializers.DeployBoard(h.App, board))
	case http.MethodPatch:
		if scope.ProjectRole < middleware.RoleAdmin {
			return serializers.Forbidden(e, "You do not have permission to perform this action.")
		}
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		applyBoardFlags(board, body)
		applyViewProps(board, body)
		if err := h.App.Save(board); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, serializers.DeployBoard(h.App, board))
	case http.MethodDelete:
		if scope.ProjectRole < middleware.RoleAdmin {
			return serializers.Forbidden(e, "You do not have permission to perform this action.")
		}
		// Django soft-deletes (`deleted_at`), which is what frees the
		// (entity_name, entity_identifier) unique constraint for a re-publish.
		board.Set("deleted_at", serializers.Now())
		board.Set("is_disabled", true)
		if err := h.App.Save(board); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

func applyBoardFlags(rec *core.Record, body map[string]any) {
	for _, key := range []string{"is_comments_enabled", "is_reactions_enabled", "is_votes_enabled", "is_activity_enabled", "is_disabled"} {
		if v, ok := body[key].(bool); ok {
			rec.Set(key, v)
		}
	}
}

// applyViewProps persists the published layouts into the `view_props` JSON
// column.
//
// Upstream has a real mismatch here: `ProjectDeployBoardViewSet.create` reads
// `request.data.get("views", <all-true default>)` (project/base.py:553) but the
// publish modal posts `view_props` (publish-project/modal.tsx:166). So Django
// silently discards the user's layout selection on the very first publish and
// stores the all-true default. The PATCH path goes through the serializer,
// where `view_props` *is* a model field, so updates do stick.
//
// We resolve the ambiguity by accepting `views` first (exact parity whenever a
// caller sends it), then `view_props` (so the modal's List/Kanban checkboxes
// actually take effect on create), then the same all-true default Django would
// have fallen back to.
func applyViewProps(rec *core.Record, body map[string]any) {
	for _, key := range []string{"views", "view_props"} {
		v, ok := body[key].(map[string]any)
		if !ok || len(v) == 0 {
			continue
		}
		rec.Set("view_props", v)
		return
	}
	rec.Set("view_props", serializers.DefaultViewProps)
}

// mapToParams converts a string map to dbx.Params (whose value type is any).
func mapToParams(m map[string]string) dbx.Params {
	p := make(dbx.Params, len(m))
	for k, v := range m {
		p[k] = v
	}
	return p
}
