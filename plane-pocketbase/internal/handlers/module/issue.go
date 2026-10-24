package module

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/filters"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// orValues turns a comma-separated list of literal (non-UUID) values — the wire
// form of Django's MultipleChoiceFilter — into `(col = {:k0} || col = {:k1} …)`.
// Blank entries are dropped; an all-blank input yields "" (no predicate).
func orValues(column, raw string, params dbx.Params) string {
	parts := make([]string, 0)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := "v" + strconv.Itoa(len(parts))
		parts = append(parts, column+" = {:"+key+"}")
		params[key] = item
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, " || ") + ")"
}

// IssuesList serves GET …/modules/{module_id}/issues/ (ModuleIssueViewSet.list):
// the cursor-paginated issue envelope. ADMIN/MEMBER only.
func (h *Handler) IssuesList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	record, err := h.findModule(scope, e.Request.PathValue("module_id"))
	if err != nil {
		return err
	}
	if record == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Module not found"})
	}

	ids := h.bridgeIssueIDs(record.Id)
	params := dbx.Params{}
	filter := "deleted_at = '' && archived_at = '' && is_draft = false && " + orIDFilter("id", "i", ids, params)
	// MultipleChoiceFilter semantics: comma-separated values on the wire.
	if raw := e.Request.URL.Query().Get("state_id"); raw != "" {
		stateIDs := make([]string, 0)
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if state, _ := serializers.FindByUUID(h.App, "states", part); state != nil {
				stateIDs = append(stateIDs, state.Id)
			}
		}
		if len(stateIDs) > 0 {
			filter += " && " + orIDFilter("state", "st", stateIDs, params)
		}
	}
	if raw := e.Request.URL.Query().Get("priority"); raw != "" {
		if clause := orValues("priority", raw, params); clause != "" {
			filter += " && " + clause
		}
	}
	// Rich filters: the shared issue-filter helper serialises the expression
	// tree into the `filters` query param for every issue board.
	filter = filters.Apply(h.App, filter, params, scope.Project.Id, e.Request.URL.Query().Get("filters"))

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

// IssuesCreate serves POST …/modules/{module_id}/issues/ (body
// `{issues: [<uuid>…]}`) — attach issues to the module (create_module_issues).
func (h *Handler) IssuesCreate(e *core.RequestEvent) error {
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
	record, err := h.findModule(scope, e.Request.PathValue("module_id"))
	if err != nil {
		return err
	}
	if record == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Module not found"})
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	requested := uuidList(body, "issues")
	if len(requested) == 0 {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Issues are required"})
	}

	collection, err := h.App.FindCollectionByNameOrId("module_issues")
	if err != nil {
		return err
	}
	err = h.App.RunInTransaction(func(txApp core.App) error {
		for _, issueUUID := range requested {
			issue, _ := serializers.FindByUUID(txApp, "issues", issueUUID)
			if issue == nil || issue.GetString("project") != scope.Project.Id || issue.GetString("deleted_at") != "" {
				continue
			}
			existing, _ := txApp.FindFirstRecordByFilter(
				"module_issues",
				"module = {:module} && issue = {:issue}",
				dbx.Params{"module": record.Id, "issue": issue.Id},
			)
			if existing != nil {
				if existing.GetString("deleted_at") == "" {
					continue
				}
				// Re-link a previously removed issue rather than duplicating.
				existing.Set("deleted_at", nil)
				existing.Set("updated_by", user.Id)
				if err := txApp.Save(existing); err != nil {
					return err
				}
				continue
			}
			link := core.NewRecord(collection)
			link.Set("uuid", serializers.NewUUID())
			link.Set("module", record.Id)
			link.Set("issue", issue.Id)
			link.Set("project", scope.Project.Id)
			link.Set("workspace", scope.Workspace.Id)
			link.Set("created_by", user.Id)
			if err := txApp.Save(link); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusCreated, map[string]any{"message": "success"})
}

// IssueModulesCreate serves POST …/issues/{issue_id}/modules/ (body
// `{modules: […], removed_modules: […]}`) — ModuleIssueViewSet.create_issue_modules.
func (h *Handler) IssueModulesCreate(e *core.RequestEvent) error {
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
	issue, _ := serializers.FindByUUID(h.App, "issues", e.Request.PathValue("issue_id"))
	if issue == nil || issue.GetString("project") != scope.Project.Id || issue.GetString("deleted_at") != "" {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Issue not found"})
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	modules := uuidList(body, "modules")
	removed := uuidList(body, "removed_modules")

	collection, err := h.App.FindCollectionByNameOrId("module_issues")
	if err != nil {
		return err
	}
	err = h.App.RunInTransaction(func(txApp core.App) error {
		for _, moduleUUID := range modules {
			module, _ := serializers.FindByUUID(txApp, "modules", moduleUUID)
			if module == nil || module.GetString("project") != scope.Project.Id || module.GetString("deleted_at") != "" {
				continue
			}
			existing, _ := txApp.FindFirstRecordByFilter(
				"module_issues",
				"module = {:module} && issue = {:issue}",
				dbx.Params{"module": module.Id, "issue": issue.Id},
			)
			if existing != nil {
				if existing.GetString("deleted_at") == "" {
					continue
				}
				existing.Set("deleted_at", nil)
				existing.Set("updated_by", user.Id)
				if err := txApp.Save(existing); err != nil {
					return err
				}
				continue
			}
			link := core.NewRecord(collection)
			link.Set("uuid", serializers.NewUUID())
			link.Set("module", module.Id)
			link.Set("issue", issue.Id)
			link.Set("project", scope.Project.Id)
			link.Set("workspace", scope.Workspace.Id)
			link.Set("created_by", user.Id)
			if err := txApp.Save(link); err != nil {
				return err
			}
		}
		for _, moduleUUID := range removed {
			module, _ := serializers.FindByUUID(txApp, "modules", moduleUUID)
			if module == nil {
				continue
			}
			existing, _ := txApp.FindFirstRecordByFilter(
				"module_issues",
				"module = {:module} && issue = {:issue} && deleted_at = ''",
				dbx.Params{"module": module.Id, "issue": issue.Id},
			)
			if existing == nil {
				continue
			}
			existing.Set("deleted_at", serializers.Now())
			if err := txApp.Save(existing); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusCreated, map[string]any{"message": "success"})
}

// IssueDetail serves GET …/modules/{module_id}/issues/{issue_id}/.
func (h *Handler) IssueDetail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	link, issue, module, err := h.findModuleIssue(scope, e.Request.PathValue("module_id"), e.Request.PathValue("issue_id"))
	if err != nil {
		return err
	}
	if link == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	return e.JSON(http.StatusOK, h.serializeModuleIssue(link, issue, module))
}

// IssueUpdate serves PUT/PATCH …/modules/{module_id}/issues/{issue_id}/ — a
// validated no-op echoing the current link.
func (h *Handler) IssueUpdate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	link, issue, module, err := h.findModuleIssue(scope, e.Request.PathValue("module_id"), e.Request.PathValue("issue_id"))
	if err != nil {
		return err
	}
	if link == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	return e.JSON(http.StatusOK, h.serializeModuleIssue(link, issue, module))
}

// IssueDelete serves DELETE …/modules/{module_id}/issues/{issue_id}/:
// soft-delete the bridge row (idempotent — a repeat delete is a 204).
func (h *Handler) IssueDelete(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	link, _, _, err := h.findModuleIssue(scope, e.Request.PathValue("module_id"), e.Request.PathValue("issue_id"))
	if err != nil {
		return err
	}
	if link == nil {
		return e.NoContent(http.StatusNoContent)
	}
	link.Set("deleted_at", serializers.Now())
	if err := h.App.Save(link); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// findModuleIssue resolves the bridge row, its issue and its module.
func (h *Handler) findModuleIssue(scope *middleware.Scope, moduleUUID, issueUUID string) (*core.Record, *core.Record, *core.Record, error) {
	module, err := h.findModule(scope, moduleUUID)
	if err != nil || module == nil {
		return nil, nil, nil, err
	}
	issue, _ := serializers.FindByUUID(h.App, "issues", issueUUID)
	if issue == nil || issue.GetString("project") != scope.Project.Id || issue.GetString("deleted_at") != "" {
		return nil, nil, nil, nil
	}
	link, _ := h.App.FindFirstRecordByFilter(
		"module_issues",
		"module = {:module} && issue = {:issue} && deleted_at = ''",
		dbx.Params{"module": module.Id, "issue": issue.Id},
	)
	if link == nil {
		return nil, nil, nil, nil
	}
	return link, issue, module, nil
}

// serializeModuleIssue builds the ModuleIssueSerializer shape
// (`ModuleIssueResponse` on the frontend).
func (h *Handler) serializeModuleIssue(link, issue, module *core.Record) map[string]any {
	out := map[string]any{
		"id":               serializers.UUID(link),
		"created_at":       serializers.DateTime(link, "created_at"),
		"updated_at":       serializers.DateTime(link, "updated_at"),
		"created_by":       serializers.RelationUUID(h.App, link, "created_by"),
		"updated_by":       serializers.RelationUUID(h.App, link, "updated_by"),
		"project":          serializers.RelationUUID(h.App, link, "project"),
		"workspace":        serializers.RelationUUID(h.App, link, "workspace"),
		"module":           serializers.RelationUUID(h.App, link, "module"),
		"issue":            serializers.RelationUUID(h.App, link, "issue"),
		"sub_issues_count": 0,
	}
	if issue != nil {
		out["issue_detail"] = serializers.Issue(h.App, issue)
	}
	if module != nil {
		out["module_detail"] = serializers.Module(h.App, module, false, false)
	}
	return out
}

// bridgeIssueIDs returns the PocketBase ids of the issues linked to a module.
func (h *Handler) bridgeIssueIDs(moduleID string) []string {
	rows, err := h.App.FindRecordsByFilter(
		"module_issues",
		"module = {:module} && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"module": moduleID},
	)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if issue := row.GetString("issue"); issue != "" {
			ids = append(ids, issue)
		}
	}
	return ids
}

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
