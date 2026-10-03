package asset

import (
	"net/http"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// This file implements Plane's legacy file-asset routes:
//
//	GET/POST/DELETE /api/workspaces/{slug}/file-assets/                          (FileAssetEndpoint)
//	GET/DELETE      /api/workspaces/file-assets/{workspace_id}/{asset_key}/       (FileAssetEndpoint)
//	POST            /api/workspaces/file-assets/{workspace_id}/{asset_key}/restore/ (FileAssetViewSet.restore)
//	GET/POST/DELETE /api/users/file-assets/                                       (UserAssetsEndpoint)
//	GET/DELETE      /api/users/file-assets/{asset_key}/                           (UserAssetsEndpoint)
//
// `asset_key` is the stored value of `file_assets.asset`. In Django that is
// `{workspace_id}/{uuid4}-{name}`; PocketBase manages the directory itself, so
// the stored value is just the filename — we therefore match the supplied key
// both verbatim and with the Django `{workspace_id}/` prefix stripped.

// LegacyWorkspaceList serves GET /api/workspaces/{slug}/file-assets/.
func (h *Handler) LegacyWorkspaceList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	records, err := h.App.FindRecordsByFilter(
		"file_assets",
		"workspace = {:ws} && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"ws": scope.Workspace.Id},
	)
	if err != nil {
		return err
	}
	out := make([]any, 0, len(records))
	for _, r := range records {
		out = append(out, serializers.Asset(h.App, r))
	}
	return e.JSON(http.StatusOK, out)
}

// LegacyWorkspaceCreate serves POST /api/workspaces/{slug}/file-assets/.
func (h *Handler) LegacyWorkspaceCreate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	record, err := h.newLegacyAsset(e, scope.Workspace.Id, "", user.Id)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusCreated, serializers.Asset(h.App, record))
}

// LegacyWorkspaceDelete serves DELETE /api/workspaces/{slug}/file-assets/?asset_key=.
func (h *Handler) LegacyWorkspaceDelete(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	record := h.findByAssetKey(scope.Workspace.Id, e.Request.URL.Query().Get("asset_key"))
	if record == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	record.Set("is_deleted", true)
	if err := h.App.Save(record); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// LegacyWorkspaceDetail serves GET
// /api/workspaces/file-assets/{workspace_id}/{asset_key}/ (FileAssetEndpoint.get).
// It answers 200 with {"data": [...], "status": true} — even for a miss, which
// Plane reports as {"error": "Asset key does not exist", "status": false}.
func (h *Handler) LegacyWorkspaceDetail(e *core.RequestEvent) error {
	workspace, member := h.resolveLegacyWorkspace(e)
	if workspace == nil || member == nil {
		return serializers.NotFound(e, "")
	}
	records := h.findByAssetKeyAll(workspace.Id, e.Request.PathValue("asset_key"))
	if len(records) == 0 {
		return e.JSON(http.StatusOK, map[string]any{"error": "Asset key does not exist", "status": false})
	}
	data := make([]any, 0, len(records))
	for _, r := range records {
		data = append(data, serializers.Asset(h.App, r))
	}
	return e.JSON(http.StatusOK, map[string]any{"data": data, "status": true})
}

// LegacyWorkspaceDetailDelete serves DELETE
// /api/workspaces/file-assets/{workspace_id}/{asset_key}/.
func (h *Handler) LegacyWorkspaceDetailDelete(e *core.RequestEvent) error {
	workspace, member := h.resolveLegacyWorkspace(e)
	if workspace == nil || member == nil {
		return serializers.NotFound(e, "")
	}
	records := h.findByAssetKeyAll(workspace.Id, e.Request.PathValue("asset_key"))
	if len(records) == 0 {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	for _, r := range records {
		r.Set("is_deleted", true)
		if err := h.App.Save(r); err != nil {
			return err
		}
	}
	return e.NoContent(http.StatusNoContent)
}

// LegacyWorkspaceRestore serves POST
// /api/workspaces/file-assets/{workspace_id}/{asset_key}/restore/.
func (h *Handler) LegacyWorkspaceRestore(e *core.RequestEvent) error {
	workspace, member := h.resolveLegacyWorkspace(e)
	if workspace == nil || member == nil {
		return serializers.NotFound(e, "")
	}
	records := h.findByAssetKeyAll(workspace.Id, e.Request.PathValue("asset_key"))
	if len(records) == 0 {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	for _, r := range records {
		r.Set("is_deleted", false)
		if err := h.App.Save(r); err != nil {
			return err
		}
	}
	return e.NoContent(http.StatusNoContent)
}

// LegacyUserList serves GET /api/users/file-assets/ (the caller's own uploads).
func (h *Handler) LegacyUserList(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	records, err := h.App.FindRecordsByFilter(
		"file_assets",
		"created_by = {:user} && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"user": user.Id},
	)
	if err != nil {
		return err
	}
	out := make([]any, 0, len(records))
	for _, r := range records {
		out = append(out, serializers.Asset(h.App, r))
	}
	return e.JSON(http.StatusOK, out)
}

// LegacyUserCreate serves POST /api/users/file-assets/.
func (h *Handler) LegacyUserCreate(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	record, err := h.newLegacyAsset(e, "", user.Id, user.Id)
	if err != nil {
		return err
	}
	return e.JSON(http.StatusCreated, serializers.Asset(h.App, record))
}

// LegacyUserDelete serves DELETE /api/users/file-assets/?asset_key=.
func (h *Handler) LegacyUserDelete(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	record := h.findByAssetKey("", e.Request.URL.Query().Get("asset_key"))
	if record == nil || record.GetString("created_by") != user.Id {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	record.Set("is_deleted", true)
	if err := h.App.Save(record); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// LegacyUserDetail serves GET /api/users/file-assets/{asset_key}/.
func (h *Handler) LegacyUserDetail(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	records := h.findByAssetKeyAll("", e.Request.PathValue("asset_key"))
	filtered := make([]any, 0, len(records))
	for _, r := range records {
		if r.GetString("created_by") == user.Id {
			filtered = append(filtered, serializers.Asset(h.App, r))
		}
	}
	if len(filtered) == 0 {
		return e.JSON(http.StatusOK, map[string]any{"error": "Asset key does not exist", "status": false})
	}
	return e.JSON(http.StatusOK, map[string]any{"data": filtered, "status": true})
}

// LegacyUserDetailDelete serves DELETE /api/users/file-assets/{asset_key}/.
func (h *Handler) LegacyUserDetailDelete(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	records := h.findByAssetKeyAll("", e.Request.PathValue("asset_key"))
	found := false
	for _, r := range records {
		if r.GetString("created_by") != user.Id {
			continue
		}
		r.Set("is_deleted", true)
		if err := h.App.Save(r); err != nil {
			return err
		}
		found = true
	}
	if !found {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	return e.NoContent(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// legacy helpers
// ---------------------------------------------------------------------------

// newLegacyAsset creates a FileAsset row from a multipart (file field `asset`)
// or JSON request, returning the saved record.
func (h *Handler) newLegacyAsset(e *core.RequestEvent, workspaceID, userID, createdBy string) (*core.Record, error) {
	body, _ := bindBody(e)
	name := sanitizeFilename(asString(body["name"]))
	if name == "" {
		name = "unnamed"
	}
	fileType := asString(body["type"])
	if fileType == "" {
		fileType = "application/octet-stream"
	}
	collection, err := h.App.FindCollectionByNameOrId("file_assets")
	if err != nil {
		return nil, err
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("attributes", map[string]any{"name": name, "type": fileType})
	record.Set("entity_type", asString(body["entity_type"]))
	record.Set("entity_identifier", asString(body["entity_identifier"]))
	if workspaceID != "" {
		record.Set("workspace", workspaceID)
	}
	if userID != "" {
		record.Set("user", userID)
	}
	if createdBy != "" {
		record.Set("created_by", createdBy)
	}

	file, ferr := h.legacyUpload(e)
	if ferr == nil && file != nil {
		if file.Size > fileSizeLimit {
			return nil, e.JSON(http.StatusBadRequest, map[string]any{"error": "File too large. Size should not exceed 5 MB."})
		}
		record.Set(serializers.AssetFieldName, file)
		record.Set("size", float64(file.Size))
		record.Set("is_uploaded", true)
		if err := h.App.Save(record); err != nil {
			return nil, serializers.Validation(e, "detail", err.Error())
		}
		return record, nil
	}
	if err := h.App.SaveNoValidate(record); err != nil {
		return nil, serializers.Validation(e, "detail", err.Error())
	}
	return record, nil
}

// legacyUpload reads the `asset` file field (Django's FileAssetSerializer
// field name) or a `file` field.
func (h *Handler) legacyUpload(e *core.RequestEvent) (*filesystem.File, error) {
	for _, key := range []string{"asset", "file"} {
		if files, err := e.FindUploadedFiles(key); err == nil && len(files) > 0 {
			return files[0], nil
		}
	}
	return nil, http.ErrMissingFile
}

// resolveLegacyWorkspace resolves `{workspace_id}` (Plane UUID or PocketBase id)
// and requires an active membership, mirroring WorkspaceMemberPermission.
func (h *Handler) resolveLegacyWorkspace(e *core.RequestEvent) (*core.Record, *core.Record) {
	user := middleware.CurrentUser(e)
	if user == nil {
		return nil, nil
	}
	workspace, _ := serializers.FindByUUIDOrID(h.App, "workspaces", e.Request.PathValue("workspace_id"))
	if workspace == nil {
		return nil, nil
	}
	member, err := h.App.FindFirstRecordByFilter(
		"workspace_members",
		"workspace = {:ws} && member = {:member} && is_active = true",
		dbx.Params{"ws": workspace.Id, "member": user.Id},
	)
	if err != nil || member == nil {
		return workspace, nil
	}
	return workspace, member
}

// findByAssetKey resolves a single asset by its stored key within a workspace
// (workspaceID "" skips the workspace scope).
func (h *Handler) findByAssetKey(workspaceID, assetKey string) *core.Record {
	records := h.findByAssetKeyAll(workspaceID, assetKey)
	if len(records) == 0 {
		return nil
	}
	return records[0]
}

// findByAssetKeyAll matches `asset` verbatim and with the Django
// `{workspace_id}/` prefix stripped.
func (h *Handler) findByAssetKeyAll(workspaceID, assetKey string) []*core.Record {
	if assetKey == "" {
		return nil
	}
	candidates := []string{assetKey}
	if idx := lastIndexByte(assetKey, '/'); idx >= 0 {
		candidates = append(candidates, assetKey[idx+1:])
	}
	seen := map[string]bool{}
	out := make([]*core.Record, 0, 1)
	for _, key := range candidates {
		filter := "asset = {:key} && deleted_at = ''"
		params := dbx.Params{"key": key}
		if workspaceID != "" {
			filter += " && workspace = {:ws}"
			params["ws"] = workspaceID
		}
		records, err := h.App.FindRecordsByFilter("file_assets", filter, "-created_at", 0, 0, params)
		if err != nil {
			continue
		}
		for _, r := range records {
			if !seen[r.Id] {
				seen[r.Id] = true
				out = append(out, r)
			}
		}
	}
	return out
}

// lastIndexByte is a tiny strings.LastIndexByte shim kept local so this file
// stays free of a strings import.
func lastIndexByte(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}
