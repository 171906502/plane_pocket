// Package intake implements Plane's intake/inbox endpoints for a project:
//
//	GET/POST        …/intakes/          (and the /inboxes/ alias)
//	GET/PATCH/DELETE …/intakes/{pk}/    (and /inboxes/{pk}/)
//	GET/POST        …/intake-issues/    (and /inbox-issues/)
//	GET/PATCH/DELETE …/intake-issues/{pk}/  (and /inbox-issues/{pk}/)
//	GET             …/intake-state/
//
// `{pk}` on the intake-issue detail routes is the underlying **issue UUID**
// (Django resolves IntakeIssue by `issue_id=pk`), not the intake-issue row id.
package intake

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/filters"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// csvValues splits a comma-separated query value into trimmed non-empty parts.
func csvValues(raw string) []string {
	out := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// earliestDay parses the values as dates and returns the earliest one — the
// widest lower bound of a multi-selected "past N days" window.
func earliestDay(values []string) (time.Time, bool) {
	var earliest time.Time
	found := false
	for _, value := range values {
		if len(value) < 10 {
			continue
		}
		day, err := time.Parse("2006-01-02", value[:10])
		if err != nil {
			continue
		}
		if !found || day.Before(earliest) {
			earliest = day
			found = true
		}
	}
	return earliest, found
}

// intakeOrderBy maps the inbox sorting wire values (`-issue__created_at` —
// Django ORM traversal with `__`) onto PocketBase relation sort syntax
// (`-issue.created_at`). Unknown values keep the default.
func intakeOrderBy(raw string) string {
	switch raw {
	case "-issue__created_at", "issue__created_at":
		return strings.ReplaceAll(raw, "__", ".")
	case "-issue__updated_at", "issue__updated_at":
		return strings.ReplaceAll(raw, "__", ".")
	case "-issue__sequence_id", "issue__sequence_id":
		return strings.ReplaceAll(raw, "__", ".")
	case "created_at", "-created_at", "updated_at", "-updated_at":
		return raw
	default:
		return "-created_at"
	}
}

// Handler holds the app dependency for the intake routes.
type Handler struct {
	App core.App
}

// New returns an intake handler bound to app.
func New(app core.App) *Handler {
	return &Handler{App: app}
}

// IntakesList serves GET …/intakes/ (IntakeViewSet.list). Plane returns a
// single IntakeSerializer object — the project's first intake — not an array.
func (h *Handler) IntakesList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	user := middleware.CurrentUser(e)
	intake, err := h.ensureDefaultIntake(scope, user)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, serializers.Intake(h.App, intake, serializers.PendingIntakeCount(h.App, intake)))
}

// IntakesCreate serves POST …/intakes/ (IntakeViewSet.create). ADMIN or MEMBER.
func (h *Handler) IntakesCreate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	name := strings.TrimSpace(asString(body["name"]))
	if name == "" {
		return serializers.Validation(e, "name", "This field is required.")
	}
	collection, err := h.App.FindCollectionByNameOrId("intakes")
	if err != nil {
		return err
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("project", scope.Project.Id)
	record.Set("workspace", scope.Workspace.Id)
	record.Set("name", name)
	record.Set("created_by", user.Id)
	for _, field := range []string{"description", "view_props", "logo_props", "is_default"} {
		if v, ok := body[field]; ok {
			record.Set(field, v)
		}
	}
	if err := h.App.Save(record); err != nil {
		return serializers.Validation(e, "name", err.Error())
	}
	return e.JSON(http.StatusCreated, serializers.Intake(h.App, record, 0))
}

// IntakeDetail serves GET …/intakes/{pk}/.
func (h *Handler) IntakeDetail(e *core.RequestEvent) error {
	_, intake, err := h.scopedIntake(e)
	if err != nil || intake == nil {
		return err
	}
	return e.JSON(http.StatusOK, serializers.Intake(h.App, intake, serializers.PendingIntakeCount(h.App, intake)))
}

// IntakeUpdate serves PATCH …/intakes/{pk}/.
func (h *Handler) IntakeUpdate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	_, intake, err := h.scopedIntake(e)
	if err != nil || intake == nil {
		return err
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	for _, field := range []string{"name", "description", "view_props", "logo_props", "is_default"} {
		if v, ok := body[field]; ok {
			intake.Set(field, v)
		}
	}
	intake.Set("updated_by", user.Id)
	if err := h.App.Save(intake); err != nil {
		return serializers.Validation(e, "name", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.Intake(h.App, intake, serializers.PendingIntakeCount(h.App, intake)))
}

// IntakeDestroy serves DELETE …/intakes/{pk}/. The default intake cannot be
// deleted (IntakeViewSet.destroy).
func (h *Handler) IntakeDestroy(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	_, intake, err := h.scopedIntake(e)
	if err != nil || intake == nil {
		return err
	}
	if intake.GetBool("is_default") {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "You cannot delete the default intake"})
	}
	intake.Set("deleted_at", serializers.Now())
	if err := h.App.Save(intake); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// IntakeState serves GET …/intake-state/ (IntakeStateEndpoint): the project's
// triage state as a StateSerializer.
func (h *Handler) IntakeState(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	state, _ := h.triageState(scope)
	if state == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Triage state not found"})
	}
	return e.JSON(http.StatusOK, serializers.State(h.App, state, h.stateOrder(scope, state)))
}

// IssuesList serves GET …/intake-issues/ (IntakeIssueViewSet.list): the
// cursor-paginated envelope whose results are IntakeIssueSerializer objects.
func (h *Handler) IssuesList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	intake, err := h.ensureDefaultIntake(scope, middleware.CurrentUser(e))
	if err != nil {
		return err
	}

	params := dbx.Params{"intake": intake.Id, "project": scope.Project.Id}
	filter := "intake = {:intake} && project = {:project} && deleted_at = ''"

	// status is a comma-separated list, defaulting to PENDING (-2); "null" is
	// dropped rather than matched.
	rawStatus := e.Request.URL.Query().Get("status")
	if rawStatus == "" {
		rawStatus = "-2"
	}
	statuses := make([]int, 0)
	for _, part := range strings.Split(rawStatus, ",") {
		part = strings.TrimSpace(part)
		if part == "" || part == "null" {
			continue
		}
		if n, err := strconv.Atoi(part); err == nil {
			statuses = append(statuses, n)
		}
	}
	if len(statuses) > 0 {
		filter += " && " + intFilter("status", statuses, params)
	}

	// The inbox filter bar sends comma-separated multi-values (project-inbox
	// store `inboxIssueQueryParams`): priority/state/created_by on the related
	// issue, assignees/labels through the issue bridge tables, and past-window
	// dates on the submission itself.
	query := e.Request.URL.Query()
	if clause := filters.InPredicate("issue.priority", csvValues(query.Get("priority")), params); clause != "" {
		filter += " && " + clause
	}
	if ids := filters.ResolveIDs(h.App, "states", csvValues(query.Get("state"))); len(ids) > 0 {
		filter += " && " + filters.InPredicate("issue.state", ids, params)
	}
	if ids := filters.ResolveIDs(h.App, "users", csvValues(query.Get("created_by"))); len(ids) > 0 {
		filter += " && " + filters.InPredicate("issue.created_by", ids, params)
	}
	if ids := filters.BridgeIssueIDs(h.App, "issue_assignees", "assignee", "users", csvValues(query.Get("assignees"))); len(ids) > 0 {
		filter += " && " + filters.InPredicate("issue", ids, params)
	}
	if ids := filters.BridgeIssueIDs(h.App, "issue_labels", "label", "labels", csvValues(query.Get("labels"))); len(ids) > 0 {
		filter += " && " + filters.InPredicate("issue", ids, params)
	}
	// Date windows: the UI offers "past 1 week / 1 month…" multi-selects, so
	// the joined dates are lower bounds and the widest (earliest) one wins.
	for _, key := range []string{"created_at", "updated_at"} {
		if lower, ok := earliestDay(csvValues(query.Get(key))); ok {
			paramKey := "since" + key
			params[paramKey] = lower.Format("2006-01-02 00:00:00.000Z")
			filter += " && " + key + " >= {:" + paramKey + "}"
		}
	}

	// A guest without guest_view_all_features sees only their own submissions.
	user := middleware.CurrentUser(e)
	if scope.ProjectRole == middleware.RoleGuest && !scope.Project.GetBool("guest_view_all_features") && user != nil {
		filter += " && created_by = {:user}"
		params["user"] = user.Id
	}

	perPage, cursor := serializers.ListParams(e, serializers.MaxPerPage)
	envelope, err := serializers.Paginate(h.App, serializers.ListQuery{
		Collection: "intake_issues",
		Filter:     filter,
		Params:     params,
		Sort:       intakeOrderBy(query.Get("order_by")),
		PerPage:    perPage,
		Cursor:     cursor,
	}, func(row *core.Record) any {
		return serializers.IntakeIssue(h.App, row)
	})
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, envelope)
}

// IssuesCreate serves POST …/intake-issues/ (IntakeIssueViewSet.create). The
// body nests the issue under `issue`; the issue is created in the project's
// triage state and linked to the intake. Returns 200 with the detail serializer
// (Plane does not use 201 here).
func (h *Handler) IssuesCreate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
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
	issueBody, _ := body["issue"].(map[string]any)
	name := strings.TrimSpace(asString(issueBody["name"]))
	if name == "" {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Name is required"})
	}
	priority := asString(issueBody["priority"])
	if priority == "" {
		priority = "none"
	}
	switch priority {
	case "low", "medium", "high", "urgent", "none":
	default:
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Invalid priority"})
	}

	intake, err := h.ensureDefaultIntake(scope, user)
	if err != nil {
		return err
	}
	state, err := h.ensureTriageState(scope, user)
	if err != nil {
		return err
	}

	var created *core.Record
	err = h.App.RunInTransaction(func(txApp core.App) error {
		issue, err := h.createIssue(txApp, scope, user, issueBody, name, priority, state.Id)
		if err != nil {
			return err
		}
		rowCollection, err := txApp.FindCollectionByNameOrId("intake_issues")
		if err != nil {
			return err
		}
		row := core.NewRecord(rowCollection)
		row.Set("uuid", serializers.NewUUID())
		row.Set("intake", intake.Id)
		row.Set("issue", issue.Id)
		row.Set("project", scope.Project.Id)
		row.Set("workspace", scope.Workspace.Id)
		row.Set("status", -2)
		row.Set("source", "IN_APP")
		row.Set("created_by", user.Id)
		if err := txApp.Save(row); err != nil {
			return err
		}
		created = row
		return nil
	})
	if err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.IntakeIssueDetail(h.App, created))
}

// IssueDetail serves GET …/intake-issues/{pk}/ (pk is the issue UUID).
func (h *Handler) IssueDetail(e *core.RequestEvent) error {
	scope, row, err := h.scopedIntakeIssue(e)
	if err != nil || row == nil {
		return err
	}
	issue, _ := h.App.FindRecordById("issues", row.GetString("issue"))
	user := middleware.CurrentUser(e)
	if scope.ProjectRole == middleware.RoleGuest && !scope.Project.GetBool("guest_view_all_features") {
		if issue == nil || user == nil || issue.GetString("created_by") != user.Id {
			return e.JSON(http.StatusForbidden, map[string]any{"error": "You are not allowed to view this issue"})
		}
	}
	return e.JSON(http.StatusOK, serializers.IntakeIssueDetail(h.App, row))
}

// IssueUpdate serves PATCH …/intake-issues/{pk}/ (IntakeIssueViewSet.partial_update).
// Allowed for project admins or the issue's creator; only admins may change the
// intake-level fields (status/snooze/duplicate/source).
func (h *Handler) IssueUpdate(e *core.RequestEvent) error {
	scope, row, err := h.scopedIntakeIssue(e)
	if err != nil || row == nil {
		return err
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	issue, _ := h.App.FindRecordById("issues", row.GetString("issue"))
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleAdmin) && !middleware.IsCreator(e, issue) {
		return e.JSON(http.StatusForbidden, map[string]any{"error": "Only admin or creator can update the intake work items"})
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}

	// Issue fields nested under `issue`.
	if issueData, ok := body["issue"].(map[string]any); ok && issue != nil {
		// A guest/creator may only touch the description-bearing fields.
		if scope.ProjectRole <= middleware.RoleGuest {
			issueData = map[string]any{
				"name":             firstNonEmpty(asString(issueData["name"]), issue.GetString("name")),
				"description_html": firstNonEmpty(asString(issueData["description_html"]), issue.GetString("description_html")),
				"description_json": issueData["description_json"],
			}
		}
		if stateUUID := asString(issueData["state_id"]); stateUUID != "" {
			if state, _ := serializers.FindByUUID(h.App, "states", stateUUID); state != nil && state.GetString("project") == scope.Project.Id {
				issue.Set("state", state.Id)
			}
		}
		for _, field := range []string{"name", "priority", "start_date", "target_date", "description_html", "description_json"} {
			if v, ok := issueData[field]; ok {
				issue.Set(field, v)
			}
		}
		if _, ok := issueData["description_html"]; ok {
			issue.Set("description_stripped", stripTags(asString(issueData["description_html"])))
		}
		issue.Set("updated_by", user.Id)
		if err := h.App.Save(issue); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
	}

	// Intake-level fields: admins only.
	if scope.ProjectRole > middleware.RoleMember {
		for _, field := range []string{"status", "snoozed_till", "source"} {
			if v, ok := body[field]; ok {
				row.Set(field, v)
			}
		}
		if v, ok := body["duplicate_to"]; ok {
			if dupUUID := asString(v); dupUUID != "" {
				if dup, _ := serializers.FindByUUID(h.App, "issues", dupUUID); dup != nil && dup.GetString("project") == scope.Project.Id {
					row.Set("duplicate_to", dup.Id)
				}
			} else {
				row.Set("duplicate_to", "")
			}
		}
		// Accepting a triage issue moves it to the project's default state.
		if status := intValue(body["status"], -999); status == 1 && issue != nil && issue.GetString("state") != "" {
			if state, _ := h.App.FindRecordById("states", issue.GetString("state")); state != nil && state.GetString("group") == "triage" {
				if defaultState, _ := h.App.FindFirstRecordByFilter("states", "project = {:project} && default = true && deleted_at = ''", dbx.Params{"project": scope.Project.Id}); defaultState != nil {
					issue.Set("state", defaultState.Id)
					_ = h.App.Save(issue)
				}
			}
		}
		row.Set("updated_by", user.Id)
		if err := h.App.Save(row); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
	}

	return e.JSON(http.StatusOK, serializers.IntakeIssueDetail(h.App, row))
}

// IssueDelete serves DELETE …/intake-issues/{pk}/. Allowed for project admins or
// the issue's creator. Pending/rejected/snoozed/duplicate issues also delete the
// underlying issue (IntakeIssueViewSet.destroy).
func (h *Handler) IssueDelete(e *core.RequestEvent) error {
	_, row, err := h.scopedIntakeIssue(e)
	if err != nil || row == nil {
		return err
	}
	issue, _ := h.App.FindRecordById("issues", row.GetString("issue"))
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleAdmin) && !middleware.IsCreator(e, issue) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	switch row.GetInt("status") {
	case -2, -1, 0, 2:
		if issue != nil {
			issue.Set("deleted_at", serializers.Now())
			if err := h.App.Save(issue); err != nil {
				return err
			}
		}
	}
	row.Set("deleted_at", serializers.Now())
	if err := h.App.Save(row); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// defaultIntake returns the project's first intake (Django uses `.first()`),
// or (nil, nil) when the project has none. PocketBase reports "no rows" as an
// error, so it is normalised to a nil record rather than propagated.
func (h *Handler) defaultIntake(scope *middleware.Scope) (*core.Record, error) {
	intake, err := h.App.FindFirstRecordByFilter(
		"intakes",
		"project = {:project} && workspace = {:workspace} && deleted_at = ''",
		dbx.Params{"project": scope.Project.Id, "workspace": scope.Workspace.Id},
	)
	if err != nil {
		return nil, nil
	}
	return intake, nil
}

// ensureDefaultIntake returns the project's default intake, creating it on
// first access. Plane seeds this row from a project-creation signal; because the
// project handler is outside this domain, the intake domain seeds it lazily so
// the inbox endpoints have a row to work with.
func (h *Handler) ensureDefaultIntake(scope *middleware.Scope, user *core.Record) (*core.Record, error) {
	if intake, _ := h.defaultIntake(scope); intake != nil {
		return intake, nil
	}
	collection, err := h.App.FindCollectionByNameOrId("intakes")
	if err != nil {
		return nil, err
	}
	intake := core.NewRecord(collection)
	intake.Set("uuid", serializers.NewUUID())
	intake.Set("project", scope.Project.Id)
	intake.Set("workspace", scope.Workspace.Id)
	intake.Set("name", "Intake")
	intake.Set("is_default", true)
	if user != nil {
		intake.Set("created_by", user.Id)
	}
	if err := h.App.Save(intake); err != nil {
		return nil, err
	}
	return intake, nil
}

// scopedIntake resolves an intake by Plane UUID within the URL project.
func (h *Handler) scopedIntake(e *core.RequestEvent) (*middleware.Scope, *core.Record, error) {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return nil, nil, serializers.NotFound(e, "")
	}
	intake, _ := serializers.FindByUUID(h.App, "intakes", e.Request.PathValue("pk"))
	if intake == nil || intake.GetString("project") != scope.Project.Id || intake.GetString("deleted_at") != "" {
		return scope, nil, serializers.NotFound(e, "")
	}
	return scope, intake, nil
}

// scopedIntakeIssue resolves the intake issue whose underlying issue UUID is
// the route's `pk`.
func (h *Handler) scopedIntakeIssue(e *core.RequestEvent) (*middleware.Scope, *core.Record, error) {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return nil, nil, serializers.NotFound(e, "")
	}
	issue, _ := serializers.FindByUUID(h.App, "issues", e.Request.PathValue("pk"))
	if issue == nil || issue.GetString("project") != scope.Project.Id || issue.GetString("deleted_at") != "" {
		return scope, nil, serializers.NotFound(e, "")
	}
	intake, err := h.defaultIntake(scope)
	if err != nil || intake == nil {
		return scope, nil, serializers.NotFound(e, "")
	}
	row, _ := h.App.FindFirstRecordByFilter(
		"intake_issues",
		"issue = {:issue} && intake = {:intake} && project = {:project} && deleted_at = ''",
		dbx.Params{"issue": issue.Id, "intake": intake.Id, "project": scope.Project.Id},
	)
	if row == nil {
		return scope, nil, serializers.NotFound(e, "")
	}
	return scope, row, nil
}

// triageState returns the project's triage state, if any. Plane's
// State.triage_objects filters on `group = "triage"` (the seeded Triage state
// carries is_triage=false), so match on the group rather than the flag.
func (h *Handler) triageState(scope *middleware.Scope) (*core.Record, error) {
	return h.App.FindFirstRecordByFilter(
		"states",
		"project = {:project} && group = 'triage' && deleted_at = ''",
		dbx.Params{"project": scope.Project.Id},
	)
}

// ensureTriageState returns the project's triage state, creating Plane's default
// one when the project has none (IntakeIssueViewSet.create).
func (h *Handler) ensureTriageState(scope *middleware.Scope, user *core.Record) (*core.Record, error) {
	if state, err := h.triageState(scope); err == nil && state != nil {
		return state, nil
	}
	collection, err := h.App.FindCollectionByNameOrId("states")
	if err != nil {
		return nil, err
	}
	state := core.NewRecord(collection)
	state.Set("uuid", serializers.NewUUID())
	state.Set("project", scope.Project.Id)
	state.Set("workspace", scope.Workspace.Id)
	state.Set("name", "Triage")
	state.Set("group", "triage")
	state.Set("color", "#4E5355")
	state.Set("sequence", 65000)
	state.Set("is_triage", true)
	state.Set("default", false)
	if user != nil {
		state.Set("created_by", user.Id)
	}
	if err := h.App.Save(state); err != nil {
		return nil, err
	}
	return state, nil
}

// stateOrder mirrors StateViewSet's computed `order`: the position of the state
// within its group, ordered by sequence.
func (h *Handler) stateOrder(scope *middleware.Scope, state *core.Record) float64 {
	states, err := h.App.FindRecordsByFilter(
		"states",
		"project = {:project} && group = {:group} && deleted_at = ''",
		"sequence", 0, 0,
		dbx.Params{"project": scope.Project.Id, "group": state.GetString("group")},
	)
	if err != nil {
		return 0
	}
	for i, s := range states {
		if s.Id == state.Id {
			return float64(i)
		}
	}
	return 0
}

// createIssue inserts an issue in the triage state, allocating the per-project
// sequence and the (project, state) sort_order, mirroring the issues handler.
func (h *Handler) createIssue(app core.App, scope *middleware.Scope, user *core.Record, issueBody map[string]any, name, priority, stateID string) (*core.Record, error) {
	collection, err := app.FindCollectionByNameOrId("issues")
	if err != nil {
		return nil, err
	}
	var maxSequence int
	if err := app.DB().
		NewQuery("SELECT COALESCE(MAX(sequence_id), 0) FROM issues WHERE project = {:project}").
		Bind(dbx.Params{"project": scope.Project.Id}).
		Row(&maxSequence); err != nil {
		return nil, err
	}
	var maxSort sql.NullFloat64
	if err := app.DB().
		NewQuery("SELECT MAX(sort_order) FROM issues WHERE project = {:project} AND state = {:state} AND deleted_at = ''").
		Bind(dbx.Params{"project": scope.Project.Id, "state": stateID}).
		Row(&maxSort); err != nil {
		return nil, err
	}
	sortOrder := float64(65535)
	if maxSort.Valid {
		sortOrder = maxSort.Float64 + 10000
	}

	issue := core.NewRecord(collection)
	issue.Set("uuid", serializers.NewUUID())
	issue.Set("project", scope.Project.Id)
	issue.Set("workspace", scope.Workspace.Id)
	issue.Set("name", name)
	issue.Set("sequence_id", maxSequence+1)
	issue.Set("sort_order", sortOrder)
	issue.Set("created_by", user.Id)
	if stateID != "" {
		issue.Set("state", stateID)
	}
	issue.Set("priority", priority)
	issue.Set("is_draft", false)
	for _, field := range []string{"description_html", "description_json"} {
		if v, ok := issueBody[field]; ok {
			issue.Set(field, v)
		}
	}
	issue.Set("description_stripped", stripTags(asString(issueBody["description_html"])))
	if err := app.Save(issue); err != nil {
		return nil, err
	}
	return issue, nil
}

// intFilter builds a PocketBase "col = {a} || col = {b}" integer expression.
func intFilter(column string, values []int, params dbx.Params) string {
	parts := make([]string, 0, len(values))
	for i, v := range values {
		key := "s" + strconv.Itoa(i)
		parts = append(parts, column+" = {:"+key+"}")
		params[key] = v
	}
	return "(" + strings.Join(parts, " || ") + ")"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func intValue(v any, def int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return def
}

// stripTags removes HTML tags so `description_stripped` matches Django's
// django.utils.html.strip_tags over `description_html`.
func stripTags(html string) string {
	if html == "" {
		return ""
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
