package publicapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// --- v1 issue attachments (FileAsset rows, ISSUE_ATTACHMENT entity type) ---------

// IssueAttachments serves GET/POST …/{issue-resource}/{issue_id}/attachments/
// and the issue-attachments/ spelling.
func (h *Handler) IssueAttachments(e *core.RequestEvent) error {
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
	filter := "project = {:p} && entity_type = 'ISSUE_ATTACHMENT' && is_deleted = false"
	params := dbx.Params{"p": project.Id}
	switch e.Request.Method {
	case http.MethodGet:
		recs, err := h.App.FindRecordsByFilter("file_assets", filter, "-created_at", 0, 0, params)
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(recs))
		for _, r := range recs {
			if r.GetString("entity_identifier") != issue.GetString("uuid") {
				continue
			}
			out = append(out, h.attachmentOut(r))
		}
		return e.JSON(http.StatusOK, out)
	case http.MethodPost:
		title := strings.TrimSpace(asString(e.Request.FormValue("title")))
		_, header, err := e.Request.FormFile("file")
		if err != nil || header == nil {
			return serializers.Validation(e, "file", "Please upload a valid file.")
		}
		coll, err := h.App.FindCollectionByNameOrId("file_assets")
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("workspace", ws.Id)
		rec.Set("project", project.Id)
		rec.Set("entity_type", "ISSUE_ATTACHMENT")
		rec.Set("entity_identifier", issue.GetString("uuid"))
		rec.Set("title", title)
		rec.Set("attributes", map[string]any{"name": header.Filename, "size": header.Size})
		rec.Set("is_uploaded", true)
		rec.Set("is_deleted", false)
		rec.Set("created_by", user.Id)
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, h.attachmentOut(rec))
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// IssueAttachmentDetail serves GET/PATCH/DELETE …/attachments/{pk}/.
func (h *Handler) IssueAttachmentDetail(e *core.RequestEvent) error {
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
	rec, _ := serializers.FindByUUIDOrID(h.App, "file_assets", e.Request.PathValue("pk"))
	if rec == nil || rec.GetString("project") != project.Id || rec.GetString("entity_type") != "ISSUE_ATTACHMENT" {
		return serializers.NotFound(e, "")
	}
	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, h.attachmentOut(rec))
	case http.MethodPatch:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		if v, ok := body["title"].(string); ok {
			rec.Set("title", v)
		}
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, h.attachmentOut(rec))
	case http.MethodDelete:
		rec.Set("is_deleted", true)
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

func (h *Handler) attachmentOut(r *core.Record) map[string]any {
	attrs := r.Get("attributes")
	return map[string]any{
		"id":                serializers.UUID(r),
		"title":             r.GetString("title"),
		"attributes":        attrs,
		"size":              nil,
		"is_deleted":        r.GetBool("is_deleted"),
		"entity_type":       r.GetString("entity_type"),
		"entity_identifier": r.GetString("entity_identifier"),
		"project":           serializers.RelationUUID(h.App, r, "project"),
		"workspace":         serializers.RelationUUID(h.App, r, "workspace"),
		"created_at":        serializers.NullableDateTime(r, "created_at"),
	}
}
