package module

import (
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// LinksList serves GET …/modules/{module_id}/module-links/ (ModuleLinkViewSet.list).
// Any project member may read.
func (h *Handler) LinksList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	record, err := h.findModuleIncludingArchived(scope, e.Request.PathValue("module_id"))
	if err != nil {
		return err
	}
	if record == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Module not found"})
	}
	links, err := h.App.FindRecordsByFilter(
		"module_links",
		"module = {:module} && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"module": record.Id},
	)
	if err != nil {
		return err
	}
	results := make([]any, 0, len(links))
	for _, link := range links {
		results = append(results, serializers.ModuleLink(h.App, link))
	}
	return e.JSON(http.StatusOK, results)
}

// LinkCreate serves POST …/modules/{module_id}/module-links/ (body
// `{title, url}`). ADMIN/MEMBER only.
func (h *Handler) LinkCreate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	record, err := h.findModuleIncludingArchived(scope, e.Request.PathValue("module_id"))
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
	url := normalizeURL(asString(body["url"]))
	if !validURL(url) {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Invalid URL format."})
	}
	if existing, _ := h.App.FindFirstRecordByFilter(
		"module_links",
		"module = {:module} && url = {:url} && deleted_at = ''",
		dbx.Params{"module": record.Id, "url": url},
	); existing != nil {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "URL already exists."})
	}

	collection, err := h.App.FindCollectionByNameOrId("module_links")
	if err != nil {
		return err
	}
	link := core.NewRecord(collection)
	link.Set("uuid", serializers.NewUUID())
	link.Set("module", record.Id)
	link.Set("project", scope.Project.Id)
	link.Set("workspace", scope.Workspace.Id)
	link.Set("title", asString(body["title"]))
	link.Set("url", url)
	if v, ok := body["metadata"]; ok {
		link.Set("metadata", v)
	}
	link.Set("created_by", user.Id)
	if err := h.App.Save(link); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusCreated, serializers.ModuleLink(h.App, link))
}

// LinkDetail serves GET …/module-links/{pk}/.
func (h *Handler) LinkDetail(e *core.RequestEvent) error {
	link, err := h.findLink(e)
	if err != nil {
		return err
	}
	if link == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	return e.JSON(http.StatusOK, serializers.ModuleLink(h.App, link))
}

// LinkUpdate serves PUT/PATCH …/module-links/{pk}/.
func (h *Handler) LinkUpdate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	link, err := h.findLink(e)
	if err != nil {
		return err
	}
	if link == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	if raw, ok := body["url"]; ok {
		url := normalizeURL(asString(raw))
		if !validURL(url) {
			return e.JSON(http.StatusBadRequest, map[string]any{"error": "Invalid URL format."})
		}
		if existing, _ := h.App.FindFirstRecordByFilter(
			"module_links",
			"module = {:module} && url = {:url} && deleted_at = '' && id != {:id}",
			dbx.Params{"module": link.GetString("module"), "url": url, "id": link.Id},
		); existing != nil {
			return e.JSON(http.StatusBadRequest, map[string]any{"error": "URL already exists for this Issue"})
		}
		link.Set("url", url)
	}
	if v, ok := body["title"]; ok {
		link.Set("title", v)
	}
	if v, ok := body["metadata"]; ok {
		link.Set("metadata", v)
	}
	if user := middleware.CurrentUser(e); user != nil {
		link.Set("updated_by", user.Id)
	}
	if err := h.App.Save(link); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.ModuleLink(h.App, link))
}

// LinkDelete serves DELETE …/module-links/{pk}/.
func (h *Handler) LinkDelete(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	link, err := h.findLink(e)
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

// findLink resolves a module-link by its Plane UUID within the project.
func (h *Handler) findLink(e *core.RequestEvent) (*core.Record, error) {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return nil, serializers.NotFound(e, "")
	}
	link, err := serializers.FindByUUID(h.App, "module_links", e.Request.PathValue("pk"))
	if err != nil || link == nil {
		return nil, err
	}
	if link.GetString("project") != scope.Project.Id || link.GetString("deleted_at") != "" {
		return nil, nil
	}
	return link, nil
}

// normalizeURL mirrors ModuleLinkSerializer.to_internal_value: a URL without a
// scheme gets `http://` prepended.
func normalizeURL(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return url
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "http://" + url
	}
	return url
}

// validURL is a deliberately small URL check (Django uses URLValidator).
func validURL(url string) bool {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return false
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://")
	if rest == "" {
		return false
	}
	host := rest
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		host = rest[:slash]
	}
	if host == "" || strings.ContainsAny(host, " \t") {
		return false
	}
	return strings.Contains(host, ".") || host == "localhost" || strings.Contains(host, ":")
}
