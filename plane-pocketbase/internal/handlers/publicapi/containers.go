package publicapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// --- Cycles ---------------------------------------------------------------------

// Cycles serves GET/POST …/{project_id}/cycles/.
func (h *Handler) Cycles(e *core.RequestEvent) error {
	user := h.requireUser(e)
	if user == nil {
		return nil
	}
	ws := h.ws(e)
	if ws == nil {
		return nil
	}
	project := h.proj(e, ws)
	if project == nil {
		return nil
	}
	switch e.Request.Method {
	case http.MethodGet:
		return h.list(e, "cycles",
			"project = {:p} && workspace = {:ws} && deleted_at = ''",
			dbx.Params{"p": project.Id, "ws": ws.Id}, "-created_at",
			func(r *core.Record) any { return serializers.Cycle(h.App, r, false) })
	case http.MethodPost:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		name, _ := body["name"].(string)
		if strings.TrimSpace(name) == "" {
			return serializers.Validation(e, "name", "This field is required.")
		}
		rec, err := h.newChild(ws, project, user, "cycles", name)
		if err != nil {
			return err
		}
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, serializers.Cycle(h.App, rec, false))
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// CycleDetail serves GET/PATCH/DELETE …/cycles/{pk}/.
func (h *Handler) CycleDetail(e *core.RequestEvent) error {
	user := h.requireUser(e)
	if user == nil {
		return nil
	}
	ws := h.ws(e)
	if ws == nil {
		return nil
	}
	project := h.proj(e, ws)
	if project == nil {
		return nil
	}
	rec, _ := serializers.FindByUUIDOrID(h.App, "cycles", e.Request.PathValue("pk"))
	if rec == nil || rec.GetString("project") != project.Id {
		return serializers.NotFound(e, "")
	}
	return h.childDetail(e, rec, serializers.Cycle(h.App, rec, false), []string{"name", "description", "start_date", "end_date"})
}

// newChild builds a fresh child record (cycle/module/label/…) with the
// standard uuid/workspace/project trio plus the required owned_by.
func (h *Handler) newChild(ws, project, user *core.Record, collection, name string) (*core.Record, error) {
	coll, err := h.App.FindCollectionByNameOrId(collection)
	if err != nil {
		return nil, err
	}
	rec := core.NewRecord(coll)
	rec.Set("uuid", serializers.NewUUID())
	rec.Set("workspace", ws.Id)
	rec.Set("project", project.Id)
	rec.Set("name", name)
	rec.Set("owned_by", user.Id)
	return rec, nil
}

// childDetail is the shared GET/PATCH/DELETE body for cycle/module children.
func (h *Handler) childDetail(e *core.RequestEvent, rec *core.Record, view map[string]any, editable []string) error {
	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, view)
	case http.MethodPatch, http.MethodPut:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		for _, key := range editable {
			if v, ok := body[key]; ok {
				rec.Set(key, v)
			}
		}
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, view)
	case http.MethodDelete:
		rec.Set("deleted_at", serializers.Now())
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// CycleIssues serves GET/POST …/cycles/{cycle_id}/cycle-issues/ — the bridge
// between a cycle and its issues.
func (h *Handler) CycleIssues(e *core.RequestEvent) error {
	user := h.requireUser(e)
	if user == nil {
		return nil
	}
	ws := h.ws(e)
	if ws == nil {
		return nil
	}
	project := h.proj(e, ws)
	if project == nil {
		return nil
	}
	cycle, _ := serializers.FindByUUIDOrID(h.App, "cycles", e.Request.PathValue("cycle_id"))
	if cycle == nil || cycle.GetString("project") != project.Id {
		return serializers.NotFound(e, "")
	}
	switch e.Request.Method {
	case http.MethodGet:
		recs, err := h.App.FindRecordsByFilter("cycle_issues",
			"cycle = {:c} && deleted_at = ''", "-created_at", 0, 0,
			dbx.Params{"c": cycle.Id})
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(recs))
		for _, b := range recs {
			issue, _ := serializers.FindByUUIDOrID(h.App, "issues", b.GetString("issue"))
			if issue == nil {
				continue
			}
			out = append(out, map[string]any{
				"id":    serializers.UUID(b),
				"issue": serializers.Issue(h.App, issue),
				"cycle": serializers.UUID(cycle),
			})
		}
		return e.JSON(http.StatusOK, out)
	case http.MethodPost:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		issueID, _ := body["issue"].(string)
		issue := h.issueInProject(project, issueID)
		if issue == nil {
			return serializers.NotFound(e, "Issue not found")
		}
		coll, err := h.App.FindCollectionByNameOrId("cycle_issues")
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("workspace", ws.Id)
		rec.Set("project", project.Id)
		rec.Set("cycle", cycle.Id)
		rec.Set("issue", issue.Id)
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, map[string]any{"id": serializers.UUID(rec)})
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// CycleIssueDetail serves GET/DELETE …/cycle-issues/{issue_id}/.
func (h *Handler) CycleIssueDetail(e *core.RequestEvent) error {
	user := h.requireUser(e)
	if user == nil {
		return nil
	}
	ws := h.ws(e)
	if ws == nil {
		return nil
	}
	project := h.proj(e, ws)
	if project == nil {
		return nil
	}
	issue := h.issueInProject(project, e.Request.PathValue("issue_id"))
	if issue == nil {
		return serializers.NotFound(e, "")
	}
	bridge, _ := h.App.FindFirstRecordByFilter("cycle_issues",
		"cycle = {:c} && issue = {:i} && deleted_at = ''",
		dbx.Params{"c": e.Request.PathValue("cycle_id"), "i": issue.Id})
	switch e.Request.Method {
	case http.MethodGet:
		if bridge == nil {
			return serializers.NotFound(e, "")
		}
		return e.JSON(http.StatusOK, map[string]any{"id": serializers.UUID(bridge), "issue": serializers.Issue(h.App, issue)})
	case http.MethodDelete:
		if bridge != nil {
			_ = h.App.Delete(bridge)
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// --- Modules --------------------------------------------------------------------

// Modules serves GET/POST …/modules/.
func (h *Handler) Modules(e *core.RequestEvent) error {
	user := h.requireUser(e)
	if user == nil {
		return nil
	}
	ws := h.ws(e)
	if ws == nil {
		return nil
	}
	project := h.proj(e, ws)
	if project == nil {
		return nil
	}
	switch e.Request.Method {
	case http.MethodGet:
		return h.list(e, "modules",
			"project = {:p} && workspace = {:ws} && deleted_at = ''",
			dbx.Params{"p": project.Id, "ws": ws.Id}, "-created_at",
			func(r *core.Record) any { return serializers.Module(h.App, r, false, false) })
	case http.MethodPost:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		name, _ := body["name"].(string)
		if strings.TrimSpace(name) == "" {
			return serializers.Validation(e, "name", "This field is required.")
		}
		rec, err := h.newChild(ws, project, user, "modules", name)
		if err != nil {
			return err
		}
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, serializers.Module(h.App, rec, false, false))
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// ModuleDetail serves GET/PATCH/DELETE …/modules/{pk}/.
func (h *Handler) ModuleDetail(e *core.RequestEvent) error {
	user := h.requireUser(e)
	if user == nil {
		return nil
	}
	ws := h.ws(e)
	if ws == nil {
		return nil
	}
	project := h.proj(e, ws)
	if project == nil {
		return nil
	}
	rec, _ := serializers.FindByUUIDOrID(h.App, "modules", e.Request.PathValue("pk"))
	if rec == nil || rec.GetString("project") != project.Id {
		return serializers.NotFound(e, "")
	}
	return h.childDetail(e, rec, serializers.Module(h.App, rec, false, false), []string{"name", "description", "start_date", "target_date", "status"})
}

// ModuleIssues serves GET/POST …/modules/{module_id}/module-issues/.
func (h *Handler) ModuleIssues(e *core.RequestEvent) error {
	user := h.requireUser(e)
	if user == nil {
		return nil
	}
	ws := h.ws(e)
	if ws == nil {
		return nil
	}
	project := h.proj(e, ws)
	if project == nil {
		return nil
	}
	module, _ := serializers.FindByUUIDOrID(h.App, "modules", e.Request.PathValue("module_id"))
	if module == nil || module.GetString("project") != project.Id {
		return serializers.NotFound(e, "")
	}
	switch e.Request.Method {
	case http.MethodGet:
		recs, err := h.App.FindRecordsByFilter("module_issues",
			"module = {:m} && deleted_at = ''", "-created_at", 0, 0,
			dbx.Params{"m": module.Id})
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(recs))
		for _, b := range recs {
			issue, _ := serializers.FindByUUIDOrID(h.App, "issues", b.GetString("issue"))
			if issue == nil {
				continue
			}
			out = append(out, map[string]any{
				"id":     serializers.UUID(b),
				"issue":  serializers.Issue(h.App, issue),
				"module": serializers.UUID(module),
			})
		}
		return e.JSON(http.StatusOK, out)
	case http.MethodPost:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		issueID, _ := body["issue"].(string)
		issue := h.issueInProject(project, issueID)
		if issue == nil {
			return serializers.NotFound(e, "Issue not found")
		}
		coll, err := h.App.FindCollectionByNameOrId("module_issues")
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("workspace", ws.Id)
		rec.Set("project", project.Id)
		rec.Set("module", module.Id)
		rec.Set("issue", issue.Id)
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, map[string]any{"id": serializers.UUID(rec)})
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// ModuleIssueDetail serves GET/DELETE …/module-issues/{issue_id}/.
func (h *Handler) ModuleIssueDetail(e *core.RequestEvent) error {
	user := h.requireUser(e)
	if user == nil {
		return nil
	}
	ws := h.ws(e)
	if ws == nil {
		return nil
	}
	project := h.proj(e, ws)
	if project == nil {
		return nil
	}
	issue := h.issueInProject(project, e.Request.PathValue("issue_id"))
	if issue == nil {
		return serializers.NotFound(e, "")
	}
	bridge, _ := h.App.FindFirstRecordByFilter("module_issues",
		"module = {:m} && issue = {:i} && deleted_at = ''",
		dbx.Params{"m": e.Request.PathValue("module_id"), "i": issue.Id})
	switch e.Request.Method {
	case http.MethodGet:
		if bridge == nil {
			return serializers.NotFound(e, "")
		}
		return e.JSON(http.StatusOK, map[string]any{"id": serializers.UUID(bridge), "issue": serializers.Issue(h.App, issue)})
	case http.MethodDelete:
		if bridge != nil {
			_ = h.App.Delete(bridge)
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}
