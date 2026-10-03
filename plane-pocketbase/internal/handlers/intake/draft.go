// Package intake also owns Plane's workspace-level draft-issue endpoints:
//
//	GET/POST         /api/workspaces/{slug}/draft-issues/
//	GET/PATCH/DELETE /api/workspaces/{slug}/draft-issues/{pk}/
//	POST             /api/workspaces/{slug}/draft-to-issue/{draft_id}/
//
// Drafts are workspace-scoped and private to their creator: every lookup is
// scoped `created_by = <caller>`, so another user's draft returns 404 (not 403)
// — permissions.md §5.6, test_draft_to_issue_owner_scope_app.py.
package intake

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// DraftsList serves GET /api/workspaces/{slug}/draft-issues/: the caller's own
// drafts, as a cursor-paginated envelope of DraftIssueSerializer objects.
func (h *Handler) DraftsList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	params := dbx.Params{"workspace": scope.Workspace.Id, "user": user.Id}
	filter := "workspace = {:workspace} && created_by = {:user} && deleted_at = ''"
	if priority := e.Request.URL.Query().Get("priority"); priority != "" {
		filter += " && priority = {:priority}"
		params["priority"] = priority
	}

	perPage, cursor := serializers.ListParams(e, serializers.MaxPerPage)
	envelope, err := serializers.Paginate(h.App, serializers.ListQuery{
		Collection: "draft_issues",
		Filter:     filter,
		Params:     params,
		Sort:       "-created_at",
		PerPage:    perPage,
		Cursor:     cursor,
	}, func(row *core.Record) any {
		return serializers.DraftIssue(h.App, row)
	})
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, envelope)
}

// DraftsCreate serves POST /api/workspaces/{slug}/draft-issues/. Returns 201.
func (h *Handler) DraftsCreate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	if start, target := asString(body["start_date"]), asString(body["target_date"]); start != "" && target != "" && start > target {
		return serializers.BadRequest(e, "Start date cannot exceed target date")
	}

	// The draft's project must belong to the resolved workspace.
	var project *core.Record
	if projectUUID := asString(body["project_id"]); projectUUID != "" {
		project, _ = serializers.FindByUUID(h.App, "projects", projectUUID)
		if project == nil || project.GetString("workspace") != scope.Workspace.Id || project.GetString("deleted_at") != "" {
			return serializers.BadRequest(e, "Project is not valid please pass a valid project_id")
		}
	}

	collection, err := h.App.FindCollectionByNameOrId("draft_issues")
	if err != nil {
		return err
	}
	draft := core.NewRecord(collection)
	draft.Set("uuid", serializers.NewUUID())
	draft.Set("workspace", scope.Workspace.Id)
	draft.Set("created_by", user.Id)
	if project != nil {
		draft.Set("project", project.Id)
	}
	applyDraftFields(h, draft, body, project)
	if err := h.App.Save(draft); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	if project != nil {
		if err := h.replaceDraftRelations(draft, body, project, user); err != nil {
			return err
		}
	}
	return e.JSON(http.StatusCreated, serializers.DraftIssue(h.App, draft))
}

// DraftDetail serves GET /api/workspaces/{slug}/draft-issues/{pk}/.
func (h *Handler) DraftDetail(e *core.RequestEvent) error {
	draft, err := h.scopedDraft(e)
	if err != nil {
		return err
	}
	if draft == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	return e.JSON(http.StatusOK, serializers.DraftIssueDetail(h.App, draft))
}

// DraftUpdate serves PATCH /api/workspaces/{slug}/draft-issues/{pk}/. Returns 204.
func (h *Handler) DraftUpdate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	draft, err := h.scopedDraft(e)
	if err != nil {
		return err
	}
	if draft == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Issue not found"})
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	if start, target := asString(body["start_date"]), asString(body["target_date"]); start != "" && target != "" && start > target {
		return serializers.BadRequest(e, "Start date cannot exceed target date")
	}

	project := h.draftProject(draft)
	if projectUUID := asString(body["project_id"]); projectUUID != "" {
		if candidate, _ := serializers.FindByUUID(h.App, "projects", projectUUID); candidate != nil && candidate.GetString("workspace") == scope.Workspace.Id {
			project = candidate
			draft.Set("project", candidate.Id)
		}
	}
	applyDraftFields(h, draft, body, project)
	draft.Set("updated_by", user.Id)
	if err := h.App.Save(draft); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	if project != nil && (hasAnyKey(body, "assignee_ids", "label_ids", "cycle_id", "module_ids")) {
		if err := h.replaceDraftRelations(draft, body, project, user); err != nil {
			return err
		}
	}
	return e.NoContent(http.StatusNoContent)
}

// DraftDestroy serves DELETE /api/workspaces/{slug}/draft-issues/{pk}/.
func (h *Handler) DraftDestroy(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	draft, _ := serializers.FindByUUID(h.App, "draft_issues", e.Request.PathValue("pk"))
	if draft == nil || draft.GetString("workspace") != scope.Workspace.Id {
		return serializers.NotFound(e, "")
	}
	if scope.WorkspaceRole < middleware.RoleAdmin && draft.GetString("created_by") != user.Id {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	draft.Set("deleted_at", serializers.Now())
	if err := h.App.Save(draft); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// DraftToIssue serves POST /api/workspaces/{slug}/draft-to-issue/{draft_id}/:
// converts the caller's own draft into a real issue, then deletes the draft.
// Returns 201 with the created IssueSerializer body.
func (h *Handler) DraftToIssue(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	if scope.WorkspaceRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	// Drafts are private: scope the lookup to the owner so another user's draft
	// is indistinguishable from a missing one (404, not 403).
	draft, _ := serializers.FindByUUID(h.App, "draft_issues", e.Request.PathValue("draft_id"))
	if draft == nil || draft.GetString("workspace") != scope.Workspace.Id || draft.GetString("created_by") != user.Id {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	if draft.GetString("project") == "" {
		return serializers.BadRequest(e, "Project is required to create an issue.")
	}
	project, _ := h.App.FindRecordById("projects", draft.GetString("project"))
	if project == nil {
		return serializers.BadRequest(e, "Project is required to create an issue.")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}

	var created *core.Record
	err := h.App.RunInTransaction(func(txApp core.App) error {
		issue, err := h.createIssueForProject(txApp, project, user, body)
		if err != nil {
			return err
		}
		if cycleUUID := asString(body["cycle_id"]); cycleUUID != "" {
			if cycle, _ := serializers.FindByUUID(txApp, "cycles", cycleUUID); cycle != nil && cycle.GetString("project") == project.Id {
				if err := h.linkIssue(txApp, "cycle_issues", map[string]string{"cycle": cycle.Id}, issue, project, user); err != nil {
					return err
				}
			}
		}
		for _, moduleUUID := range stringList(body["module_ids"]) {
			if module, _ := serializers.FindByUUID(txApp, "modules", moduleUUID); module != nil && module.GetString("project") == project.Id {
				if err := h.linkIssue(txApp, "module_issues", map[string]string{"module": module.Id}, issue, project, user); err != nil {
					return err
				}
			}
		}
		draft.Set("deleted_at", serializers.Now())
		if err := txApp.Save(draft); err != nil {
			return err
		}
		created = issue
		return nil
	})
	if err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusCreated, serializers.Issue(h.App, created))
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// scopedDraft resolves the caller's own draft (created_by = caller) in the
// workspace. Returns (nil, nil) when absent so callers emit the 404 shape.
func (h *Handler) scopedDraft(e *core.RequestEvent) (*core.Record, error) {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return nil, serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return nil, serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	draft, _ := serializers.FindByUUID(h.App, "draft_issues", e.Request.PathValue("pk"))
	if draft == nil || draft.GetString("workspace") != scope.Workspace.Id || draft.GetString("deleted_at") != "" || draft.GetString("created_by") != user.Id {
		return nil, nil
	}
	return draft, nil
}

func (h *Handler) draftProject(draft *core.Record) *core.Record {
	if id := draft.GetString("project"); id != "" {
		project, _ := h.App.FindRecordById("projects", id)
		return project
	}
	return nil
}

// applyDraftFields copies the model fields the DraftIssueCreateSerializer
// accepts onto the record.
func applyDraftFields(h *Handler, draft *core.Record, body map[string]any, project *core.Record) {
	for _, field := range []string{"name", "description_json", "description_html", "description_binary", "priority", "start_date", "target_date", "completed_at", "sort_order", "external_source", "external_id"} {
		if v, ok := body[field]; ok {
			draft.Set(field, v)
		}
	}
	if _, ok := body["description_html"]; ok {
		draft.Set("description_stripped", stripTags(asString(body["description_html"])))
	}
	if project != nil {
		if stateUUID := asString(body["state_id"]); stateUUID != "" {
			if state, _ := serializers.FindByUUID(h.App, "states", stateUUID); state != nil && state.GetString("project") == project.Id {
				draft.Set("state", state.Id)
			}
		}
		if parentUUID := asString(body["parent_id"]); parentUUID != "" {
			if parent, _ := serializers.FindByUUID(h.App, "issues", parentUUID); parent != nil && parent.GetString("project") == project.Id {
				draft.Set("parent", parent.Id)
			}
		}
	}
}

// replaceDraftRelations rewrites the draft's assignee/label/module/cycle joins.
func (h *Handler) replaceDraftRelations(draft *core.Record, body map[string]any, project *core.Record, user *core.Record) error {
	assignees := stringList(body["assignee_ids"])
	if _, ok := body["assignee_ids"]; ok {
		if err := h.resetJoin("draft_issue_assignees", "draft_issue", draft); err != nil {
			return err
		}
		for _, userUUID := range assignees {
			member, _ := serializers.FindByUUID(h.App, "users", userUUID)
			if member == nil || !h.isEligibleProjectMember(project, member.Id) {
				continue
			}
			if err := h.createJoin("draft_issue_assignees", map[string]string{"assignee": member.Id}, draft, project, user); err != nil {
				return err
			}
		}
	}
	if _, ok := body["label_ids"]; ok {
		if err := h.resetJoin("draft_issue_labels", "draft_issue", draft); err != nil {
			return err
		}
		for _, labelUUID := range stringList(body["label_ids"]) {
			label, _ := serializers.FindByUUID(h.App, "labels", labelUUID)
			if label == nil || label.GetString("project") != project.Id {
				continue
			}
			if err := h.createJoin("draft_issue_labels", map[string]string{"label": label.Id}, draft, project, user); err != nil {
				return err
			}
		}
	}
	if _, ok := body["module_ids"]; ok {
		if err := h.resetJoin("draft_issue_modules", "draft_issue", draft); err != nil {
			return err
		}
		for _, moduleUUID := range stringList(body["module_ids"]) {
			module, _ := serializers.FindByUUID(h.App, "modules", moduleUUID)
			if module == nil || module.GetString("project") != project.Id {
				continue
			}
			if err := h.createJoin("draft_issue_modules", map[string]string{"module": module.Id}, draft, project, user); err != nil {
				return err
			}
		}
	}
	if _, ok := body["cycle_id"]; ok {
		if err := h.resetJoin("draft_issue_cycles", "draft_issue", draft); err != nil {
			return err
		}
		if cycleUUID := asString(body["cycle_id"]); cycleUUID != "" {
			if cycle, _ := serializers.FindByUUID(h.App, "cycles", cycleUUID); cycle != nil && cycle.GetString("project") == project.Id {
				if err := h.createJoin("draft_issue_cycles", map[string]string{"cycle": cycle.Id}, draft, project, user); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// resetJoin hard-deletes the existing join rows for a draft.
func (h *Handler) resetJoin(collection, draftField string, draft *core.Record) error {
	rows, err := h.App.FindRecordsByFilter(collection, draftField+" = {:draft}", "", 0, 0, dbx.Params{"draft": draft.Id})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := h.App.Delete(row); err != nil {
			return err
		}
	}
	return nil
}

// createJoin inserts one draft join row.
func (h *Handler) createJoin(collection string, extra map[string]string, draft *core.Record, project *core.Record, user *core.Record) error {
	col, err := h.App.FindCollectionByNameOrId(collection)
	if err != nil {
		return err
	}
	row := core.NewRecord(col)
	row.Set("uuid", serializers.NewUUID())
	row.Set("draft_issue", draft.Id)
	for field, value := range extra {
		row.Set(field, value)
	}
	row.Set("project", project.Id)
	row.Set("workspace", project.GetString("workspace"))
	if user != nil {
		row.Set("created_by", user.Id)
	}
	return h.App.Save(row)
}

// linkIssue inserts an issue↔cycle/module join row.
func (h *Handler) linkIssue(app core.App, collection string, extra map[string]string, issue *core.Record, project *core.Record, user *core.Record) error {
	col, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		return err
	}
	row := core.NewRecord(col)
	row.Set("uuid", serializers.NewUUID())
	row.Set("issue", issue.Id)
	for field, value := range extra {
		row.Set(field, value)
	}
	row.Set("project", project.Id)
	row.Set("workspace", project.GetString("workspace"))
	if user != nil {
		row.Set("created_by", user.Id)
	}
	return app.Save(row)
}

// isEligibleProjectMember reports whether the user is an active project member
// with role >= MEMBER.
func (h *Handler) isEligibleProjectMember(project *core.Record, userID string) bool {
	member, err := h.App.FindFirstRecordByFilter(
		"project_members",
		"project = {:project} && member = {:member} && is_active = true && deleted_at = ''",
		dbx.Params{"project": project.Id, "member": userID},
	)
	return err == nil && member != nil && member.GetInt("role") >= middleware.RoleMember
}

// createIssueForProject inserts an issue from a draft-to-issue payload,
// allocating the per-project sequence and default state.
func (h *Handler) createIssueForProject(app core.App, project *core.Record, user *core.Record, body map[string]any) (*core.Record, error) {
	name := strings.TrimSpace(asString(body["name"]))
	if name == "" {
		return nil, errNameRequired
	}
	collection, err := app.FindCollectionByNameOrId("issues")
	if err != nil {
		return nil, err
	}
	var maxSequence int
	if err := app.DB().
		NewQuery("SELECT COALESCE(MAX(sequence_id), 0) FROM issues WHERE project = {:project}").
		Bind(dbx.Params{"project": project.Id}).
		Row(&maxSequence); err != nil {
		return nil, err
	}
	stateID := resolveDefaultState(app, project, body)

	var maxSort sql.NullFloat64
	if err := app.DB().
		NewQuery("SELECT MAX(sort_order) FROM issues WHERE project = {:project} AND state = {:state} AND deleted_at = ''").
		Bind(dbx.Params{"project": project.Id, "state": stateID}).
		Row(&maxSort); err != nil {
		return nil, err
	}
	sortOrder := float64(65535)
	if maxSort.Valid {
		sortOrder = maxSort.Float64 + 10000
	}

	issue := core.NewRecord(collection)
	issue.Set("uuid", serializers.NewUUID())
	issue.Set("project", project.Id)
	issue.Set("workspace", project.GetString("workspace"))
	issue.Set("name", name)
	issue.Set("sequence_id", maxSequence+1)
	issue.Set("sort_order", sortOrder)
	issue.Set("created_by", user.Id)
	issue.Set("is_draft", false)
	if stateID != "" {
		issue.Set("state", stateID)
	}
	priority := asString(body["priority"])
	if priority == "" {
		priority = "none"
	}
	issue.Set("priority", priority)
	for _, field := range []string{"description_html", "description_json", "start_date", "target_date", "completed_at", "external_source", "external_id"} {
		if v, ok := body[field]; ok {
			issue.Set(field, v)
		}
	}
	issue.Set("description_stripped", stripTags(asString(body["description_html"])))
	if err := app.Save(issue); err != nil {
		return nil, err
	}
	return issue, nil
}

// resolveDefaultState picks the issue state for a draft conversion: an explicit
// state_id, else the project's default state.
func resolveDefaultState(app core.App, project *core.Record, body map[string]any) string {
	if stateUUID := asString(body["state_id"]); stateUUID != "" {
		if state, _ := serializers.FindByUUID(app, "states", stateUUID); state != nil && state.GetString("project") == project.Id {
			return state.Id
		}
	}
	if state, _ := app.FindFirstRecordByFilter("states", "project = {:project} && default = true && is_triage = false && deleted_at = ''", dbx.Params{"project": project.Id}); state != nil {
		return state.Id
	}
	return ""
}

var errNameRequired = errors.New("name is required")

func hasAnyKey(body map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, ok := body[key]; ok {
			return true
		}
	}
	return false
}

func stringList(raw any) []string {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
