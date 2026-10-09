package workspaces

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// This file implements the workspace quick-link endpoints
// (plane/app/views/workspace/quick_link.py, QuickLinkViewSet):
//
//	GET    /api/workspaces/{slug}/quick-links/
//	POST   /api/workspaces/{slug}/quick-links/
//	GET    /api/workspaces/{slug}/quick-links/{link_id}/
//	PATCH  /api/workspaces/{slug}/quick-links/{link_id}/
//	DELETE /api/workspaces/{slug}/quick-links/{link_id}/
//
// The backing collection is `workspace_user_links` (Django `WorkspaceUserLink`,
// db_table = "workspace_user_links"), which already exists from wave W9 — no new
// migration is needed. Links are owned by the caller: every query is scoped to
// `owner = <current user>`.

// QuickLinks serves GET and POST /api/workspaces/{slug}/quick-links/.
func (h *Handler) QuickLinks(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	if e.Request.Method == http.MethodPost {
		return h.createQuickLink(e, scope, user)
	}

	links, err := h.App.FindRecordsByFilter(
		"workspace_user_links",
		"workspace = {:workspace} && owner = {:owner} && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"workspace": scope.Workspace.Id, "owner": user.Id},
	)
	if err != nil {
		return err
	}
	results := make([]any, 0, len(links))
	for _, link := range links {
		results = append(results, workspaceUserLink(h.App, link))
	}
	return e.JSON(http.StatusOK, results)
}

// QuickLinkDetail serves GET, PATCH and DELETE
// /api/workspaces/{slug}/quick-links/{link_id}/.
func (h *Handler) QuickLinkDetail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	link, _ := serializers.FindByUUID(h.App, "workspace_user_links", e.Request.PathValue("link_id"))
	if link == nil || link.GetString("workspace") != scope.Workspace.Id || link.GetString("owner") != user.Id || link.GetString("deleted_at") != "" {
		return e.JSON(http.StatusNotFound, map[string]any{"detail": "Quick link not found."})
	}

	switch e.Request.Method {
	case http.MethodDelete:
		link.Set("deleted_at", serializers.Now())
		link.Set("updated_by", user.Id)
		if err := h.App.Save(link); err != nil {
			return err
		}
		return e.NoContent(http.StatusNoContent)
	case http.MethodPatch:
		return h.updateQuickLink(e, scope, user, link)
	default:
		return e.JSON(http.StatusOK, workspaceUserLink(h.App, link))
	}
}

// createQuickLink handles POST /api/workspaces/{slug}/quick-links/.
func (h *Handler) createQuickLink(e *core.RequestEvent, scope *middleware.Scope, user *core.Record) error {
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	rawURL := normalizeQuickLinkURL(asString(body["url"]))
	if !isValidQuickLinkURL(rawURL) {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": []string{"Invalid URL format."}})
	}
	if existing, _ := h.App.FindFirstRecordByFilter(
		"workspace_user_links",
		"url = {:url} && workspace = {:workspace} && owner = {:owner} && deleted_at = ''",
		dbx.Params{"url": rawURL, "workspace": scope.Workspace.Id, "owner": user.Id},
	); existing != nil {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": []string{"URL already exists for this workspace and owner"}})
	}

	collection, err := h.App.FindCollectionByNameOrId("workspace_user_links")
	if err != nil {
		return err
	}
	link := core.NewRecord(collection)
	link.Set("uuid", serializers.NewUUID())
	link.Set("workspace", scope.Workspace.Id)
	link.Set("owner", user.Id)
	link.Set("url", rawURL)
	if v, ok := body["title"]; ok {
		link.Set("title", asString(v))
	}
	link.Set("metadata", quickLinkMetadata(body["metadata"]))
	link.Set("created_by", user.Id)
	link.Set("updated_by", user.Id)
	if err := h.App.Save(link); err != nil {
		return serializers.Validation(e, "url", err.Error())
	}
	return e.JSON(http.StatusCreated, workspaceUserLink(h.App, link))
}

// updateQuickLink handles PATCH on a quick link (QuickLinkViewSet.partial_update).
func (h *Handler) updateQuickLink(e *core.RequestEvent, scope *middleware.Scope, user *core.Record, link *core.Record) error {
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	if raw, ok := body["url"]; ok {
		newURL := normalizeQuickLinkURL(asString(raw))
		if !isValidQuickLinkURL(newURL) {
			return e.JSON(http.StatusBadRequest, map[string]any{"error": []string{"Invalid URL format."}})
		}
		if existing, _ := h.App.FindFirstRecordByFilter(
			"workspace_user_links",
			"url = {:url} && workspace = {:workspace} && owner = {:owner} && deleted_at = '' && id != {:id}",
			dbx.Params{"url": newURL, "workspace": scope.Workspace.Id, "owner": user.Id, "id": link.Id},
		); existing != nil {
			return e.JSON(http.StatusBadRequest, map[string]any{"error": []string{"URL already exists for this workspace and owner"}})
		}
		link.Set("url", newURL)
	}
	if v, ok := body["title"]; ok {
		link.Set("title", asString(v))
	}
	if v, ok := body["metadata"]; ok {
		link.Set("metadata", quickLinkMetadata(v))
	}
	link.Set("updated_by", user.Id)
	if err := h.App.Save(link); err != nil {
		return serializers.Validation(e, "url", err.Error())
	}
	return e.JSON(http.StatusOK, workspaceUserLink(h.App, link))
}

// workspaceUserLink mirrors Django's WorkspaceUserLinkSerializer (`fields =
// "__all__"` over the WorkspaceUserLink model). Foreign keys serialise as the
// related record's Plane UUID under the field name (DRF's PrimaryKeyRelatedField
// naming), matching the convention used by the other `__all__` serializers in
// this slice.
func workspaceUserLink(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":         serializers.UUID(r),
		"title":      serializers.NullableText(r, "title"),
		"url":        serializers.Text(r, "url"),
		"metadata":   serializers.JSONOrDefault(r, "metadata", map[string]any{}),
		"owner":      serializers.RelationUUID(app, r, "owner"),
		"workspace":  serializers.RelationUUID(app, r, "workspace"),
		"project":    serializers.RelationUUID(app, r, "project"),
		"created_at": serializers.DateTime(r, "created_at"),
		"updated_at": serializers.DateTime(r, "updated_at"),
		"created_by": serializers.RelationUUID(app, r, "created_by"),
		"updated_by": serializers.RelationUUID(app, r, "updated_by"),
		"deleted_at": serializers.NullableDateTime(r, "deleted_at"),
	}
}

// normalizeQuickLinkURL mirrors the serializer's `to_internal_value`: a URL
// without an explicit scheme is assumed to be http.
func normalizeQuickLinkURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return "http://" + raw
	}
	return raw
}

// isValidQuickLinkURL mirrors the serializer's `validate_url` (DRF's
// URLValidator): a scheme and a host are both required.
func isValidQuickLinkURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return parsed.Scheme != "" && parsed.Host != ""
}

// quickLinkMetadata normalises the JSON metadata column, defaulting to `{}`
// (Django's `default=dict`).
func quickLinkMetadata(v any) any {
	if v == nil {
		return map[string]any{}
	}
	return v
}
