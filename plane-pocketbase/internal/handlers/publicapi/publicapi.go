// Package publicapi implements Plane's open API v1 (`/api/v1/…`, group
// `publicapi`) — the external, third-party-facing API authenticated with an
// `X-Api-Key` header against api_tokens (NOT a session cookie). Django
// source: apps/api/plane/api/**.
package publicapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Handler carries the app handle for the open API domain.
type Handler struct {
	App core.App
}

// New builds the handler.
func New(app core.App) *Handler { return &Handler{App: app} }

// requireUser rejects requests without a valid API token (Django:
// permission_classes = [IsAuthenticated]).
func (h *Handler) requireUser(e *core.RequestEvent) *core.Record {
	user := middleware.APIKeyUser(e)
	if user == nil {
		serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	return user
}

// ws resolves the workspace by slug; nil (with response written) on miss.
func (h *Handler) ws(e *core.RequestEvent) *core.Record {
	ws, err := h.App.FindFirstRecordByFilter("workspaces",
		"slug = {:slug} && deleted_at = ''",
		dbx.Params{"slug": e.Request.PathValue("slug")})
	if err != nil || ws == nil {
		serializers.NotFound(e, "")
		return nil
	}
	return ws
}

// proj resolves a project (Plane UUID or record id) inside the workspace.
func (h *Handler) proj(e *core.RequestEvent, ws *core.Record) *core.Record {
	p, _ := serializers.FindByUUIDOrID(h.App, "projects", e.Request.PathValue("project_id"))
	if p == nil || p.GetString("workspace") != ws.Id {
		serializers.NotFound(e, "")
		return nil
	}
	return p
}

// list is the shared cursor-paginated list helper.
func (h *Handler) list(e *core.RequestEvent, collection, filter string, params dbx.Params, sort string, serialize func(*core.Record) any) error {
	perPage, cursor := serializers.ListParams(e, 50)
	env, err := serializers.Paginate(h.App, serializers.ListQuery{
		Collection: collection,
		Filter:     filter,
		Params:     params,
		Sort:       sort,
		PerPage:    perPage,
		Cursor:     cursor,
	}, serialize)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, env)
}

// --- Schema (docs) -----------------------------------------------------------

// Schema serves GET /api/v1/schema/ — a minimal OpenAPI document. Plane
// generates the full schema with drf-spectacular; the frontends never call
// this, so a compact stub keeps the contract alive.
func (h *Handler) Schema(e *core.RequestEvent) error {
	doc := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":   "Plane API",
			"version": "v1",
		},
		"paths": map[string]any{},
	}
	return e.JSON(http.StatusOK, doc)
}

// SchemaUI serves the swagger-ui / redoc HTML shells.
func (h *Handler) SchemaUI(e *core.RequestEvent) error {
	if strings.HasSuffix(e.Request.URL.Path, "redoc") {
		e.Response.Header().Set("Content-Type", "text/html; charset=utf-8")
		return e.String(http.StatusOK, redocHTML)
	}
	e.Response.Header().Set("Content-Type", "text/html; charset=utf-8")
	return e.String(http.StatusOK, swaggerHTML)
}

const swaggerHTML = `<!DOCTYPE html><html><head><title>Plane API</title></head><body><p>Swagger UI placeholder — see /api/v1/schema/ for the OpenAPI document.</p></body></html>`
const redocHTML = `<!DOCTYPE html><html><head><title>Plane API</title></head><body><p>ReDoc placeholder — see /api/v1/schema/ for the OpenAPI document.</p></body></html>`

// --- Users -------------------------------------------------------------------

// Me serves GET /api/v1/users/me/.
func (h *Handler) Me(e *core.RequestEvent) error {
	user := h.requireUser(e)
	if user == nil {
		return nil
	}
	return e.JSON(http.StatusOK, map[string]any{
		"id":            serializers.UUID(user),
		"email":         user.GetString("email"),
		"first_name":    user.GetString("first_name"),
		"last_name":     user.GetString("last_name"),
		"display_name":  user.GetString("display_name"),
		"avatar":        user.GetString("avatar"),
		"is_bot":        user.GetBool("is_bot"),
		"user_timezone": user.GetString("user_timezone"),
	})
}

// --- Workspace-scoped lookups -------------------------------------------------

// MembersLite serves GET /api/v1/workspaces/{slug}/members-lite/.
func (h *Handler) MembersLite(e *core.RequestEvent) error {
	if h.requireUser(e) == nil {
		return nil
	}
	ws := h.ws(e)
	if ws == nil {
		return nil
	}
	recs, err := h.App.FindRecordsByFilter("workspace_members",
		"workspace = {:ws} && is_active = 1", "created_at", 0, 0,
		dbx.Params{"ws": ws.Id})
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(recs))
	for _, m := range recs {
		item := map[string]any{
			"id":   serializers.UUID(m),
			"role": m.GetInt("role"),
		}
		if u, _ := serializers.FindByUUIDOrID(h.App, "users", m.GetString("member")); u != nil {
			item["member"] = serializers.UUID(u)
			item["display_name"] = u.GetString("display_name")
			item["first_name"] = u.GetString("first_name")
			item["last_name"] = u.GetString("last_name")
			item["avatar"] = u.GetString("avatar")
			item["is_bot"] = u.GetBool("is_bot")
		}
		out = append(out, item)
	}
	return e.JSON(http.StatusOK, out)
}

// Members serves GET /api/v1/workspaces/{slug}/members/.
func (h *Handler) Members(e *core.RequestEvent) error {
	if h.requireUser(e) == nil {
		return nil
	}
	ws := h.ws(e)
	if ws == nil {
		return nil
	}
	return h.list(e, "workspace_members",
		"workspace = {:ws} && is_active = 1",
		dbx.Params{"ws": ws.Id}, "-created_at",
		func(r *core.Record) any { return serializers.WorkspaceMember(h.App, r, false) })
}

// ProjectsLite serves GET /api/v1/workspaces/{slug}/projects-lite/.
func (h *Handler) ProjectsLite(e *core.RequestEvent) error {
	if h.requireUser(e) == nil {
		return nil
	}
	ws := h.ws(e)
	if ws == nil {
		return nil
	}
	recs, err := h.App.FindRecordsByFilter("projects",
		"workspace = {:ws} && deleted_at = ''", "-created_at", 0, 0,
		dbx.Params{"ws": ws.Id})
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(recs))
	for _, p := range recs {
		out = append(out, serializers.ProjectLite(p))
	}
	return e.JSON(http.StatusOK, out)
}

// Search serves GET /api/v1/workspaces/{slug}/issues/search/ and
// …/work-items/search/ — workspace-scoped issue search.
func (h *Handler) Search(e *core.RequestEvent) error {
	if h.requireUser(e) == nil {
		return nil
	}
	ws := h.ws(e)
	if ws == nil {
		return nil
	}
	term := strings.TrimSpace(e.Request.URL.Query().Get("search"))
	if term == "" {
		return e.JSON(http.StatusOK, map[string]any{"results": map[string]any{}})
	}
	recs, err := h.App.FindRecordsByFilter("issues",
		"workspace = {:ws} && deleted_at = '' && name ~ {:term}",
		"-created_at", 20, 0,
		dbx.Params{"ws": ws.Id, "term": term})
	if err != nil {
		return err
	}
	issues := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		issues = append(issues, map[string]any{
			"id":                  serializers.UUID(r),
			"name":                r.GetString("name"),
			"sequence_id":         r.GetInt("sequence_id"),
			"project__identifier": h.projectIdentifier(r.GetString("project")),
			"project_id":          serializers.RelationUUID(h.App, r, "project"),
			"workspace__slug":     ws.GetString("slug"),
		})
	}
	return e.JSON(http.StatusOK, map[string]any{"results": map[string]any{"issue": issues}})
}

func (h *Handler) projectIdentifier(projectID string) string {
	p, _ := serializers.FindByUUIDOrID(h.App, "projects", projectID)
	if p == nil {
		return ""
	}
	return p.GetString("identifier")
}

// issueByLocator resolves `{project_identifier}-{sequence}` (e.g. PROJ-12).
func (h *Handler) issueByLocator(ws *core.Record, locator string) *core.Record {
	idx := strings.LastIndex(locator, "-")
	if idx <= 0 || idx == len(locator)-1 {
		return nil
	}
	identifier := strings.ToUpper(locator[:idx])
	seq, err := strconv.Atoi(locator[idx+1:])
	if err != nil {
		return nil
	}
	project, err := h.App.FindFirstRecordByFilter("projects",
		"workspace = {:ws} && identifier = {:ident} && deleted_at = ''",
		dbx.Params{"ws": ws.Id, "ident": identifier})
	if err != nil || project == nil {
		return nil
	}
	issue, err := h.App.FindFirstRecordByFilter("issues",
		"project = {:project} && sequence_id = {:seq} && deleted_at = ''",
		dbx.Params{"project": project.Id, "seq": seq})
	if err != nil {
		return nil
	}
	return issue
}

// WorkItemByIdentifier serves GET
// /api/v1/workspaces/{slug}/work-items/{project_identifier}-{issue_identifier}/
// and the /issues/ alias.
func (h *Handler) WorkItemByIdentifier(e *core.RequestEvent) error {
	if h.requireUser(e) == nil {
		return nil
	}
	ws := h.ws(e)
	if ws == nil {
		return nil
	}
	issue := h.issueByLocator(ws, e.Request.PathValue("identifier"))
	if issue == nil {
		return serializers.NotFound(e, "")
	}
	return e.JSON(http.StatusOK, serializers.Issue(h.App, issue))
}

// --- Stickies -----------------------------------------------------------------

// Stickies serves GET/POST /api/v1/workspaces/{slug}/stickies/.
func (h *Handler) Stickies(e *core.RequestEvent) error {
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
		recs, err := h.App.FindRecordsByFilter("stickies",
			"workspace = {:ws} && owner = {:owner} && deleted_at = ''",
			"-created_at", 0, 0,
			dbx.Params{"ws": ws.Id, "owner": user.Id})
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(recs))
		for _, r := range recs {
			out = append(out, serializers.Sticky(h.App, r))
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
		coll, err := h.App.FindCollectionByNameOrId("stickies")
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("workspace", ws.Id)
		rec.Set("owner", user.Id)
		rec.Set("name", name)
		if v, ok := body["description_html"].(string); ok {
			rec.Set("description_html", v)
		}
		if v, ok := body["color"].(string); ok {
			rec.Set("color", v)
		}
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, serializers.Sticky(h.App, rec))
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// StickyDetail serves GET/PUT/PATCH/DELETE /api/v1/workspaces/{slug}/stickies/{pk}/.
func (h *Handler) StickyDetail(e *core.RequestEvent) error {
	user := h.requireUser(e)
	if user == nil {
		return nil
	}
	ws := h.ws(e)
	if ws == nil {
		return nil
	}
	rec, _ := serializers.FindByUUIDOrID(h.App, "stickies", e.Request.PathValue("pk"))
	if rec == nil || rec.GetString("workspace") != ws.Id || rec.GetString("owner") != user.Id {
		return serializers.NotFound(e, "")
	}
	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, serializers.Sticky(h.App, rec))
	case http.MethodPatch, http.MethodPut:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		if v, ok := body["name"].(string); ok {
			rec.Set("name", v)
		}
		if v, ok := body["description_html"].(string); ok {
			rec.Set("description_html", v)
		}
		if v, ok := body["color"].(string); ok {
			rec.Set("color", v)
		}
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, serializers.Sticky(h.App, rec))
	case http.MethodDelete:
		if err := h.App.Delete(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// fmtErr is a small convenience for wrapped errors.
func fmtErr(format string, args ...any) error { return fmt.Errorf(format, args...) }
