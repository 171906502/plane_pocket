package publicapi

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// --- Projects -----------------------------------------------------------------

// Projects serves GET/POST /api/v1/workspaces/{slug}/projects/.
func (h *Handler) Projects(e *core.RequestEvent) error {
	user := h.requireUser(e)
	if user == nil {
		return nil
	}
	ws := h.ws(e)
	if ws == nil {
		return nil
	}
	switch e.Request.Method {
	case http.MethodGet:
		return h.list(e, "projects",
			"workspace = {:ws} && deleted_at = ''",
			dbx.Params{"ws": ws.Id}, "-created_at",
			func(r *core.Record) any { return serializers.Project(h.App, r, user) })
	case http.MethodPost:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		name, _ := body["name"].(string)
		if strings.TrimSpace(name) == "" {
			return serializers.Validation(e, "name", "This field is required.")
		}
		coll, err := h.App.FindCollectionByNameOrId("projects")
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("workspace", ws.Id)
		rec.Set("name", name)
		rec.Set("identifier", strings.ToUpper(asString(body["identifier"])))
		rec.Set("network", 2)
		if v, ok := body["description"].(string); ok {
			rec.Set("description_text", v)
		}
		rec.Set("created_by", user.Id)
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		// Seed the creator's membership + default states, mirroring the app API.
		h.seedProjectExtras(rec, user)
		return e.JSON(http.StatusCreated, serializers.Project(h.App, rec, user))
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// seedProjectExtras creates the owner membership and the default states for a
// freshly created project (mirrors ProjectCreate in the app API).
func (h *Handler) seedProjectExtras(project *core.Record, user *core.Record) {
	if coll, err := h.App.FindCollectionByNameOrId("project_members"); err == nil {
		m := core.NewRecord(coll)
		m.Set("uuid", serializers.NewUUID())
		m.Set("workspace", project.GetString("workspace"))
		m.Set("project", project.Id)
		m.Set("member", user.Id)
		m.Set("role", 20)
		m.Set("is_active", true)
		_ = h.App.Save(m)
	}
	defaults := []struct {
		name, group, color string
		sequence           int
		def                bool
	}{
		{"Backlog", "backlog", "#60646C", 15000, true},
		{"Todo", "unstarted", "#60646C", 25000, false},
		{"In Progress", "started", "#F59E0B", 35000, false},
		{"Done", "completed", "#46A758", 45000, false},
		{"Cancelled", "cancelled", "#9AA4BC", 55000, false},
	}
	if coll, err := h.App.FindCollectionByNameOrId("states"); err == nil {
		for _, d := range defaults {
			s := core.NewRecord(coll)
			s.Set("uuid", serializers.NewUUID())
			s.Set("workspace", project.GetString("workspace"))
			s.Set("project", project.Id)
			s.Set("name", d.name)
			s.Set("group", d.group)
			s.Set("color", d.color)
			s.Set("sequence", d.sequence)
			s.Set("default", d.def)
			_ = h.App.Save(s)
		}
	}
}

// ProjectDetail serves GET/PATCH/DELETE /api/v1/workspaces/{slug}/projects/{pk}/.
func (h *Handler) ProjectDetail(e *core.RequestEvent) error {
	user := h.requireUser(e)
	if user == nil {
		return nil
	}
	ws := h.ws(e)
	if ws == nil {
		return nil
	}
	p, _ := serializers.FindByUUIDOrID(h.App, "projects", e.Request.PathValue("pk"))
	if p == nil || p.GetString("workspace") != ws.Id {
		return serializers.NotFound(e, "")
	}
	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, serializers.Project(h.App, p, user))
	case http.MethodPatch:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		for _, key := range []string{"name", "identifier", "description_text", "emoji", "network"} {
			if v, ok := body[key]; ok {
				p.Set(key, v)
			}
		}
		if err := h.App.Save(p); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, serializers.Project(h.App, p, user))
	case http.MethodDelete:
		p.Set("deleted_at", serializers.Now())
		if err := h.App.Save(p); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// --- States & Labels ------------------------------------------------------------

// States serves GET/POST /api/v1/workspaces/{slug}/projects/{project_id}/states/.
func (h *Handler) States(e *core.RequestEvent) error {
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
		recs, err := h.App.FindRecordsByFilter("states",
			"project = {:p} && workspace = {:ws}", "sequence", 0, 0,
			dbx.Params{"p": project.Id, "ws": ws.Id})
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(recs))
		for _, s := range recs {
			out = append(out, serializers.State(h.App, s, float64(s.GetInt("sequence"))))
		}
		return e.JSON(http.StatusOK, out)
	case http.MethodPost:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		name, _ := body["name"].(string)
		if strings.TrimSpace(name) == "" {
			return serializers.Validation(e, "name", "This field is required.")
		}
		coll, err := h.App.FindCollectionByNameOrId("states")
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("workspace", ws.Id)
		rec.Set("project", project.Id)
		rec.Set("name", name)
		rec.Set("group", asString(body["group"]))
		rec.Set("color", asString(body["color"]))
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, serializers.State(h.App, rec, float64(rec.GetInt("sequence"))))
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// StateDetail serves GET/PATCH/DELETE …/states/{state_id}/.
func (h *Handler) StateDetail(e *core.RequestEvent) error {
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
	rec, _ := serializers.FindByUUIDOrID(h.App, "states", e.Request.PathValue("state_id"))
	if rec == nil || rec.GetString("project") != project.Id {
		return serializers.NotFound(e, "")
	}
	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, serializers.State(h.App, rec, float64(rec.GetInt("sequence"))))
	case http.MethodPatch:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		for _, key := range []string{"name", "group", "color", "description"} {
			if v, ok := body[key]; ok {
				rec.Set(key, v)
			}
		}
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, serializers.State(h.App, rec, float64(rec.GetInt("sequence"))))
	case http.MethodDelete:
		if err := h.App.Delete(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// Labels serves GET/POST …/labels/.
func (h *Handler) Labels(e *core.RequestEvent) error {
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
		recs, err := h.App.FindRecordsByFilter("labels",
			"project = {:p} && workspace = {:ws}", "-created_at", 0, 0,
			dbx.Params{"p": project.Id, "ws": ws.Id})
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(recs))
		for _, l := range recs {
			out = append(out, serializers.Label(h.App, l))
		}
		return e.JSON(http.StatusOK, out)
	case http.MethodPost:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		name, _ := body["name"].(string)
		if strings.TrimSpace(name) == "" {
			return serializers.Validation(e, "name", "This field is required.")
		}
		coll, err := h.App.FindCollectionByNameOrId("labels")
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("workspace", ws.Id)
		rec.Set("project", project.Id)
		rec.Set("name", name)
		rec.Set("color", asString(body["color"]))
		rec.Set("sort_order", 65535)
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, serializers.Label(h.App, rec))
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// LabelDetail serves GET/PATCH/DELETE …/labels/{pk}/.
func (h *Handler) LabelDetail(e *core.RequestEvent) error {
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
	rec, _ := serializers.FindByUUIDOrID(h.App, "labels", e.Request.PathValue("pk"))
	if rec == nil || rec.GetString("project") != project.Id {
		return serializers.NotFound(e, "")
	}
	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, serializers.Label(h.App, rec))
	case http.MethodPatch:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		for _, key := range []string{"name", "color", "description", "sort_order"} {
			if v, ok := body[key]; ok {
				rec.Set(key, v)
			}
		}
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, serializers.Label(h.App, rec))
	case http.MethodDelete:
		if err := h.App.Delete(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// asString coerces a JSON value to a trimmed string.
func asString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", t))
	}
}
