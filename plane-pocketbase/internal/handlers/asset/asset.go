// Package asset implements Plane's file/asset domain:
//
//	POST          /api/assets/v2/workspaces/{slug}/                     (WorkspaceFileAssetEndpoint.post)
//	GET/PATCH/DELETE /api/assets/v2/workspaces/{slug}/{asset_id}/        (WorkspaceFileAssetEndpoint)
//	POST          /api/assets/v2/user-assets/                            (UserAssetsV2Endpoint.post)
//	PATCH/DELETE  /api/assets/v2/user-assets/{asset_id}/                 (UserAssetsV2Endpoint)
//	POST          /api/assets/v2/workspaces/{slug}/restore/{asset_id}/   (AssetRestoreEndpoint)
//	GET           /api/assets/v2/static/{asset_id}/                      (StaticFileAssetEndpoint)
//	POST          /api/assets/v2/workspaces/{slug}/projects/{project_id}/            (ProjectAssetEndpoint.post)
//	GET/PATCH/DELETE /api/assets/v2/workspaces/{slug}/projects/{project_id}/{pk}/    (ProjectAssetEndpoint)
//	POST          /api/assets/v2/workspaces/{slug}/projects/{project_id}/{entity_id}/bulk/  (ProjectBulkAssetEndpoint)
//	GET           /api/assets/v2/workspaces/{slug}/check/{asset_id}/     (AssetCheckEndpoint)
//	POST          /api/assets/v2/workspaces/{slug}/duplicate-assets/{asset_id}/     (DuplicateAssetEndpoint)
//	GET           /api/assets/v2/workspaces/{slug}/download/{asset_id}/             (WorkspaceAssetDownloadEndpoint)
//	GET           /api/assets/v2/workspaces/{slug}/projects/{project_id}/download/{asset_id}/ (ProjectAssetDownloadEndpoint)
//
// plus the v2 issue-attachment endpoints (group `issue` in the spec, but they
// live under the /api/assets/v2/ prefix this file owns) and the legacy
// `/api/workspaces/{slug}/file-assets/…` / `/api/users/file-assets/…` routes.
//
// # URL scheme
//
// PocketBase stores files on disk and exposes them at
// `/api/files/{collectionId}/{recordId}/{filename}`. That is a PocketBase URL
// shape Plane's frontends never build, and `file_assets.asset` is `protected`
// so PB's own file route additionally demands a short-lived file token. We
// therefore emit Plane's own `asset_url` shape (the four `FileAsset.asset_url`
// variants) and register handlers for each of those shapes that STREAM the
// stored bytes straight from `app.NewFilesystem()`. Django 302-redirects to a
// presigned S3 URL; a 200-with-bytes is the direct equivalent for a client that
// just follows the URL, and it works with the session cookie.
//
// # Upload handshake
//
// Plane's frontends POST metadata, receive `upload_data = {url, fields}` for an
// S3 presigned POST, upload the bytes there, then PATCH to mark the asset
// uploaded. There is no S3 here, so `upload_data.url` points at our own
// receiver `/api/assets/v2/upload/{asset_id}/`, which accepts the multipart
// `file` field (or a raw body) and writes it into the PocketBase file column.
package asset

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/types"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// fileSizeLimit mirrors Plane's settings.FILE_SIZE_LIMIT (5 MB default).
const fileSizeLimit = 5242880

// allowedImageTypes mirrors the allowlist the v2 workspace/user uploads enforce.
var allowedImageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/jpg":  true,
	"image/gif":  true,
}

// allowedAttachmentTypes mirrors settings.ATTACHMENT_MIME_TYPES.
var allowedAttachmentTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true,
	"image/svg+xml": true, "image/webp": true, "image/tiff": true, "image/bmp": true,
	"application/pdf": true, "application/msword": true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
	"application/vnd.ms-excel": true,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         true,
	"application/vnd.ms-powerpoint":                                             true,
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": true,
	"text/plain": true, "text/csv": true, "application/zip": true,
	"application/json": true, "application/octet-stream": true,
}

// scriptCapableTypes must always be served as an attachment (settings
// SCRIPT_CAPABLE_MIME_TYPES) to avoid same-origin XSS.
var scriptCapableTypes = map[string]bool{
	"image/svg+xml": true, "text/javascript": true, "application/javascript": true,
	"text/html": true, "application/xhtml+xml": true, "text/xml": true, "application/xml": true,
}

// Handler holds the app dependency for the asset routes.
type Handler struct {
	App core.App
}

// New returns an asset handler bound to app.
func New(app core.App) *Handler {
	return &Handler{App: app}
}

// ---------------------------------------------------------------------------
// v2 metadata uploads
// ---------------------------------------------------------------------------

// WorkspaceUpload serves POST /api/assets/v2/workspaces/{slug}/
// (WorkspaceFileAssetEndpoint.post). It creates the FileAsset row and returns
// the upload handshake. Guests may upload (Django allows ADMIN/MEMBER/GUEST).
func (h *Handler) WorkspaceUpload(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	body, err := bindBody(e)
	if err != nil {
		return err
	}
	return h.createAsset(e, createInput{
		workspaceID: scope.Workspace.Id,
		createdBy:   user.Id,
		body:        body,
	})
}

// ProjectUpload serves POST /api/assets/v2/workspaces/{slug}/projects/{project_id}/
// (ProjectAssetEndpoint.post).
func (h *Handler) ProjectUpload(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	body, err := bindBody(e)
	if err != nil {
		return err
	}
	return h.createAsset(e, createInput{
		workspaceID: scope.Workspace.Id,
		projectID:   scope.Project.Id,
		createdBy:   user.Id,
		body:        body,
	})
}

// UserUpload serves POST /api/assets/v2/user-assets/ (UserAssetsV2Endpoint.post).
// Only USER_AVATAR / USER_COVER are accepted, and only image MIME types.
func (h *Handler) UserUpload(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	body, err := bindBody(e)
	if err != nil {
		return err
	}
	entityType := asString(body["entity_type"])
	if entityType != serializers.EntityUserAvatar && entityType != serializers.EntityUserCover {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Invalid entity type.", "status": false})
	}
	return h.createAsset(e, createInput{
		userID:    user.Id,
		createdBy: user.Id,
		body:      body,
	})
}

// ProjectSubresource dispatches the two POST routes that share the
// `.../projects/{project_id}/{entity_id}/{action}/` shape:
//
//	POST …/{entity_id}/bulk/                            → Bulk
//	POST …/issues/{issue_id}/attachments/               → AttachmentUpload
//
// They cannot be registered separately because both are stdlib-mux subtree
// patterns that overlap (`issues/bulk/attachments/` matches both).
func (h *Handler) ProjectSubresource(e *core.RequestEvent) error {
	path := strings.TrimSuffix(e.Request.URL.Path, "/")
	if strings.HasSuffix(path, "/attachments") {
		return h.attachmentUpload(e, e.Request.PathValue("action"))
	}
	if e.Request.PathValue("action") == "bulk" {
		return h.Bulk(e)
	}
	return serializers.NotFound(e, "")
}

// AttachmentUpload serves POST
// /api/assets/v2/workspaces/{slug}/projects/{project_id}/issues/{issue_id}/attachments/
// (IssueAttachmentV2Endpoint.post). Attachments accept the wider
// ATTACHMENT_MIME_TYPES allowlist and always carry ISSUE_ATTACHMENT.
func (h *Handler) AttachmentUpload(e *core.RequestEvent) error {
	return h.attachmentUpload(e, e.Request.PathValue("issue_id"))
}

// attachmentUpload is AttachmentUpload with an explicit issue id, so the shared
// ProjectSubresource dispatcher can pass it through.
func (h *Handler) attachmentUpload(e *core.RequestEvent, issueID string) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	issue, err := serializers.FindByUUID(h.App, "issues", issueID)
	if err != nil || issue == nil || issue.GetString("project") != scope.Project.Id {
		return serializers.NotFound(e, "")
	}
	body, err := bindBody(e)
	if err != nil {
		return err
	}
	return h.createAsset(e, createInput{
		workspaceID: scope.Workspace.Id,
		projectID:   scope.Project.Id,
		issueID:     issue.Id,
		createdBy:   user.Id,
		entityType:  serializers.EntityIssueAttachment,
		attach:      true,
		body:        body,
	})
}

// createInput carries the already-resolved context for an asset creation.
type createInput struct {
	workspaceID string
	projectID   string
	issueID     string
	userID      string
	createdBy   string
	entityType  string // forced entity type (attachments); else read from body
	attach      bool   // attachment MIME allowlist instead of the image one
	body        map[string]any
}

// createAsset validates the request and creates the FileAsset metadata row,
// returning Plane's {upload_data, asset_id, asset_url} handshake. The row is
// created without bytes (is_uploaded=false) — exactly like Django, where the
// file only lands after the S3 POST — so validation is skipped for the
// required `asset` column.
func (h *Handler) createAsset(e *core.RequestEvent, in createInput) error {
	body := in.body
	name := sanitizeFilename(asString(body["name"]))
	if name == "" {
		name = "unnamed"
	}
	fileType := asString(body["type"])
	if fileType == "" {
		fileType = "image/jpeg"
	}
	size := fileSizeLimit
	if raw, ok := body["size"]; ok {
		size = toInt(raw, fileSizeLimit)
	}
	entityType := in.entityType
	if entityType == "" {
		entityType = asString(body["entity_type"])
	}
	if !serializers.ValidEntityType(entityType) {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Invalid entity type.", "status": false})
	}

	// WORKSPACE_LOGO may only be uploaded by workspace admins.
	if entityType == serializers.EntityWorkspaceLogo {
		scope := middleware.ScopeOf(e)
		if scope == nil || scope.WorkspaceRole < middleware.RoleAdmin {
			return e.JSON(http.StatusForbidden, map[string]any{"error": "Only workspace admins can upload a workspace logo."})
		}
	}

	if in.attach {
		if !allowedAttachmentTypes[fileType] {
			return e.JSON(http.StatusBadRequest, map[string]any{"error": "Invalid file type.", "status": false})
		}
	} else if !allowedImageTypes[fileType] {
		return e.JSON(http.StatusBadRequest, map[string]any{
			"error":  "Invalid file type. Only JPEG, PNG, WebP, JPG and GIF files are allowed.",
			"status": false,
		})
	}

	sizeLimit := size
	if sizeLimit > fileSizeLimit {
		sizeLimit = fileSizeLimit
	}

	collection, err := h.App.FindCollectionByNameOrId("file_assets")
	if err != nil {
		return err
	}
	record := core.NewRecord(collection)
	record.Set("uuid", serializers.NewUUID())
	record.Set("attributes", map[string]any{"name": name, "type": fileType, "size": sizeLimit})
	record.Set("size", float64(sizeLimit))
	record.Set("entity_type", entityType)
	record.Set("entity_identifier", asString(body["entity_identifier"]))
	record.Set("is_uploaded", false)
	record.Set("is_deleted", false)
	if in.workspaceID != "" {
		record.Set("workspace", in.workspaceID)
	}
	if in.projectID != "" {
		record.Set("project", in.projectID)
	}
	if in.issueID != "" {
		record.Set("issue", in.issueID)
	}
	if in.userID != "" {
		record.Set("user", in.userID)
	}
	if in.createdBy != "" {
		record.Set("created_by", in.createdBy)
	}
	// Map entity_identifier onto its relation column (Django's
	// get_entity_id_field), when it resolves.
	if entityIdentifier := asString(body["entity_identifier"]); entityIdentifier != "" {
		if field, id := h.entityRelation(entityType, entityIdentifier); field != "" && id != "" {
			record.Set(field, id)
		}
	}
	// A file posted alongside the metadata (our upload receiver folded in) is
	// stored immediately.
	if file, err := h.firstUploadedFile(e); err == nil && file != nil {
		if file.Size > fileSizeLimit {
			return e.JSON(http.StatusBadRequest, map[string]any{"error": "File too large. Size should not exceed 5 MB."})
		}
		record.Set(serializers.AssetFieldName, file)
		record.Set("size", float64(file.Size))
		record.Set("is_uploaded", true)
	}

	// `asset` is required but absent until the upload lands; skip validation.
	if err := h.App.SaveNoValidate(record); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}

	payload := map[string]any{
		"upload_data": map[string]any{
			"url":    absoluteURL(e, "/api/assets/v2/upload/"+serializers.UUID(record)+"/"),
			"fields": map[string]any{"key": serializers.UUID(record)},
		},
		"asset_id":  serializers.UUID(record),
		"asset_url": serializers.AssetURL(h.App, record),
	}
	// Only the issue-attachment endpoint adds this extra key
	// (IssueAttachmentV2Endpoint, plane/app/views/issue/attachment.py:139-145);
	// the generic workspace/project/user uploads do not. The frontend's
	// uploadIssueAttachment returns `signedURLResponse.attachment` to its caller,
	// so omitting it here made the freshly created attachment come back as
	// undefined in the UI.
	if in.attach {
		payload["attachment"] = serializers.Asset(h.App, record)
	}
	return e.JSON(http.StatusOK, payload)
}

// Upload receives the bytes for a metadata row created by createAsset. It is our
// stand-in for the S3 presigned POST: accept multipart `file` (S3 style) or a
// raw request body, enforce FILE_SIZE_LIMIT, store into the PocketBase file
// column and mark the asset uploaded.
// Upload serves POST /api/assets/v2/upload/{asset_id}/ — the stand-in for the
// S3 presigned POST.
//
// This endpoint is deliberately UNAUTHENTICATED. Plane's frontends upload the
// bytes with `withCredentials: false` (apps/web/services/file-upload.service.ts)
// because a real presigned URL carries its own signature rather than a session
// cookie. Requiring a session here returns 401 and silently aborts every
// cover-image / avatar / attachment upload.
//
// The capability is the URL itself: the asset row can only be created through
// the authenticated `POST /api/assets/v2/workspaces/{slug}/` handshake, and its
// id is an unguessable UUID. A leaked id can therefore only write bytes into an
// asset that its owner already created — the same blast radius as a leaked
// presigned URL. The follow-up PATCH that marks the asset uploaded is
// authenticated as usual.
func (h *Handler) Upload(e *core.RequestEvent) error {
	asset, err := h.findAsset(e.Request.PathValue("asset_id"))
	if err != nil {
		return err
	}
	if asset == nil || asset.GetBool("is_deleted") {
		return serializers.NotFound(e, "The requested asset could not be found.")
	}
	// Never let an anonymous caller overwrite bytes that are already committed.
	if asset.GetBool("is_uploaded") {
		return serializers.Forbidden(e, "This asset has already been uploaded.")
	}

	file, err := h.readUpload(e, asset)
	if err != nil {
		return serializers.BadRequest(e, "No file provided.")
	}
	if file.Size > fileSizeLimit {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "File too large. Size should not exceed 5 MB."})
	}

	asset.Set(serializers.AssetFieldName, file)
	asset.Set("size", float64(file.Size))
	asset.Set("is_uploaded", true)
	asset.Set("storage_metadata", map[string]any{"size": file.Size, "content_type": fileTypeOf(file)})
	if err := h.App.Save(asset); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, map[string]any{
		"asset_id":  serializers.UUID(asset),
		"asset_url": serializers.AssetURL(h.App, asset),
		"asset":     serializers.Asset(h.App, asset),
	})
}

// ---------------------------------------------------------------------------
// v2 detail / patch / delete
// ---------------------------------------------------------------------------

// WorkspaceAssetGet serves GET /api/assets/v2/workspaces/{slug}/{asset_id}/ —
// streams the bytes with attachment disposition.
func (h *Handler) WorkspaceAssetGet(e *core.RequestEvent) error {
	asset, err := h.scopedWorkspaceAsset(e)
	if err != nil || asset == nil {
		return err
	}
	if !asset.GetBool("is_uploaded") {
		return serializers.NotFound(e, "The requested asset could not be found.")
	}
	return h.streamAsset(e, asset, "attachment")
}

// WorkspaceAssetPatch serves PATCH /api/assets/v2/workspaces/{slug}/{asset_id}/:
// marks the asset uploaded and applies the entity side-effects (workspace logo /
// project cover). 204.
func (h *Handler) WorkspaceAssetPatch(e *core.RequestEvent) error {
	asset, err := h.scopedWorkspaceAsset(e)
	if err != nil || asset == nil {
		return err
	}
	body, _ := bindBody(e)
	asset.Set("is_uploaded", true)
	if attrs, ok := body["attributes"]; ok {
		asset.Set("attributes", attrs)
	}
	h.entityAssetSave(asset)
	if err := h.App.Save(asset); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.NoContent(http.StatusNoContent)
}

// WorkspaceAssetDelete serves DELETE /api/assets/v2/workspaces/{slug}/{asset_id}/:
// soft-delete + entity side-effects. 204.
func (h *Handler) WorkspaceAssetDelete(e *core.RequestEvent) error {
	asset, err := h.scopedWorkspaceAsset(e)
	if err != nil || asset == nil {
		return err
	}
	asset.Set("is_deleted", true)
	asset.Set("deleted_at", serializers.Now())
	h.entityAssetDelete(asset)
	if err := h.App.Save(asset); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// UserAssetPatch serves PATCH /api/assets/v2/user-assets/{asset_id}/.
func (h *Handler) UserAssetPatch(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	asset, err := h.findAsset(e.Request.PathValue("asset_id"))
	if err != nil || asset == nil || asset.GetString("user") != user.Id {
		return serializers.NotFound(e, "The requested asset could not be found.")
	}
	body, _ := bindBody(e)
	asset.Set("is_uploaded", true)
	if attrs, ok := body["attributes"]; ok {
		asset.Set("attributes", attrs)
	}
	h.entityAssetSave(asset)
	if err := h.App.Save(asset); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.NoContent(http.StatusNoContent)
}

// UserAssetDelete serves DELETE /api/assets/v2/user-assets/{asset_id}/.
func (h *Handler) UserAssetDelete(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	asset, err := h.findAsset(e.Request.PathValue("asset_id"))
	if err != nil || asset == nil || asset.GetString("user") != user.Id {
		return serializers.NotFound(e, "The requested asset could not be found.")
	}
	asset.Set("is_deleted", true)
	asset.Set("deleted_at", serializers.Now())
	h.entityAssetDelete(asset)
	if err := h.App.Save(asset); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// ProjectAssetGet serves GET
// /api/assets/v2/workspaces/{slug}/projects/{project_id}/{pk}/ — the URL shape
// description assets use; streams with attachment disposition.
func (h *Handler) ProjectAssetGet(e *core.RequestEvent) error {
	asset, err := h.scopedProjectAsset(e, e.Request.PathValue("pk"))
	if err != nil || asset == nil {
		return err
	}
	if !asset.GetBool("is_uploaded") {
		return serializers.NotFound(e, "The requested asset could not be found.")
	}
	return h.streamAsset(e, asset, "attachment")
}

// ProjectAssetPatch serves PATCH …/projects/{project_id}/{pk}/.
func (h *Handler) ProjectAssetPatch(e *core.RequestEvent) error {
	asset, err := h.scopedProjectAsset(e, e.Request.PathValue("pk"))
	if err != nil || asset == nil {
		return err
	}
	body, _ := bindBody(e)
	asset.Set("is_uploaded", true)
	if attrs, ok := body["attributes"]; ok {
		asset.Set("attributes", attrs)
	}
	if err := h.App.Save(asset); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.NoContent(http.StatusNoContent)
}

// ProjectAssetDelete serves DELETE …/projects/{project_id}/{pk}/.
func (h *Handler) ProjectAssetDelete(e *core.RequestEvent) error {
	asset, err := h.scopedProjectAsset(e, e.Request.PathValue("pk"))
	if err != nil || asset == nil {
		return err
	}
	asset.Set("is_deleted", true)
	asset.Set("deleted_at", serializers.Now())
	if err := h.App.Save(asset); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// v2 serving endpoints
// ---------------------------------------------------------------------------

// StaticServe serves GET /api/assets/v2/static/{asset_id}/ (AllowAny in Django).
// Only the four "static" entity types are allowed; script-capable types are
// forced to an attachment disposition.
func (h *Handler) StaticServe(e *core.RequestEvent) error {
	asset, err := h.findAsset(e.Request.PathValue("asset_id"))
	if err != nil {
		return err
	}
	if asset == nil || asset.GetBool("is_deleted") {
		return serializers.NotFound(e, "The requested asset could not be found.")
	}
	switch asset.GetString("entity_type") {
	case serializers.EntityUserAvatar, serializers.EntityUserCover,
		serializers.EntityWorkspaceLogo, serializers.EntityProjectCover:
	default:
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Invalid entity type.", "status": false})
	}
	if !asset.GetBool("is_uploaded") {
		return serializers.NotFound(e, "The requested asset could not be found.")
	}
	disposition := "inline"
	if scriptCapableTypes[strings.ToLower(mimeOf(asset))] {
		disposition = "attachment"
	}
	return h.streamAsset(e, asset, disposition)
}

// Download serves GET /api/assets/v2/workspaces/{slug}/download/{asset_id}/.
func (h *Handler) Download(e *core.RequestEvent) error {
	asset, err := h.scopedWorkspaceAsset(e)
	if err != nil || asset == nil {
		return err
	}
	if !asset.GetBool("is_uploaded") {
		return serializers.NotFound(e, "The requested asset could not be found.")
	}
	return h.streamAsset(e, asset, "attachment")
}

// ProjectDownload serves GET
// /api/assets/v2/workspaces/{slug}/projects/{project_id}/download/{asset_id}/.
func (h *Handler) ProjectDownload(e *core.RequestEvent) error {
	asset, err := h.scopedProjectAsset(e, e.Request.PathValue("asset_id"))
	if err != nil || asset == nil {
		return err
	}
	if !asset.GetBool("is_uploaded") {
		return serializers.NotFound(e, "The requested asset could not be found.")
	}
	return h.streamAsset(e, asset, "attachment")
}

// Check serves GET /api/assets/v2/workspaces/{slug}/check/{asset_id}/ →
// {"exists": bool}. A soft-deleted asset reports false.
func (h *Handler) Check(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	asset, err := h.findAsset(e.Request.PathValue("asset_id"))
	if err != nil {
		return err
	}
	exists := asset != nil &&
		asset.GetString("workspace") == scope.Workspace.Id &&
		asset.GetString("deleted_at") == ""
	return e.JSON(http.StatusOK, map[string]any{"exists": exists})
}

// Restore serves POST /api/assets/v2/workspaces/{slug}/restore/{asset_id}/.
func (h *Handler) Restore(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	asset, err := h.findAsset(e.Request.PathValue("asset_id"))
	if err != nil || asset == nil || asset.GetString("workspace") != scope.Workspace.Id {
		return serializers.NotFound(e, "The requested asset could not be found.")
	}
	asset.Set("is_deleted", false)
	asset.Set("deleted_at", nil)
	if err := h.App.Save(asset); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// Duplicate serves POST
// /api/assets/v2/workspaces/{slug}/duplicate-assets/{asset_id}/ → {"asset_id"}.
func (h *Handler) Duplicate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	body, _ := bindBody(e)
	entityType := asString(body["entity_type"])
	if !serializers.ValidEntityType(entityType) {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Invalid entity type or entity id"})
	}

	var projectID string
	if pid := asString(body["project_id"]); pid != "" {
		project, _ := serializers.FindByUUID(h.App, "projects", pid)
		if project == nil || project.GetString("workspace") != scope.Workspace.Id {
			return serializers.NotFound(e, "Project not found")
		}
		projectID = project.Id
	}

	original, err := h.findAsset(e.Request.PathValue("asset_id"))
	if err != nil || original == nil ||
		!original.GetBool("is_uploaded") || original.GetString("workspace") != scope.Workspace.Id {
		return serializers.NotFound(e, "Asset not found")
	}

	collection, err := h.App.FindCollectionByNameOrId("file_assets")
	if err != nil {
		return err
	}
	duplicate := core.NewRecord(collection)
	duplicate.Set("uuid", serializers.NewUUID())
	duplicate.Set("attributes", original.Get("attributes"))
	duplicate.Set("size", original.GetFloat("size"))
	duplicate.Set("workspace", scope.Workspace.Id)
	duplicate.Set("created_by", user.Id)
	duplicate.Set("entity_type", entityType)
	duplicate.Set("is_uploaded", true)
	duplicate.Set("storage_metadata", original.Get("storage_metadata"))
	if projectID != "" {
		duplicate.Set("project", projectID)
	}
	if entityID := asString(body["entity_id"]); entityID != "" {
		if field, id := h.entityRelation(entityType, entityID); field != "" && id != "" {
			duplicate.Set(field, id)
		}
	}

	// Copy the bytes via the filesystem (S3Storage.copy_object equivalent).
	srcName := original.GetString(serializers.AssetFieldName)
	if srcName != "" {
		fsys, ferr := h.App.NewFilesystem()
		if ferr != nil {
			return ferr
		}
		defer fsys.Close()
		reupload, rerr := fsys.GetReuploadableFile(original.BaseFilesPath()+"/"+srcName, false)
		if rerr == nil && reupload != nil {
			duplicate.Set(serializers.AssetFieldName, reupload)
		}
	}
	if err := h.App.Save(duplicate); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, map[string]any{"asset_id": serializers.UUID(duplicate)})
}

// Bulk serves POST
// /api/assets/v2/workspaces/{slug}/projects/{project_id}/{entity_id}/bulk/
// (ProjectBulkAssetEndpoint). It associates freshly-uploaded assets with their
// entity. 204.
func (h *Handler) Bulk(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	body, _ := bindBody(e)
	ids := stringSlice(body["asset_ids"])
	if len(ids) == 0 {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "No asset ids provided."})
	}
	entityID := e.Request.PathValue("entity_id")

	// Scope to the caller's own uploads in this workspace that are either
	// unassociated or already in this project.
	filter := "workspace = {:ws} && created_by = {:user} && (project = {:project} || project = '') && uuid = {:uuid}"
	for _, assetUUID := range ids {
		asset, err := h.App.FindFirstRecordByFilter("file_assets", filter, dbx.Params{
			"ws": scope.Workspace.Id, "user": user.Id, "project": scope.Project.Id, "uuid": assetUUID,
		})
		if err != nil || asset == nil {
			continue
		}
		switch asset.GetString("entity_type") {
		case serializers.EntityProjectCover:
			asset.Set("project", scope.Project.Id)
			h.setProjectCover(scope.Project, asset)
		case serializers.EntityIssueDescription:
			if id := h.relationID("issues", entityID); id != "" {
				asset.Set("issue", id)
				asset.Set("project", scope.Project.Id)
			}
		case serializers.EntityCommentDescription:
			if id := h.relationID("issue_comments", entityID); id != "" {
				asset.Set("comment", id)
			}
		case serializers.EntityPageDescription:
			if id := h.relationID("pages", entityID); id != "" {
				asset.Set("page", id)
			}
		case serializers.EntityDraftIssueDesc:
			if id := h.relationID("draft_issues", entityID); id != "" {
				asset.Set("draft_issue", id)
			}
		}
		if err := h.App.Save(asset); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
	}
	return e.NoContent(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// issue-attachment v2 (group `issue`, under the /api/assets/v2/ prefix)
// ---------------------------------------------------------------------------

// AttachmentList serves GET
// /api/assets/v2/workspaces/{slug}/projects/{project_id}/issues/{issue_id}/attachments/
// — the uploaded attachments for an issue.
func (h *Handler) AttachmentList(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	issue, _ := serializers.FindByUUID(h.App, "issues", e.Request.PathValue("issue_id"))
	if issue == nil || issue.GetString("project") != scope.Project.Id {
		return serializers.NotFound(e, "")
	}
	records, err := h.App.FindRecordsByFilter(
		"file_assets",
		"issue = {:issue} && entity_type = {:type} && is_uploaded = true && is_deleted = false",
		"-created_at", 0, 0,
		dbx.Params{"issue": issue.Id, "type": serializers.EntityIssueAttachment},
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

// AttachmentGet serves GET …/issues/{issue_id}/attachments/{pk}/ — streams the
// attachment bytes.
func (h *Handler) AttachmentGet(e *core.RequestEvent) error {
	asset, err := h.scopedAttachment(e, e.Request.PathValue("pk"))
	if err != nil || asset == nil {
		return err
	}
	if !asset.GetBool("is_uploaded") {
		return serializers.BadRequest(e, "The asset is not uploaded.")
	}
	return h.streamAsset(e, asset, "attachment")
}

// AttachmentPatch serves PATCH …/attachments/{pk}/ (marks uploaded). 204.
func (h *Handler) AttachmentPatch(e *core.RequestEvent) error {
	asset, err := h.scopedAttachment(e, e.Request.PathValue("pk"))
	if err != nil || asset == nil {
		return err
	}
	asset.Set("is_uploaded", true)
	if err := h.App.Save(asset); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.NoContent(http.StatusNoContent)
}

// AttachmentDelete serves DELETE …/attachments/{pk}/ — soft-delete. 204.
func (h *Handler) AttachmentDelete(e *core.RequestEvent) error {
	asset, err := h.scopedAttachment(e, e.Request.PathValue("pk"))
	if err != nil || asset == nil {
		return err
	}
	asset.Set("is_deleted", true)
	asset.Set("deleted_at", serializers.Now())
	if err := h.App.Save(asset); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

// findAsset resolves a Plane UUID or a PocketBase id to a file_assets record.
func (h *Handler) findAsset(idOrUUID string) (*core.Record, error) {
	return serializers.FindByUUIDOrID(h.App, "file_assets", idOrUUID)
}

// scopedWorkspaceAsset resolves the `{asset_id}` path value within the resolved
// workspace, applying Django's project-access guard for project-bound assets.
func (h *Handler) scopedWorkspaceAsset(e *core.RequestEvent) (*core.Record, error) {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return nil, serializers.NotFound(e, "")
	}
	asset, err := h.findAsset(e.Request.PathValue("asset_id"))
	if err != nil {
		return nil, err
	}
	if asset == nil || asset.GetString("workspace") != scope.Workspace.Id {
		return nil, serializers.NotFound(e, "The requested asset could not be found.")
	}
	if !h.hasProjectAssetAccess(e, asset) {
		return nil, e.JSON(http.StatusForbidden, map[string]any{"error": "You don't have access to this asset."})
	}
	return asset, nil
}

// scopedProjectAsset resolves an asset bound to the resolved project.
func (h *Handler) scopedProjectAsset(e *core.RequestEvent, idOrUUID string) (*core.Record, error) {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return nil, serializers.NotFound(e, "")
	}
	asset, err := h.findAsset(idOrUUID)
	if err != nil {
		return nil, err
	}
	if asset == nil ||
		asset.GetString("workspace") != scope.Workspace.Id ||
		asset.GetString("project") != scope.Project.Id ||
		asset.GetString("deleted_at") != "" {
		return nil, serializers.NotFound(e, "The requested asset could not be found.")
	}
	return asset, nil
}

// scopedAttachment resolves an issue attachment bound to the resolved project
// and the `{issue_id}` path value.
func (h *Handler) scopedAttachment(e *core.RequestEvent, idOrUUID string) (*core.Record, error) {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return nil, serializers.NotFound(e, "")
	}
	issue, _ := serializers.FindByUUID(h.App, "issues", e.Request.PathValue("issue_id"))
	if issue == nil || issue.GetString("project") != scope.Project.Id {
		return nil, serializers.NotFound(e, "")
	}
	asset, err := h.findAsset(idOrUUID)
	if err != nil {
		return nil, err
	}
	if asset == nil ||
		asset.GetString("issue") != issue.Id ||
		asset.GetString("project") != scope.Project.Id ||
		asset.GetString("deleted_at") != "" {
		return nil, serializers.NotFound(e, "The requested asset could not be found.")
	}
	return asset, nil
}

// hasProjectAssetAccess ports WorkspaceFileAssetEndpoint.has_project_asset_access:
// project-bound assets need an active project membership.
func (h *Handler) hasProjectAssetAccess(e *core.RequestEvent, asset *core.Record) bool {
	projectID := asset.GetString("project")
	if projectID == "" {
		return true
	}
	scope := middleware.ScopeOf(e)
	if scope != nil && scope.Project != nil && scope.Project.Id == projectID {
		return true
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return false
	}
	member, err := h.App.FindFirstRecordByFilter(
		"project_members",
		"project = {:project} && member = {:member} && is_active = true",
		dbx.Params{"project": projectID, "member": user.Id},
	)
	return err == nil && member != nil
}

// streamAsset writes the stored bytes with Plane's content type and the given
// disposition. Django 302s to S3; here we serve the bytes directly.
func (h *Handler) streamAsset(e *core.RequestEvent, asset *core.Record, disposition string) error {
	filename := asset.GetString(serializers.AssetFieldName)
	if filename == "" {
		return serializers.NotFound(e, "The requested asset could not be found.")
	}
	fsys, err := h.App.NewFilesystem()
	if err != nil {
		return err
	}
	defer fsys.Close()

	reader, err := fsys.GetReader(asset.BaseFilesPath() + "/" + filename)
	if err != nil {
		return serializers.NotFound(e, "The requested asset could not be found.")
	}
	defer reader.Close()

	contentType := mimeOf(asset)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	displayName := displayNameOf(asset, filename)
	e.Response.Header().Set("Content-Type", contentType)
	e.Response.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disposition, displayName))
	e.Response.Header().Set("Content-Length", strconv.FormatInt(reader.Size(), 10))
	return e.Stream(http.StatusOK, contentType, reader)
}

// readUpload returns the file from the request: multipart `file` (the S3 shape)
// or a raw body.
func (h *Handler) readUpload(e *core.RequestEvent, asset *core.Record) (*filesystem.File, error) {
	if files, err := e.FindUploadedFiles("file"); err == nil && len(files) > 0 {
		return files[0], nil
	}
	// Raw body fallback (curl --data-binary @file).
	body, err := io.ReadAll(io.LimitReader(e.Request.Body, fileSizeLimit+1))
	if err != nil || len(body) == 0 {
		return nil, http.ErrMissingFile
	}
	name := displayNameOf(asset, "upload")
	return filesystem.NewFileFromBytes(body, name)
}

// firstUploadedFile is a non-erroring probe used by createAsset when the client
// folds the file into the metadata POST.
func (h *Handler) firstUploadedFile(e *core.RequestEvent) (*filesystem.File, error) {
	files, err := e.FindUploadedFiles("file")
	if err != nil || len(files) == 0 {
		return nil, http.ErrMissingFile
	}
	return files[0], nil
}

// entityRelation maps an entity type + Plane UUID to the file_assets relation
// column and the resolved PocketBase record id (Django's get_entity_id_field).
func (h *Handler) entityRelation(entityType, entityIdentifier string) (string, string) {
	var collection, field string
	switch entityType {
	case serializers.EntityWorkspaceLogo:
		collection, field = "workspaces", "workspace"
	case serializers.EntityProjectCover:
		collection, field = "projects", "project"
	case serializers.EntityUserAvatar, serializers.EntityUserCover:
		collection, field = "users", "user"
	case serializers.EntityIssueAttachment, serializers.EntityIssueDescription:
		collection, field = "issues", "issue"
	case serializers.EntityPageDescription:
		collection, field = "pages", "page"
	case serializers.EntityCommentDescription:
		collection, field = "issue_comments", "comment"
	case serializers.EntityDraftIssueAttach, serializers.EntityDraftIssueDesc:
		collection, field = "draft_issues", "draft_issue"
	default:
		return "", ""
	}
	return field, h.relationID(collection, entityIdentifier)
}

// relationID resolves a Plane UUID (or PocketBase id) to a record id, "" when
// absent.
func (h *Handler) relationID(collection, idOrUUID string) string {
	record, _ := serializers.FindByUUIDOrID(h.App, collection, idOrUUID)
	if record == nil {
		return ""
	}
	return record.Id
}

// entityAssetSave ports WorkspaceFileAssetEndpoint.entity_asset_save: workspace
// logo and project cover assets become the entity's current asset.
func (h *Handler) entityAssetSave(asset *core.Record) {
	switch asset.GetString("entity_type") {
	case serializers.EntityWorkspaceLogo:
		if ws, _ := h.App.FindRecordById("workspaces", asset.GetString("workspace")); ws != nil {
			ws.Set("logo_asset", asset.Id)
			_ = h.App.Save(ws)
		}
	case serializers.EntityProjectCover:
		if project, _ := h.App.FindRecordById("projects", asset.GetString("project")); project != nil {
			project.Set("cover_image_asset", asset.Id)
			_ = h.App.Save(project)
		}
	}
}

// entityAssetDelete clears the entity pointer when the asset is removed.
func (h *Handler) entityAssetDelete(asset *core.Record) {
	switch asset.GetString("entity_type") {
	case serializers.EntityWorkspaceLogo:
		if ws, _ := h.App.FindRecordById("workspaces", asset.GetString("workspace")); ws != nil && ws.GetString("logo_asset") == asset.Id {
			ws.Set("logo_asset", "")
			_ = h.App.Save(ws)
		}
	case serializers.EntityProjectCover:
		if project, _ := h.App.FindRecordById("projects", asset.GetString("project")); project != nil && project.GetString("cover_image_asset") == asset.Id {
			project.Set("cover_image_asset", "")
			_ = h.App.Save(project)
		}
	}
}

// setProjectCover mirrors ProjectBulkAssetEndpoint.save_project_cover.
func (h *Handler) setProjectCover(project *core.Record, asset *core.Record) {
	project.Set("cover_image_asset", asset.Id)
	_ = h.App.Save(project)
}

// mimeOf reads attributes.type, stripping any parameters.
func mimeOf(asset *core.Record) string {
	raw := jsonMap(asset.Get("attributes"))
	if raw == nil {
		return ""
	}
	value, _ := raw["type"].(string)
	value = strings.TrimSpace(strings.Split(value, ";")[0])
	return strings.ToLower(value)
}

// displayNameOf prefers attributes.name, falling back to the stored filename.
func displayNameOf(asset *core.Record, fallback string) string {
	if raw := jsonMap(asset.Get("attributes")); raw != nil {
		if name, ok := raw["name"].(string); ok && name != "" {
			return sanitizeFilename(name)
		}
	}
	return fallback
}

// jsonMap normalises a PocketBase JSON column, which may surface as a decoded
// map (freshly set) or a types.JSONRaw (loaded from the DB).
func jsonMap(v any) map[string]any {
	switch value := v.(type) {
	case map[string]any:
		return value
	case types.JSONRaw:
		out := map[string]any{}
		if err := json.Unmarshal(value, &out); err != nil {
			return nil
		}
		return out
	case []byte:
		out := map[string]any{}
		if err := json.Unmarshal(value, &out); err != nil {
			return nil
		}
		return out
	case string:
		out := map[string]any{}
		if err := json.Unmarshal([]byte(value), &out); err != nil {
			return nil
		}
		return out
	}
	return nil
}

// fileTypeOf guesses a content type for storage_metadata.
func fileTypeOf(file *filesystem.File) string {
	if ext := path.Ext(file.Name); ext != "" {
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
	}
	return "application/octet-stream"
}

// absoluteURL builds a request-origin URL for the upload handshake.
func absoluteURL(e *core.RequestEvent, urlPath string) string {
	host := e.Request.Host
	if host == "" {
		return urlPath
	}
	scheme := "http"
	if e.Request.TLS != nil || strings.EqualFold(e.Request.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + host + urlPath
}

// sanitizeFilename ports plane/utils/path_validator.sanitize_filename.
func sanitizeFilename(filename string) string {
	if filename == "" {
		return ""
	}
	cleaned := make([]rune, 0, len(filename))
	for _, r := range filename {
		if r < 32 || r == 127 {
			continue
		}
		cleaned = append(cleaned, r)
	}
	name := strings.ReplaceAll(string(cleaned), "\\", "/")
	name = path.Base(name)
	name = strings.ReplaceAll(name, "..", "")
	name = strings.TrimSpace(name)
	name = strings.TrimLeft(name, ".")
	name = strings.TrimSpace(name)
	return name
}

// bindBody decodes a JSON body, tolerating an empty body (returns an empty map).
func bindBody(e *core.RequestEvent) (map[string]any, error) {
	body := map[string]any{}
	if e.Request.ContentLength == 0 {
		return body, nil
	}
	if err := e.BindBody(&body); err != nil {
		return body, nil
	}
	return body, nil
}

// asString reads a string field.
func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// toInt reads an integer-ish field with a fallback.
func toInt(v any, fallback int) int {
	switch value := v.(type) {
	case float64:
		return int(value)
	case int:
		return value
	case string:
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return fallback
}

// stringSlice reads a []string from a JSON array.
func stringSlice(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
