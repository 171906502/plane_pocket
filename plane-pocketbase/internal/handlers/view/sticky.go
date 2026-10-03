package view

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// StickyList serves GET /api/workspaces/{slug}/stickies/. It always answers with
// a cursor-paginated envelope (default 20 per page), matching
// WorkspaceStickyViewSet.list; `query` filters on the stripped description.
func (h *Handler) StickyList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	filter := "workspace = {:workspace} && owner = {:owner} && deleted_at = ''"
	params := dbx.Params{"workspace": scope.Workspace.Id, "owner": user.Id}
	if query := e.Request.URL.Query().Get("query"); query != "" {
		filter += " && description_stripped ~ {:query}"
		params["query"] = query
	}

	perPage, cursor := serializers.ListParams(e, 20)
	envelope, err := serializers.Paginate(h.App, serializers.ListQuery{
		Collection: "stickies",
		Filter:     filter,
		Params:     params,
		Sort:       "-sort_order",
		PerPage:    perPage,
		Cursor:     cursor,
	}, func(record *core.Record) any {
		return serializers.Sticky(h.App, record)
	})
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, envelope)
}

// StickyCreate serves POST /api/workspaces/{slug}/stickies/ (201 + the sticky).
func (h *Handler) StickyCreate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}

	collection, err := h.App.FindCollectionByNameOrId("stickies")
	if err != nil {
		return err
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("workspace", scope.Workspace.Id)
	record.Set("owner", user.Id)
	record.Set("created_by", user.Id)
	for _, field := range []string{"name", "description", "description_html", "logo_props", "color", "background_color"} {
		if v, ok := body[field]; ok {
			record.Set(field, v)
		}
	}
	record.Set("description_stripped", stripTags(asString(body["description_html"])))
	record.Set("sort_order", h.nextStickySortOrder(scope.Workspace.Id))
	if err := h.App.Save(record); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusCreated, serializers.Sticky(h.App, record))
}

// StickyDetail serves GET /api/workspaces/{slug}/stickies/{pk}/.
func (h *Handler) StickyDetail(e *core.RequestEvent) error {
	record, err := h.findSticky(e)
	if err != nil {
		return err
	}
	if record == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	return e.JSON(http.StatusOK, serializers.Sticky(h.App, record))
}

// StickyUpdate serves PATCH /api/workspaces/{slug}/stickies/{pk}/.
func (h *Handler) StickyUpdate(e *core.RequestEvent) error {
	record, err := h.findSticky(e)
	if err != nil {
		return err
	}
	if record == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	for _, field := range []string{"name", "description", "description_html", "logo_props", "color", "background_color", "sort_order"} {
		if v, ok := body[field]; ok {
			record.Set(field, v)
		}
	}
	if _, ok := body["description_html"]; ok {
		record.Set("description_stripped", stripTags(asString(body["description_html"])))
	}
	if user := middleware.CurrentUser(e); user != nil {
		record.Set("updated_by", user.Id)
	}
	if err := h.App.Save(record); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.Sticky(h.App, record))
}

// StickyDestroy serves DELETE /api/workspaces/{slug}/stickies/{pk}/ (soft delete).
func (h *Handler) StickyDestroy(e *core.RequestEvent) error {
	record, err := h.findSticky(e)
	if err != nil {
		return err
	}
	if record == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	record.Set("deleted_at", serializers.Now())
	if err := h.App.Save(record); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// findSticky resolves a non-deleted sticky owned by the caller; another user's
// sticky is a 404, never a 403.
func (h *Handler) findSticky(e *core.RequestEvent) (*core.Record, error) {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return nil, serializers.NotFound(e, "")
	}
	if user == nil {
		return nil, serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	record, err := serializers.FindByUUID(h.App, "stickies", e.Request.PathValue("pk"))
	if err != nil || record == nil {
		return nil, err
	}
	if record.GetString("workspace") != scope.Workspace.Id ||
		record.GetString("owner") != user.Id ||
		record.GetString("deleted_at") != "" {
		return nil, nil
	}
	return record, nil
}

// nextStickySortOrder mirrors Sticky.save(): max(sort_order) + 10000, else 65535.
func (h *Handler) nextStickySortOrder(workspaceID string) float64 {
	var max sql.NullFloat64
	err := h.App.DB().
		NewQuery("SELECT MAX(sort_order) FROM stickies WHERE workspace = {:workspace} AND deleted_at = ''").
		Bind(dbx.Params{"workspace": workspaceID}).
		Row(&max)
	if err != nil || !max.Valid {
		return 65535
	}
	return max.Float64 + 10000
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
