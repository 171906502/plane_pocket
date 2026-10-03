// Package issues implements Plane's issue endpoints for a project:
//
//	GET/POST  /api/workspaces/{slug}/projects/{project_id}/issues/       (paginated)
//	GET       /api/workspaces/{slug}/projects/{project_id}/issues/list/  (plain array)
//	GET/PATCH/DELETE /api/workspaces/{slug}/projects/{project_id}/issues/{pk}/
//
// `{pk}` is a Plane UUID.
package issues

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Handler holds the app dependency for the issue routes.
type Handler struct {
	App core.App
}

// New returns an issue handler bound to app.
func New(app core.App) *Handler {
	return &Handler{App: app}
}

// List serves GET /api/workspaces/{slug}/projects/{project_id}/issues/
// (IssueViewSet.list) — the cursor-paginated envelope whose `results` are
// IssueSerializer objects.
func (h *Handler) List(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)

	filter := "project = {:project} && deleted_at = '' && archived_at = '' && is_draft = false && state.group != \"triage\""
	params := dbx.Params{"project": scope.Project.Id}

	// A guest without guest_view_all_features sees only issues they created
	// (permissions.md §5.1). This filter is mandatory on every list path.
	if scope.ProjectRole == middleware.RoleGuest && !scope.Project.GetBool("guest_view_all_features") && user != nil {
		filter += " && created_by = {:user}"
		params["user"] = user.Id
	}
	if priority := e.Request.URL.Query().Get("priority"); priority != "" {
		filter += " && priority = {:priority}"
		params["priority"] = priority
	}
	if stateID := e.Request.URL.Query().Get("state_id"); stateID != "" {
		if state, _ := serializers.FindByUUID(h.App, "states", stateID); state != nil {
			filter += " && state = {:state}"
			params["state"] = state.Id
		}
	}

	perPage, cursor := serializers.ListParams(e, serializers.MaxPerPage)
	envelope, err := serializers.Paginate(h.App, serializers.ListQuery{
		Collection: "issues",
		Filter:     filter,
		Params:     params,
		Sort:       orderBy(e.Request.URL.Query().Get("order_by")),
		PerPage:    perPage,
		Cursor:     cursor,
	}, func(issue *core.Record) any {
		return serializers.Issue(h.App, issue)
	})
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, envelope)
}

// PlainList serves GET /api/workspaces/{slug}/projects/{project_id}/issues/list/
// (IssueListEndpoint). It returns a plain, non-paginated array of issues
// selected by `?issues=<uuid>,<uuid>` — do not confuse it with the paginated
// collection endpoint above.
func (h *Handler) PlainList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}

	raw := e.Request.URL.Query().Get("issues")
	if raw == "" {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Issues are required"})
	}
	wanted := make([]string, 0)
	for _, id := range strings.Split(raw, ",") {
		if id != "" {
			wanted = append(wanted, id)
		}
	}
	if len(wanted) == 0 {
		return e.JSON(http.StatusOK, []any{})
	}

	// Resolve the requested Plane UUIDs to records, keeping Plane's ordering.
	results := make([]any, 0, len(wanted))
	for _, id := range wanted {
		issue, _ := serializers.FindByUUID(h.App, "issues", id)
		if issue == nil || issue.GetString("project") != scope.Project.Id || issue.GetString("deleted_at") != "" {
			continue
		}
		results = append(results, serializers.Issue(h.App, issue))
	}
	return e.JSON(http.StatusOK, results)
}

// Create serves POST /api/workspaces/{slug}/projects/{project_id}/issues/.
//
// `sequence_id` is allocated per project inside a single transaction: SQLite
// serialises writers, so reading MAX(sequence_id) and inserting in one
// transaction is the equivalent of Plane's `issue_sequences` advisory lock
// (spec/schema-spec.md §7.12). `sort_order` follows Plane's rule of
// "largest sort_order in (project, state) + 10000", defaulting to 65535.
func (h *Handler) Create(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	// issue.create: ADMIN or MEMBER only (permissions.md §3).
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}

	// Pre-allocate the map: BindBody's reflect-based unmarshal panics with
	// "assignment to entry in nil map" when the target map is nil and the
	// request carries form/multipart values.
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	name := strings.TrimSpace(asString(body["name"]))
	if name == "" {
		return serializers.Validation(e, "name", "This field is required.")
	}
	startDate := asString(body["start_date"])
	targetDate := asString(body["target_date"])
	if startDate != "" && targetDate != "" && startDate > targetDate {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Start date cannot exceed target date"})
	}

	collection, err := h.App.FindCollectionByNameOrId("issues")
	if err != nil {
		return err
	}

	var created *core.Record
	err = h.App.RunInTransaction(func(txApp core.App) error {
		project := scope.Project

		stateID, err := resolveState(txApp, project, body)
		if err != nil {
			return err
		}
		if stateID == "" {
			// No state and no default: Plane still creates the issue.
			stateID = ""
		}

		var maxSequence int
		if err := txApp.DB().
			NewQuery("SELECT COALESCE(MAX(sequence_id), 0) FROM issues WHERE project = {:project}").
			Bind(dbx.Params{"project": project.Id}).
			Row(&maxSequence); err != nil {
			return err
		}

		var maxSort sql.NullFloat64
		if err := txApp.DB().
			NewQuery("SELECT MAX(sort_order) FROM issues WHERE project = {:project} AND state = {:state} AND deleted_at = ''").
			Bind(dbx.Params{"project": project.Id, "state": stateID}).
			Row(&maxSort); err != nil {
			return err
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
		if stateID != "" {
			issue.Set("state", stateID)
		}
		issue.Set("priority", defaultPriority(asString(body["priority"])))
		for _, field := range []string{"description_html", "description_json", "external_source", "external_id", "is_draft", "start_date", "target_date", "completed_at", "archived_at", "point"} {
			if v, ok := body[field]; ok {
				issue.Set(field, v)
			}
		}
		issue.Set("description_stripped", stripTags(asString(body["description_html"])))
		if parent := asString(body["parent_id"]); parent != "" {
			if parentIssue, _ := serializers.FindByUUID(txApp, "issues", parent); parentIssue != nil && parentIssue.GetString("project") == project.Id {
				issue.Set("parent", parentIssue.Id)
			}
		}
		if err := txApp.Save(issue); err != nil {
			return err
		}

		assigneeIDs := uuidList(body, "assignee_ids", "assignees")
		if err := h.replaceAssignees(txApp, project, issue, assigneeIDs, user); err != nil {
			return err
		}
		labelIDs := uuidList(body, "label_ids", "labels")
		if err := h.replaceLabels(txApp, project, issue, labelIDs, user); err != nil {
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

// Detail serves GET /api/workspaces/{slug}/projects/{project_id}/issues/{pk}/.
func (h *Handler) Detail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	issue, err := h.findIssue(scope, e.Request.PathValue("pk"))
	if err != nil {
		return err
	}
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	return e.JSON(http.StatusOK, serializers.Issue(h.App, issue))
}

// Update serves PATCH /api/workspaces/{slug}/projects/{project_id}/issues/{pk}/.
// Plane returns 204 with no body (IssueViewSet.partial_update).
//
// Permission: project ADMIN/MEMBER, plus the creator short-circuit that lets a
// guest edit their own issue (permissions.md §3, issue.update).
func (h *Handler) Update(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	issue, err := h.findIssue(scope, e.Request.PathValue("pk"))
	if err != nil {
		return err
	}
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Issue not found"})
	}
	if !middleware.IsCreator(e, issue) && scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	user := middleware.CurrentUser(e)

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}

	if stateUUID := asString(body["state_id"]); stateUUID != "" {
		state, _ := serializers.FindByUUID(h.App, "states", stateUUID)
		if state == nil || state.GetString("project") != scope.Project.Id {
			return e.JSON(http.StatusBadRequest, map[string]any{"error": "State is not valid please pass a valid state_id"})
		}
		issue.Set("state", state.Id)
	}
	for _, field := range []string{"name", "description_html", "description_json", "priority", "start_date", "target_date", "completed_at", "archived_at", "is_draft", "point", "external_source", "external_id"} {
		if v, ok := body[field]; ok {
			issue.Set(field, v)
		}
	}
	if _, ok := body["description_html"]; ok {
		issue.Set("description_stripped", stripTags(asString(body["description_html"])))
	}
	if parent := asString(body["parent_id"]); parent != "" {
		if parentIssue, _ := serializers.FindByUUID(h.App, "issues", parent); parentIssue != nil && parentIssue.GetString("project") == scope.Project.Id {
			issue.Set("parent", parentIssue.Id)
		}
	}
	if user != nil {
		issue.Set("updated_by", user.Id)
	}
	if err := h.App.Save(issue); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}

	// Plane replaces the whole set when assignees/labels are supplied.
	if hasAnyKey(body, "assignee_ids", "assignees") {
		if err := h.replaceAssignees(h.App, scope.Project, issue, uuidList(body, "assignee_ids", "assignees"), user); err != nil {
			return err
		}
	}
	if hasAnyKey(body, "label_ids", "labels") {
		if err := h.replaceLabels(h.App, scope.Project, issue, uuidList(body, "label_ids", "labels"), user); err != nil {
			return err
		}
	}
	return e.NoContent(http.StatusNoContent)
}

// Delete serves DELETE /api/workspaces/{slug}/projects/{project_id}/issues/{pk}/.
// Permission: project ADMIN, plus the creator short-circuit (permissions.md §3,
// issue.delete). Plane soft-deletes.
func (h *Handler) Delete(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	issue, err := h.findIssue(scope, e.Request.PathValue("pk"))
	if err != nil {
		return err
	}
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	if !middleware.IsCreator(e, issue) && scope.ProjectRole < middleware.RoleAdmin {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	issue.Set("deleted_at", serializers.Now())
	if err := h.App.Save(issue); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// findIssue resolves a Plane UUID within the resolved project. Returns
// (nil, nil) when the issue is absent, so callers can emit Plane's 404 shape.
func (h *Handler) findIssue(scope *middleware.Scope, uuid string) (*core.Record, error) {
	issue, err := serializers.FindByUUID(h.App, "issues", uuid)
	if err != nil || issue == nil {
		return nil, err
	}
	if issue.GetString("project") != scope.Project.Id || issue.GetString("deleted_at") != "" {
		return nil, nil
	}
	return issue, nil
}

// replaceAssignees rewrites `issue_assignees` for an issue. Only active project
// members with role >= MEMBER are accepted, mirroring IssueCreateSerializer.
func (h *Handler) replaceAssignees(app core.App, project *core.Record, issue *core.Record, ids []string, user *core.Record) error {
	existing, err := app.FindRecordsByFilter(
		"issue_assignees",
		"issue = {:issue} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"issue": issue.Id},
	)
	if err != nil {
		return err
	}
	for _, row := range existing {
		if err := app.Delete(row); err != nil {
			return err
		}
	}
	if len(ids) == 0 {
		// Fall back to the project's default assignee when it is a valid member.
		if defaultAssignee := project.GetString("default_assignee"); defaultAssignee != "" {
			ids = []string{defaultAssignee}
		}
	}

	collection, err := app.FindCollectionByNameOrId("issue_assignees")
	if err != nil {
		return err
	}
	for _, userUUID := range ids {
		member, _ := serializers.FindByUUID(app, "users", userUUID)
		if member == nil {
			continue
		}
		if !h.isEligibleAssignee(app, project, member.Id) {
			continue
		}
		row := core.NewRecord(collection)
		row.Set("uuid", serializers.NewUUID())
		row.Set("issue", issue.Id)
		row.Set("assignee", member.Id)
		row.Set("project", project.Id)
		row.Set("workspace", project.GetString("workspace"))
		if user != nil {
			row.Set("created_by", user.Id)
		}
		if err := app.Save(row); err != nil {
			return err
		}
	}
	return nil
}

// replaceLabels rewrites `issue_labels` for an issue. Only labels belonging to
// the project are accepted.
func (h *Handler) replaceLabels(app core.App, project *core.Record, issue *core.Record, ids []string, user *core.Record) error {
	existing, err := app.FindRecordsByFilter(
		"issue_labels",
		"issue = {:issue} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"issue": issue.Id},
	)
	if err != nil {
		return err
	}
	for _, row := range existing {
		if err := app.Delete(row); err != nil {
			return err
		}
	}

	collection, err := app.FindCollectionByNameOrId("issue_labels")
	if err != nil {
		return err
	}
	for _, labelUUID := range ids {
		label, _ := serializers.FindByUUID(app, "labels", labelUUID)
		if label == nil || label.GetString("project") != project.Id {
			continue
		}
		row := core.NewRecord(collection)
		row.Set("uuid", serializers.NewUUID())
		row.Set("issue", issue.Id)
		row.Set("label", label.Id)
		row.Set("project", project.Id)
		row.Set("workspace", project.GetString("workspace"))
		if user != nil {
			row.Set("created_by", user.Id)
		}
		if err := app.Save(row); err != nil {
			return err
		}
	}
	return nil
}

// isEligibleAssignee reports whether the user is an active project member with
// role >= MEMBER.
func (h *Handler) isEligibleAssignee(app core.App, project *core.Record, userID string) bool {
	member, err := app.FindFirstRecordByFilter(
		"project_members",
		"project = {:project} && member = {:member} && is_active = true && deleted_at = ''",
		dbx.Params{"project": project.Id, "member": userID},
	)
	if err != nil || member == nil {
		return false
	}
	return member.GetInt("role") >= middleware.RoleMember
}

// resolveState picks the issue's state: an explicit `state_id` (validated
// against the project), else the project's default state, else the first
// non-triage state — mirroring Issue._ensure_default_state.
func resolveState(app core.App, project *core.Record, body map[string]any) (string, error) {
	if stateUUID := asString(body["state_id"]); stateUUID != "" {
		state, _ := serializers.FindByUUID(app, "states", stateUUID)
		if state == nil || state.GetString("project") != project.Id {
			return "", errInvalidState
		}
		return state.Id, nil
	}
	state, err := app.FindFirstRecordByFilter(
		"states",
		"project = {:project} && default = true && is_triage = false && deleted_at = ''",
		dbx.Params{"project": project.Id},
	)
	if err == nil && state != nil {
		return state.Id, nil
	}
	states, err := app.FindRecordsByFilter(
		"states",
		"project = {:project} && is_triage = false && deleted_at = ''",
		"sequence", 1, 0,
		dbx.Params{"project": project.Id},
	)
	if err != nil || len(states) == 0 {
		return "", nil
	}
	return states[0].Id, nil
}

type stateError struct{}

func (stateError) Error() string { return "State is not valid please pass a valid state_id" }

var errInvalidState = stateError{}

// orderBy maps Plane's `order_by` values onto PocketBase sort expressions.
func orderBy(param string) string {
	switch param {
	case "created_at":
		return "created_at"
	case "-created_at", "":
		return "-created_at"
	case "updated_at":
		return "updated_at"
	case "-updated_at":
		return "-updated_at"
	case "sort_order":
		return "sort_order"
	case "-sort_order":
		return "-sort_order"
	case "sequence_id":
		return "sequence_id"
	case "-sequence_id":
		return "-sequence_id"
	case "priority":
		return "priority"
	case "-priority":
		return "-priority"
	default:
		return "-created_at"
	}
}

func defaultPriority(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

// uuidList reads a list-valued field, accepting both the snake_case and the
// short Plane frontend spellings (`assignee_ids` / `assignees`).
func uuidList(body map[string]any, keys ...string) []string {
	for _, key := range keys {
		raw, ok := body[key]
		if !ok {
			continue
		}
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
	return nil
}

func hasAnyKey(body map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, ok := body[key]; ok {
			return true
		}
	}
	return false
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// stripTags removes HTML tags so `description_stripped` matches Django's
// django.utils.html.strip_tags over `description_html`.
func stripTags(html string) any {
	if html == "" {
		return nil
	}
	var b strings.Builder
	inTag := false
	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}
