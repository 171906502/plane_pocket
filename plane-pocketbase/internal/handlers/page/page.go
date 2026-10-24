// Package page implements Plane's page (wiki) endpoints for a project:
//
//	GET       …/pages-summary/                     (stats object)
//	GET/POST  …/pages/                             (plain array / create)
//	GET/PATCH/DELETE …/pages/{page_id}/
//	GET       …/favorite-pages/                    (plain array)
//	POST/DELETE …/favorite-pages/{page_id}/
//	GET       …/archived-pages/                    (plain array)
//	POST/DELETE …/pages/{page_id}/archive/         (archive / unarchive)
//	POST/DELETE …/pages/{page_id}/lock/            (lock / unlock)
//	POST      …/pages/{page_id}/access/
//	POST      …/pages/{page_id}/move/
//	GET/PATCH …/pages/{page_id}/description/       (binary stream / patch)
//	GET       …/pages/{page_id}/versions/          (plain array)
//	GET       …/pages/{page_id}/versions/{pk}/
//	POST      …/pages/{page_id}/versions/{version_id}/restore/
//	POST      …/pages/{page_id}/duplicate/
//
// `{page_id}`/`{pk}` are Plane UUIDs.
//
// Wire-shape note: PageViewSet.list overrides DRF's pagination and returns a
// plain JSON array (the frontend's page service is typed as an array). The
// api-spec's generic "cursor-paginated envelope" response_shape does not apply
// to pages — same situation as cycles (see internal/handlers/cycle).
package page

import (
	"encoding/base64"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"github.com/plane-pocketbase/plane-pocketbase/internal/live"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Handler holds the app dependency for the page routes.
type Handler struct {
	App core.App
}

// New returns a page handler bound to app.
func New(app core.App) *Handler {
	return &Handler{App: app}
}

// List serves GET …/pages/ (PageViewSet.list). It returns a plain array of
// PageSerializer objects ordered favourites-first, then by `order_by`
// (default -created_at), then id.
func (h *Handler) List(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	pages, err := h.listPages(e, scope)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, h.serializeList(e, scope, pages))
}

// Summary serves GET …/pages-summary/ (PageViewSet.summary): counts of public,
// private and archived pages visible to the caller.
func (h *Handler) Summary(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	pages, err := h.listPages(e, scope)
	if err != nil {
		return err
	}
	public, private, archived := 0, 0, 0
	for _, p := range pages {
		if p.GetString("archived_at") != "" {
			archived++
			continue
		}
		if p.GetInt("access") == 0 {
			public++
		} else {
			private++
		}
	}
	return e.JSON(http.StatusOK, map[string]any{
		"public_pages":   public,
		"private_pages":  private,
		"archived_pages": archived,
	})
}

// Create serves POST …/pages/ (PageViewSet.create). ADMIN or MEMBER only.
func (h *Handler) Create(e *core.RequestEvent) error {
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

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	// `Page.name` is `TextField(blank=True)` in Django, so the serializer marks
	// it optional. The web app deliberately creates the page first and lets the
	// user type the title into the inline editor afterwards — rejecting an empty
	// name makes every "Add page" fail with "Page could not be created".
	name := strings.TrimSpace(asString(body["name"]))

	collection, err := h.App.FindCollectionByNameOrId("pages")
	if err != nil {
		return err
	}
	page := core.NewRecord(collection)
	page.Set("uuid", serializers.NewUUID())
	page.Set("workspace", scope.Workspace.Id)
	page.Set("name", name)
	page.Set("owned_by", user.Id)
	page.Set("created_by", user.Id)
	page.Set("access", intValue(body["access"], 0))
	for _, field := range []string{"color", "view_props", "logo_props", "is_locked", "sort_order"} {
		if v, ok := body[field]; ok {
			page.Set(field, v)
		}
	}
	// description_* is the collaborative-editor payload consumed by internal/live.
	page.Set("description_json", jsonDefault(body["description_json"], map[string]any{}))
	page.Set("description_html", htmlDefault(body["description_html"]))
	if v, ok := body["description_binary"]; ok {
		if err := h.setDescriptionBinary(page, v); err != nil {
			return serializers.Validation(e, "description_binary", err.Error())
		}
	}
	if parent := asString(body["parent"]); parent != "" {
		if parentPage, _ := serializers.FindByUUID(h.App, "pages", parent); parentPage != nil && parentPage.GetString("workspace") == scope.Workspace.Id {
			page.Set("parent", parentPage.Id)
		}
	}
	if err := h.App.Save(page); err != nil {
		return serializers.Validation(e, "name", err.Error())
	}

	// Link the page to the URL project (ProjectPage) and attach labels.
	h.linkProject(scope, page, user)
	if err := h.replaceLabels(scope, page, labelIDsFrom(body), user); err != nil {
		return err
	}

	return e.JSON(http.StatusCreated, serializers.PageDetail(h.App, page, false))
}

// Detail serves GET …/pages/{page_id}/ (PageViewSet.retrieve).
func (h *Handler) Detail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	page, err := h.findPage(e, scope, e.Request.PathValue("page_id"))
	if err != nil {
		return err
	}
	if page == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Page not found"})
	}
	if h.guestBlocked(e, scope, page) {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "You are not allowed to view this page"})
	}
	data := serializers.PageDetail(h.App, page, h.isFavorite(e, scope, serializers.UUID(page)))
	data["issue_ids"] = h.linkedIssueIDs(page)
	return e.JSON(http.StatusOK, data)
}

// Update serves PATCH …/pages/{page_id}/ (PageViewSet.partial_update).
// Plane returns 200 with the updated PageDetailSerializer body.
func (h *Handler) Update(e *core.RequestEvent) error {
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
	page, err := h.findPage(e, scope, e.Request.PathValue("page_id"))
	if err != nil {
		return err
	}
	if page == nil {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Access cannot be updated since this page is owned by someone else"})
	}
	if page.GetBool("is_locked") {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Page is locked"})
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	if parent := asString(body["parent"]); parent != "" {
		parentPage, _ := serializers.FindByUUID(h.App, "pages", parent)
		if parentPage == nil || !h.hasProjectLink(parentPage, scope.Project.Id) {
			return serializers.NotFound(e, "")
		}
		page.Set("parent", parentPage.Id)
	}
	// Only the page owner may change `access`.
	if newAccess, ok := body["access"]; ok && intValue(newAccess, page.GetInt("access")) != page.GetInt("access") {
		if page.GetString("owned_by") != user.Id {
			return e.JSON(http.StatusBadRequest, map[string]any{"error": "Access cannot be updated since this page is owned by someone else"})
		}
		page.Set("access", intValue(newAccess, 0))
	}
	for _, field := range []string{"name", "color", "view_props", "logo_props", "is_locked", "description_json", "description_html"} {
		if v, ok := body[field]; ok {
			page.Set(field, v)
		}
	}
	// `description_binary` is a file field, so it cannot go through the loop
	// above: the payload is base64 and has to be turned into a stored file.
	if v, ok := body["description_binary"]; ok {
		if err := h.setDescriptionBinary(page, v); err != nil {
			return serializers.Validation(e, "description_binary", err.Error())
		}
	}
	if _, ok := body["description_html"]; ok {
		page.Set("description_stripped", stripTags(asString(body["description_html"])))
	}
	page.Set("updated_by", user.Id)
	if err := h.App.Save(page); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	if hasAnyKey(body, "labels", "label_ids") {
		if err := h.replaceLabels(scope, page, labelIDsFrom(body), user); err != nil {
			return err
		}
	}
	return e.JSON(http.StatusOK, serializers.PageDetail(h.App, page, h.isFavorite(e, scope, serializers.UUID(page))))
}

// Delete serves DELETE …/pages/{page_id}/ (PageViewSet.destroy). The page must
// be archived first; only the owner or a project admin may delete. Soft delete.
func (h *Handler) Delete(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	page, err := h.findPage(e, scope, e.Request.PathValue("page_id"))
	if err != nil {
		return err
	}
	if page == nil {
		return serializers.NotFound(e, "")
	}
	if page.GetString("archived_at") == "" {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "The page should be archived before deleting"})
	}
	if page.GetString("owned_by") != user.Id && scope.ProjectRole < middleware.RoleAdmin {
		return e.JSON(http.StatusForbidden, map[string]any{"error": "Only admin or owner can delete the page"})
	}
	// Detach children so the hierarchy does not point at a deleted page.
	children, _ := h.App.FindRecordsByFilter("pages", "parent = {:parent} && deleted_at = ''", "", 0, 0, dbx.Params{"parent": page.Id})
	for _, child := range children {
		child.Set("parent", "")
		_ = h.App.Save(child)
	}
	page.Set("deleted_at", serializers.Now())
	if err := h.App.Save(page); err != nil {
		return err
	}
	h.clearFavorites(scope, serializers.UUID(page))
	return e.NoContent(http.StatusNoContent)
}

// FavoriteCreate serves POST …/favorite-pages/{page_id}/ (PageFavoriteViewSet.create).
func (h *Handler) FavoriteCreate(e *core.RequestEvent) error {
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
	collection, err := h.App.FindCollectionByNameOrId("user_favorites")
	if err != nil {
		return err
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("user", user.Id)
	record.Set("project", scope.Project.Id)
	record.Set("workspace", scope.Workspace.Id)
	record.Set("entity_type", "page")
	record.Set("entity_identifier", e.Request.PathValue("page_id"))
	record.Set("created_by", user.Id)
	if err := h.App.Save(record); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// FavoriteDestroy serves DELETE …/favorite-pages/{page_id}/.
func (h *Handler) FavoriteDestroy(e *core.RequestEvent) error {
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
	record, _ := h.App.FindFirstRecordByFilter(
		"user_favorites",
		"user = {:user} && project = {:project} && entity_type = 'page' && entity_identifier = {:page}",
		dbx.Params{"user": user.Id, "project": scope.Project.Id, "page": e.Request.PathValue("page_id")},
	)
	if record != nil {
		if err := h.App.Delete(record); err != nil {
			return err
		}
	}
	return e.NoContent(http.StatusNoContent)
}

// FavoriteList serves GET …/favorite-pages/ — the LIST half of the favourites
// pair (PageFavoriteViewSet.list). The web app's `fetchFavorites` is typed as
// `TPage[]`, so it is a plain array of PageSerializer objects, same shape as
// `GET …/pages/`, restricted to the pages the caller has favourited in the
// project and marked `is_favorite: true`.
func (h *Handler) FavoriteList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	pages, err := h.listPages(e, scope)
	if err != nil {
		return err
	}
	favorites := h.favoriteIDs(e, scope)
	results := make([]any, 0, len(favorites))
	for _, page := range pages {
		if favorites[serializers.UUID(page)] {
			results = append(results, serializers.Page(h.App, page, true))
		}
	}
	return e.JSON(http.StatusOK, results)
}

// Archive serves POST …/pages/{page_id}/archive/. Returns Plane's
// {"archived_at": "<timestamp>"} body (200).
func (h *Handler) Archive(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	page, err := h.findPage(e, scope, e.Request.PathValue("page_id"))
	if err != nil {
		return err
	}
	if page == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleMember && page.GetString("owned_by") != user.Id {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Only the owner or member can archive the page"})
	}
	now := time.Now()
	h.setArchivedRecursive(page, now)
	h.clearFavorites(scope, serializers.UUID(page))
	return e.JSON(http.StatusOK, map[string]any{"archived_at": now.Format("2006-01-02 15:04:05.000000")})
}

// Unarchive serves DELETE …/pages/{page_id}/archive/ (PageViewSet.unarchive).
func (h *Handler) Unarchive(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	page, err := h.findPage(e, scope, e.Request.PathValue("page_id"))
	if err != nil {
		return err
	}
	if page == nil {
		return serializers.NotFound(e, "")
	}
	if scope.ProjectRole < middleware.RoleAdmin && page.GetString("owned_by") != user.Id {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Only the owner or admin can un archive the page"})
	}
	// If the parent is still archived, detach it so the hierarchy is valid.
	if parentID := page.GetString("parent"); parentID != "" {
		if parent, _ := h.App.FindRecordById("pages", parentID); parent != nil && parent.GetString("archived_at") != "" {
			page.Set("parent", "")
			_ = h.App.Save(page)
		}
	}
	h.setArchivedRecursive(page, time.Time{})
	return e.NoContent(http.StatusNoContent)
}

// ArchivedList serves GET …/archived-pages/ — the "Archived" pages tab. The web
// app's `fetchArchived` is typed as `TPage[]`, so it is a plain array of
// PageSerializer objects, restricted to pages whose `archived_at` is set. It
// reuses the same visibility scope as the main list (`listPages`), so a page
// the caller may not see never leaks through the archive view.
func (h *Handler) ArchivedList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	pages, err := h.listPages(e, scope)
	if err != nil {
		return err
	}
	favorites := h.favoriteIDs(e, scope)
	results := make([]any, 0)
	for _, page := range pages {
		if page.GetString("archived_at") == "" {
			continue
		}
		results = append(results, serializers.Page(h.App, page, favorites[serializers.UUID(page)]))
	}
	return e.JSON(http.StatusOK, results)
}

// Lock serves POST …/pages/{page_id}/lock/.
func (h *Handler) Lock(e *core.RequestEvent) error {
	// PageViewSet.lock is a POST, so ProjectPagePermission's non-safe branch
	// applies: ADMIN or MEMBER (the owner is admitted earlier by the
	// `owned_by == user` short-circuit).
	return h.setLocked(e, true, middleware.RoleMember)
}

// Unlock serves DELETE …/pages/{page_id}/lock/.
func (h *Handler) Unlock(e *core.RequestEvent) error {
	// PageViewSet.unlock is a DELETE, which ProjectPagePermission restricts to
	// ADMIN for anyone who is not the owner.
	return h.setLocked(e, false, middleware.RoleAdmin)
}

// Access serves POST …/pages/{page_id}/access/.
func (h *Handler) Access(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	page, err := h.findPage(e, scope, e.Request.PathValue("page_id"))
	if err != nil {
		return err
	}
	if page == nil {
		return serializers.NotFound(e, "")
	}
	body := map[string]any{}
	_ = e.BindBody(&body)
	access := intValue(body["access"], 0)
	if access != page.GetInt("access") && page.GetString("owned_by") != user.Id {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Access cannot be updated since this page is owned by someone else"})
	}
	page.Set("access", access)
	if err := h.App.Save(page); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// DescriptionGet serves GET …/pages/{page_id}/description/: the raw Yjs binary
// as an application/octet-stream attachment (PagesDescriptionViewSet.retrieve).
func (h *Handler) DescriptionGet(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	page, err := h.findPage(e, scope, e.Request.PathValue("page_id"))
	if err != nil {
		return err
	}
	if page == nil {
		return serializers.NotFound(e, "")
	}
	data := h.readBinary(page)
	e.Response.Header().Set("Content-Disposition", `attachment; filename="page_description.bin"`)
	return e.Blob(http.StatusOK, "application/octet-stream", data)
}

// DescriptionPatch serves PATCH …/pages/{page_id}/description/
// (PagesDescriptionViewSet.partial_update). Returns {"message": "Updated successfully"}.
func (h *Handler) DescriptionPatch(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	page, err := h.findPage(e, scope, e.Request.PathValue("page_id"))
	if err != nil {
		return err
	}
	if page == nil {
		return serializers.NotFound(e, "")
	}
	// PagesDescriptionViewSet is guarded by ProjectPagePermission, whose PATCH
	// branch is ADMIN/MEMBER — the owner is admitted by the `owned_by` shortcut.
	if page.GetString("owned_by") != user.Id && scope.ProjectRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	if page.GetBool("is_locked") {
		return e.JSON(http.StatusBadRequest, map[string]any{"error_code": "PAGE_LOCKED", "error_message": "PAGE_LOCKED"})
	}
	if page.GetString("archived_at") != "" {
		return e.JSON(http.StatusBadRequest, map[string]any{"error_code": "PAGE_ARCHIVED", "error_message": "PAGE_ARCHIVED"})
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	if v, ok := body["description_binary"]; ok {
		if err := h.setDescriptionBinary(page, v); err != nil {
			return serializers.Validation(e, "description_binary", err.Error())
		}
	}
	if v, ok := body["description_html"]; ok {
		page.Set("description_html", v)
		// Django's Page.save() derives this column; without it a page edited
		// through this endpoint keeps an empty search index (sticky search
		// filters on `description_stripped`).
		page.Set("description_stripped", stripTags(asString(v)))
	}
	if v, ok := body["description_json"]; ok {
		page.Set("description_json", v)
	}
	page.Set("updated_by", user.Id)
	if err := h.App.Save(page); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, map[string]any{"message": "Updated successfully"})
}

// VersionsList serves GET …/pages/{page_id}/versions/ (PageVersionEndpoint, no pk):
// a plain array of PageVersionSerializer objects.
func (h *Handler) VersionsList(e *core.RequestEvent) error {
	_, page, err := h.scopedPage(e)
	if err != nil || page == nil {
		return err
	}
	versions, err := h.App.FindRecordsByFilter(
		"page_versions",
		"page = {:page} && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"page": page.Id},
	)
	if err != nil {
		return err
	}
	results := make([]any, 0, len(versions))
	for _, v := range versions {
		results = append(results, serializers.PageVersion(h.App, v))
	}
	return e.JSON(http.StatusOK, results)
}

// VersionDetail serves GET …/pages/{page_id}/versions/{pk}/.
func (h *Handler) VersionDetail(e *core.RequestEvent) error {
	_, page, err := h.scopedPage(e)
	if err != nil || page == nil {
		return err
	}
	version, _ := serializers.FindByUUID(h.App, "page_versions", e.Request.PathValue("pk"))
	if version == nil || version.GetString("page") != page.Id {
		return serializers.NotFound(e, "")
	}
	return e.JSON(http.StatusOK, serializers.PageVersionDetail(h.App, version))
}

// RestoreVersion serves POST …/pages/{page_id}/versions/{version_id}/restore/.
// It copies the version's stored description back onto the page and answers 204
// (the web client types `restoreVersion` as `Promise<void>`).
//
// The whole payload is copied, `description_binary` included: the version row
// holds its own protected-file snapshot, so the bytes are read from the
// version and written back onto the page as a new file (a stored filename
// cannot be pointed at from another record — see setDescriptionBinary).
//
// The page's live log is then purged. The Yjs relay replays its stored CRDT
// updates on join, which would lay the pre-restore content back over the
// restored one and make the restore silently do nothing; after the purge the
// next join re-creates an empty log and seeds it from the page's (just
// restored) description_binary.
//
// The save itself fires the pages update hook, which records a new version for
// the restored content — upstream behaves the same way, and the write lands in
// the unwatched page_versions collection, so it stops there (no loop).
func (h *Handler) RestoreVersion(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	page, err := h.findPage(e, scope, e.Request.PathValue("page_id"))
	if err != nil {
		return err
	}
	if page == nil {
		return serializers.NotFound(e, "")
	}
	version, _ := serializers.FindByUUID(h.App, "page_versions", e.Request.PathValue("version_id"))
	if version == nil || version.GetString("page") != page.Id {
		return serializers.NotFound(e, "")
	}
	page.Set("description_json", version.Get("description_json"))
	page.Set("description_html", version.GetString("description_html"))
	page.Set("description_stripped", version.GetString("description_stripped"))
	if data := h.readBinary(version); len(data) > 0 {
		if file, err := filesystem.NewFileFromBytes(data, descriptionBinaryName); err == nil {
			page.Set("description_binary", file)
		}
	}
	page.Set("updated_by", user.Id)
	if err := h.App.Save(page); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	// The document name the relay keys rooms and logs by is the page's uuid
	// (internal/live handler.go authorize + store.go openDocLog).
	live.New(h.App).PurgeDocument(page.GetString("uuid"))
	return e.NoContent(http.StatusNoContent)
}

// Duplicate serves POST …/pages/{page_id}/duplicate/ (PageDuplicateEndpoint).
// Returns 201 with the new page's PageDetailSerializer body.
func (h *Handler) Duplicate(e *core.RequestEvent) error {
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
	page, err := h.findPage(e, scope, e.Request.PathValue("page_id"))
	if err != nil {
		return err
	}
	if page == nil {
		return serializers.NotFound(e, "")
	}
	if page.GetInt("access") == 1 && page.GetString("owned_by") != user.Id {
		return serializers.Forbidden(e, "Permission denied")
	}

	collection, err := h.App.FindCollectionByNameOrId("pages")
	if err != nil {
		return err
	}
	copy := core.NewRecord(collection)
	copy.Set("uuid", serializers.NewUUID())
	copy.Set("workspace", page.GetString("workspace"))
	copy.Set("name", page.GetString("name")+" (Copy)")
	copy.Set("description_json", page.Get("description_json"))
	copy.Set("description_html", page.GetString("description_html"))
	copy.Set("description_stripped", page.GetString("description_stripped"))
	copy.Set("owned_by", user.Id)
	copy.Set("access", page.GetInt("access"))
	copy.Set("color", page.GetString("color"))
	copy.Set("is_locked", page.GetBool("is_locked"))
	copy.Set("view_props", page.Get("view_props"))
	copy.Set("logo_props", page.Get("logo_props"))
	copy.Set("created_by", user.Id)
	if err := h.App.Save(copy); err != nil {
		return serializers.Validation(e, "name", err.Error())
	}
	// Copy the project links (Django copies every ProjectPage the source has).
	sourceLinks, _ := h.App.FindRecordsByFilter("project_pages", "page = {:page} && deleted_at = ''", "", 0, 0, dbx.Params{"page": page.Id})
	linkCollection, err := h.App.FindCollectionByNameOrId("project_pages")
	if err != nil {
		return err
	}
	for _, link := range sourceLinks {
		row := core.NewRecord(linkCollection)
		row.Set("uuid", serializers.NewUUID())
		row.Set("workspace", link.GetString("workspace"))
		row.Set("project", link.GetString("project"))
		row.Set("page", copy.Id)
		row.Set("created_by", user.Id)
		if err := h.App.Save(row); err != nil {
			return err
		}
	}
	return e.JSON(http.StatusCreated, serializers.PageDetail(h.App, copy, false))
}

// Move serves POST …/pages/{page_id}/move/. The web client sends
// `{ "new_project_id": "<uuid>" }` and treats the response as empty
// (`Promise<void>`), so this answers 204.
//
// A page reaches a project through a `project_pages` link row, so moving means
// retiring the link to the URL project and (re)activating one for the
// destination project. The caller must be able to see the destination: an
// active membership there, or a workspace Admin.
func (h *Handler) Move(e *core.RequestEvent) error {
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
	page, err := h.findPage(e, scope, e.Request.PathValue("page_id"))
	if err != nil {
		return err
	}
	if page == nil {
		return serializers.NotFound(e, "")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	newProjectUUID := asString(body["new_project_id"])
	if newProjectUUID == "" {
		return serializers.Validation(e, "new_project_id", "This field is required.")
	}
	newProject, _ := serializers.FindByUUID(h.App, "projects", newProjectUUID)
	if newProject == nil || newProject.GetString("workspace") != scope.Workspace.Id || newProject.GetString("deleted_at") != "" {
		return serializers.NotFound(e, "")
	}
	if newProject.Id == scope.Project.Id {
		// Already there — nothing to do, but do not 404: the client only wants
		// the page gone from the old project's list.
		return e.NoContent(http.StatusNoContent)
	}
	if !h.canReachProject(scope, user, newProject.Id) {
		return serializers.NotFound(e, "")
	}

	if err := h.App.RunInTransaction(func(txApp core.App) error {
		// Retire the active link to the URL project.
		if link, _ := txApp.FindFirstRecordByFilter(
			"project_pages",
			"page = {:page} && project = {:project} && deleted_at = ''",
			dbx.Params{"page": page.Id, "project": scope.Project.Id},
		); link != nil {
			link.Set("deleted_at", serializers.Now())
			if err := txApp.Save(link); err != nil {
				return err
			}
		}
		// Reactivate a previous link to the destination, or create one.
		if link, _ := txApp.FindFirstRecordByFilter(
			"project_pages",
			"page = {:page} && project = {:project}",
			dbx.Params{"page": page.Id, "project": newProject.Id},
		); link != nil {
			link.Set("deleted_at", "")
			return txApp.Save(link)
		}
		collection, err := txApp.FindCollectionByNameOrId("project_pages")
		if err != nil {
			return err
		}
		row := core.NewRecord(collection)
		row.Set("uuid", serializers.NewUUID())
		row.Set("workspace", scope.Workspace.Id)
		row.Set("project", newProject.Id)
		row.Set("page", page.Id)
		row.Set("created_by", user.Id)
		return txApp.Save(row)
	}); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// canReachProject reports whether the caller may target the given project: an
// active membership row, or a workspace Admin.
func (h *Handler) canReachProject(scope *middleware.Scope, user *core.Record, projectID string) bool {
	if scope.WorkspaceRole >= middleware.RoleAdmin {
		return true
	}
	member, err := h.App.FindFirstRecordByFilter(
		"project_members",
		"project = {:project} && member = {:member} && is_active = true && deleted_at = ''",
		dbx.Params{"project": projectID, "member": user.Id},
	)
	return err == nil && member != nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// listPages reproduces PageViewSet.get_queryset: pages in the workspace with an
// active ProjectPage link to the URL project, top-level only (parent is null),
// visible to the caller (owner, or public). Guests without
// guest_view_all_features see only pages they own.
func (h *Handler) listPages(e *core.RequestEvent, scope *middleware.Scope) ([]*core.Record, error) {
	links, err := h.App.FindRecordsByFilter("project_pages", "project = {:project} && deleted_at = ''", "", 0, 0, dbx.Params{"project": scope.Project.Id})
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return []*core.Record{}, nil
	}
	pageIDs := make([]string, 0, len(links))
	for _, link := range links {
		pageIDs = append(pageIDs, link.GetString("page"))
	}
	params := dbx.Params{"workspace": scope.Workspace.Id}
	filter := "workspace = {:workspace} && deleted_at = '' && parent = '' && " + orIDFilter("id", pageIDs, params)
	pages, err := h.App.FindRecordsByFilter("pages", filter, "", 0, 0, params)
	if err != nil {
		return nil, err
	}

	user := middleware.CurrentUser(e)
	guestScoped := scope.ProjectRole == middleware.RoleGuest && !scope.Project.GetBool("guest_view_all_features")
	visible := make([]*core.Record, 0, len(pages))
	for _, p := range pages {
		owner := user != nil && p.GetString("owned_by") == user.Id
		if guestScoped && !owner {
			continue
		}
		// Default queryset visibility: the owner or a public page.
		if !owner && p.GetInt("access") != 0 {
			continue
		}
		visible = append(visible, p)
	}
	return h.sortPages(e, scope, visible), nil
}

// sortPages orders favourites first, then by the caller's `order_by`
// (default -created_at), then by id, mirroring PageViewSet.get_queryset.
func (h *Handler) sortPages(e *core.RequestEvent, scope *middleware.Scope, pages []*core.Record) []*core.Record {
	favorites := h.favoriteIDs(e, scope)
	orderBy := e.Request.URL.Query().Get("order_by")
	if orderBy == "" {
		orderBy = "-created_at"
	}
	desc := strings.HasPrefix(orderBy, "-")
	field := strings.TrimPrefix(orderBy, "-")
	if field != "created_at" && field != "updated_at" && field != "name" && field != "sort_order" {
		field, desc = "created_at", true
	}
	sort.SliceStable(pages, func(i, j int) bool {
		fi := favorites[serializers.UUID(pages[i])]
		fj := favorites[serializers.UUID(pages[j])]
		if fi != fj {
			return fi
		}
		less := compareField(pages[i], pages[j], field)
		if less == 0 {
			return pages[i].Id < pages[j].Id
		}
		if desc {
			return less > 0
		}
		return less < 0
	})
	return pages
}

func compareField(a, b *core.Record, field string) int {
	switch field {
	case "name":
		return strings.Compare(a.GetString("name"), b.GetString("name"))
	case "sort_order":
		return cmpFloat(a.GetFloat("sort_order"), b.GetFloat("sort_order"))
	default:
		return cmpFloat(float64(a.GetDateTime(field).Time().UnixNano()), float64(b.GetDateTime(field).Time().UnixNano()))
	}
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// serializeList renders pages for the list endpoint.
func (h *Handler) serializeList(e *core.RequestEvent, scope *middleware.Scope, pages []*core.Record) []any {
	favorites := h.favoriteIDs(e, scope)
	results := make([]any, 0, len(pages))
	for _, p := range pages {
		results = append(results, serializers.Page(h.App, p, favorites[serializers.UUID(p)]))
	}
	return results
}

// findPage resolves a page by Plane UUID, scoped to the URL project via an
// active ProjectPage link, and not soft-deleted.
//
// It also applies PageViewSet.get_queryset's visibility rule —
// `Q(owned_by=caller) | Q(access=0)` — so a **private page behaves as absent to
// everyone but its owner**, whatever their project role. Without it any project
// member could read, lock, archive or re-describe another user's private page,
// which ProjectPagePermission denies.
func (h *Handler) findPage(e *core.RequestEvent, scope *middleware.Scope, pageUUID string) (*core.Record, error) {
	page, err := serializers.FindByUUID(h.App, "pages", pageUUID)
	if err != nil || page == nil {
		return nil, err
	}
	if page.GetString("workspace") != scope.Workspace.Id || page.GetString("deleted_at") != "" {
		return nil, nil
	}
	if !h.hasProjectLink(page, scope.Project.Id) {
		return nil, nil
	}
	// `access` 1 is PRIVATE_ACCESS, 0 is PUBLIC_ACCESS.
	if page.GetInt("access") != 0 {
		if user := middleware.CurrentUser(e); user == nil || page.GetString("owned_by") != user.Id {
			return nil, nil
		}
	}
	return page, nil
}

// scopedPage is the common guard for the version endpoints: it returns the
// resolved project scope and page, writing the 404 itself when the page is not
// visible in the URL project.
func (h *Handler) scopedPage(e *core.RequestEvent) (*middleware.Scope, *core.Record, error) {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return nil, nil, serializers.NotFound(e, "")
	}
	page, err := h.findPage(e, scope, e.Request.PathValue("page_id"))
	if err != nil {
		return nil, nil, err
	}
	if page == nil {
		return scope, nil, serializers.NotFound(e, "")
	}
	return scope, page, nil
}

// hasProjectLink reports whether the page has an active ProjectPage link to the
// given project.
func (h *Handler) hasProjectLink(page *core.Record, projectID string) bool {
	link, err := h.App.FindFirstRecordByFilter(
		"project_pages",
		"page = {:page} && project = {:project} && deleted_at = ''",
		dbx.Params{"page": page.Id, "project": projectID},
	)
	return err == nil && link != nil
}

// linkProject creates the ProjectPage join for a freshly created page.
func (h *Handler) linkProject(scope *middleware.Scope, page *core.Record, user *core.Record) {
	collection, err := h.App.FindCollectionByNameOrId("project_pages")
	if err != nil {
		return
	}
	row := core.NewRecord(collection)
	row.Set("uuid", serializers.NewUUID())
	row.Set("workspace", scope.Workspace.Id)
	row.Set("project", scope.Project.Id)
	row.Set("page", page.Id)
	row.Set("created_by", user.Id)
	_ = h.App.Save(row)
}

// replaceLabels rewrites the page's PageLabel rows.
func (h *Handler) replaceLabels(scope *middleware.Scope, page *core.Record, ids []string, user *core.Record) error {
	existing, err := h.App.FindRecordsByFilter("page_labels", "page = {:page} && deleted_at = ''", "", 0, 0, dbx.Params{"page": page.Id})
	if err != nil {
		return err
	}
	for _, row := range existing {
		if err := h.App.Delete(row); err != nil {
			return err
		}
	}
	collection, err := h.App.FindCollectionByNameOrId("page_labels")
	if err != nil {
		return err
	}
	for _, labelUUID := range ids {
		label, _ := serializers.FindByUUID(h.App, "labels", labelUUID)
		if label == nil || label.GetString("project") != scope.Project.Id {
			continue
		}
		row := core.NewRecord(collection)
		row.Set("uuid", serializers.NewUUID())
		row.Set("page", page.Id)
		row.Set("label", label.Id)
		row.Set("workspace", scope.Workspace.Id)
		row.Set("created_by", user.Id)
		if err := h.App.Save(row); err != nil {
			return err
		}
	}
	return nil
}

// setLocked toggles is_locked. `minRole` is the non-owner floor: PageViewSet
// guards both halves with ProjectPagePermission, which asks ADMIN/MEMBER for a
// POST and ADMIN for a DELETE, and lets the owner through unconditionally.
func (h *Handler) setLocked(e *core.RequestEvent, locked bool, minRole int) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	page, err := h.findPage(e, scope, e.Request.PathValue("page_id"))
	if err != nil {
		return err
	}
	if page == nil {
		return serializers.NotFound(e, "")
	}
	if page.GetString("owned_by") != user.Id && scope.ProjectRole < minRole {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	page.Set("is_locked", locked)
	if err := h.App.Save(page); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// setArchivedRecursive sets (or clears) archived_at on a page and every
// descendant, mirroring Django's recursive CTE.
func (h *Handler) setArchivedRecursive(page *core.Record, at time.Time) {
	h.applyArchived(page, at)
	children, _ := h.App.FindRecordsByFilter("pages", "parent = {:parent} && deleted_at = ''", "", 0, 0, dbx.Params{"parent": page.Id})
	for _, child := range children {
		h.setArchivedRecursive(child, at)
	}
}

func (h *Handler) applyArchived(page *core.Record, at time.Time) {
	if at.IsZero() {
		page.Set("archived_at", "")
	} else {
		page.Set("archived_at", at)
	}
	_ = h.App.Save(page)
}

// descriptionBinaryName is the stored filename. Keeping it constant means a new
// snapshot replaces the previous one instead of leaving a file per save.
const descriptionBinaryName = "description.bin"

// setDescriptionBinary stores the collaborative editor's snapshot.
//
// `description_binary` is a PocketBase *file* field: the column holds a filename
// and the bytes live in the record's protected storage. Assigning the base64
// payload straight to the field — what this used to do — stores it as a
// filename, which then fails validation on save, so `GET …/description/` never
// returned anything and the editor's offline fallback could never restore a
// document from the server.
func (h *Handler) setDescriptionBinary(page *core.Record, raw any) error {
	value := asString(raw)
	if value == "" {
		page.Set("description_binary", "")
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(data) == 0 {
		// Not base64: the client echoed back the filename it was given. Keep it
		// so a PATCH that only changes the HTML does not drop the snapshot.
		page.Set("description_binary", value)
		return nil
	}
	file, err := filesystem.NewFileFromBytes(data, descriptionBinaryName)
	if err != nil {
		return err
	}
	page.Set("description_binary", file)
	return nil
}

// readBinary returns the stored description_binary file contents, or an empty
// slice when none was saved.
func (h *Handler) readBinary(page *core.Record) []byte {
	filename := page.GetString("description_binary")
	if filename == "" {
		return []byte{}
	}
	fsys, err := h.App.NewFilesystem()
	if err != nil {
		return []byte{}
	}
	defer fsys.Close()

	reader, err := fsys.GetReader(page.BaseFilesPath() + "/" + filename)
	if err != nil {
		return []byte{}
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return []byte{}
	}
	return data
}

// linkedIssueIDs returns the issue UUIDs referenced by the page's PageLog rows.
func (h *Handler) linkedIssueIDs(page *core.Record) []string {
	logs, err := h.App.FindRecordsByFilter("page_logs", "page = {:page} && entity_name = 'issue'", "", 0, 0, dbx.Params{"page": page.Id})
	if err != nil {
		return []string{}
	}
	out := make([]string, 0, len(logs))
	for _, log := range logs {
		out = append(out, log.GetString("entity_identifier"))
	}
	return out
}

// guestBlocked reports whether a guest without guest_view_all_features is
// looking at a page they do not own.
func (h *Handler) guestBlocked(e *core.RequestEvent, scope *middleware.Scope, page *core.Record) bool {
	user := middleware.CurrentUser(e)
	if scope.ProjectRole != middleware.RoleGuest || scope.Project.GetBool("guest_view_all_features") {
		return false
	}
	return user == nil || page.GetString("owned_by") != user.Id
}

// favoriteIDs returns the set of page UUIDs the caller has favourited in the
// project.
func (h *Handler) favoriteIDs(e *core.RequestEvent, scope *middleware.Scope) map[string]bool {
	out := map[string]bool{}
	user := middleware.CurrentUser(e)
	if user == nil {
		return out
	}
	records, err := h.App.FindRecordsByFilter(
		"user_favorites",
		"user = {:user} && project = {:project} && entity_type = 'page' && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"user": user.Id, "project": scope.Project.Id},
	)
	if err != nil {
		return out
	}
	for _, record := range records {
		out[record.GetString("entity_identifier")] = true
	}
	return out
}

// isFavorite reports whether the caller favourited the given page.
func (h *Handler) isFavorite(e *core.RequestEvent, scope *middleware.Scope, pageUUID string) bool {
	user := middleware.CurrentUser(e)
	if user == nil || scope.Project == nil {
		return false
	}
	record, err := h.App.FindFirstRecordByFilter(
		"user_favorites",
		"user = {:user} && project = {:project} && entity_type = 'page' && entity_identifier = {:page} && deleted_at = ''",
		dbx.Params{"user": user.Id, "project": scope.Project.Id, "page": pageUUID},
	)
	return err == nil && record != nil
}

// clearFavorites hard-deletes every favourite pointing at the page.
func (h *Handler) clearFavorites(scope *middleware.Scope, pageUUID string) {
	records, err := h.App.FindRecordsByFilter(
		"user_favorites",
		"project = {:project} && entity_type = 'page' && entity_identifier = {:page}",
		"", 0, 0,
		dbx.Params{"project": scope.Project.Id, "page": pageUUID},
	)
	if err != nil {
		return
	}
	for _, record := range records {
		_ = h.App.Delete(record)
	}
}

// orIDFilter builds a PocketBase "id = {a} || id = {b}" expression.
func orIDFilter(column string, ids []string, params dbx.Params) string {
	if len(ids) == 0 {
		params["none"] = ""
		return column + " = {:none}"
	}
	parts := make([]string, 0, len(ids))
	for i, id := range ids {
		key := "p" + strconv.Itoa(i)
		parts = append(parts, column+" = {:"+key+"}")
		params[key] = id
	}
	return "(" + strings.Join(parts, " || ") + ")"
}

// labelIDsFrom reads the page label list, accepting both the serializer's
// write-only `labels` field and the read-side `label_ids` spelling.
func labelIDsFrom(body map[string]any) []string {
	for _, key := range []string{"labels", "label_ids"} {
		if ids, ok := stringList(body[key]); ok {
			return ids
		}
	}
	return nil
}

func stringList(raw any) ([]string, bool) {
	items, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out, true
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

func intValue(v any, def int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return def
}

func jsonDefault(v any, def any) any {
	if v == nil {
		return def
	}
	return v
}

func htmlDefault(v any) string {
	if s := asString(v); s != "" {
		return s
	}
	return "<p></p>"
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
