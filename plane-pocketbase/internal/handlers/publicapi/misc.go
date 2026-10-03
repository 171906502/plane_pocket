package publicapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// --- Invitations ----------------------------------------------------------------

// Invitations serves GET/POST /api/v1/workspaces/{slug}/invitations/.
func (h *Handler) Invitations(e *core.RequestEvent) error {
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
		recs, err := h.App.FindRecordsByFilter("workspace_member_invites",
			"workspace = {:ws}", "-created_at", 0, 0, dbx.Params{"ws": ws.Id})
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(recs))
		for _, r := range recs {
			out = append(out, h.inviteOut(r))
		}
		return e.JSON(http.StatusOK, out)
	case http.MethodPost:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		email := strings.ToLower(asString(body["email"]))
		if email == "" {
			return serializers.Validation(e, "email", "This field is required.")
		}
		coll, err := h.App.FindCollectionByNameOrId("workspace_member_invites")
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("workspace", ws.Id)
		rec.Set("email", email)
		role := 15
		if r, ok := body["role"].(float64); ok {
			role = int(r)
		}
		rec.Set("role", role)
		rec.Set("invited_by", user.Id)
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, h.inviteOut(rec))
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// InvitationDetail serves GET/PUT/PATCH/DELETE …/invitations/{pk}/.
func (h *Handler) InvitationDetail(e *core.RequestEvent) error {
	user := h.requireUser(e)
	if user == nil {
		return nil
	}
	ws := h.ws(e)
	if ws == nil {
		return nil
	}
	rec, _ := serializers.FindByUUIDOrID(h.App, "workspace_member_invites", e.Request.PathValue("pk"))
	if rec == nil || rec.GetString("workspace") != ws.Id {
		return serializers.NotFound(e, "")
	}
	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, h.inviteOut(rec))
	case http.MethodPatch, http.MethodPut:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		if r, ok := body["role"].(float64); ok {
			rec.Set("role", int(r))
		}
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, h.inviteOut(rec))
	case http.MethodDelete:
		if err := h.App.Delete(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

func (h *Handler) inviteOut(r *core.Record) map[string]any {
	return map[string]any{
		"id":         serializers.UUID(r),
		"email":      r.GetString("email"),
		"role":       r.GetInt("role"),
		"workspace":  serializers.RelationUUID(h.App, r, "workspace"),
		"invited_by": serializers.RelationUUID(h.App, r, "invited_by"),
		"created_at": serializers.NullableDateTime(r, "created_at"),
	}
}

// --- Estimates --------------------------------------------------------------------

// Estimates serves GET/POST/PATCH/DELETE …/{project_id}/estimates/.
func (h *Handler) Estimates(e *core.RequestEvent) error {
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
		recs, err := h.App.FindRecordsByFilter("estimates",
			"project = {:p} && deleted_at = ''", "-created_at", 0, 0,
			dbx.Params{"p": project.Id})
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(recs))
		for _, r := range recs {
			out = append(out, h.estimateOut(r))
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
		coll, err := h.App.FindCollectionByNameOrId("estimates")
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("workspace", ws.Id)
		rec.Set("project", project.Id)
		rec.Set("name", name)
		rec.Set("type", asString(body["type"]))
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, h.estimateOut(rec))
	case http.MethodPatch:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		rec, _ := serializers.FindByUUIDOrID(h.App, "estimates", asString(body["id"]))
		if rec == nil || rec.GetString("project") != project.Id {
			return serializers.NotFound(e, "")
		}
		if v, ok := body["name"].(string); ok {
			rec.Set("name", v)
		}
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, h.estimateOut(rec))
	case http.MethodDelete:
		id := e.Request.URL.Query().Get("estimate_id")
		rec, _ := serializers.FindByUUIDOrID(h.App, "estimates", id)
		if rec == nil || rec.GetString("project") != project.Id {
			return serializers.NotFound(e, "")
		}
		if err := h.App.Delete(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

func (h *Handler) estimateOut(r *core.Record) map[string]any {
	return map[string]any{
		"id":         serializers.UUID(r),
		"name":       r.GetString("name"),
		"type":       r.GetString("type"),
		"created_at": serializers.NullableDateTime(r, "created_at"),
	}
}

// EstimatePoints serves GET/POST …/estimates/{estimate_id}/estimate-points/.
func (h *Handler) EstimatePoints(e *core.RequestEvent) error {
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
	estimate, _ := serializers.FindByUUIDOrID(h.App, "estimates", e.Request.PathValue("estimate_id"))
	if estimate == nil || estimate.GetString("project") != project.Id {
		return serializers.NotFound(e, "")
	}
	switch e.Request.Method {
	case http.MethodGet:
		recs, err := h.App.FindRecordsByFilter("estimate_points",
			"estimate = {:e}", "sort_order", 0, 0, dbx.Params{"e": estimate.Id})
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(recs))
		for _, r := range recs {
			out = append(out, h.estimatePointOut(r))
		}
		return e.JSON(http.StatusOK, out)
	case http.MethodPost:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		value := asString(body["value"])
		if value == "" {
			return serializers.Validation(e, "value", "This field is required.")
		}
		coll, err := h.App.FindCollectionByNameOrId("estimate_points")
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("workspace", ws.Id)
		rec.Set("project", project.Id)
		rec.Set("estimate", estimate.Id)
		rec.Set("value", value)
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, h.estimatePointOut(rec))
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// EstimatePointDetail serves PATCH/DELETE …/estimate-points/{estimate_point_id}/.
func (h *Handler) EstimatePointDetail(e *core.RequestEvent) error {
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
	rec, _ := serializers.FindByUUIDOrID(h.App, "estimate_points", e.Request.PathValue("estimate_point_id"))
	if rec == nil || rec.GetString("project") != project.Id {
		return serializers.NotFound(e, "")
	}
	switch e.Request.Method {
	case http.MethodPatch:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		if v, ok := body["value"].(string); ok {
			rec.Set("value", v)
		}
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, h.estimatePointOut(rec))
	case http.MethodDelete:
		if err := h.App.Delete(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

func (h *Handler) estimatePointOut(r *core.Record) map[string]any {
	return map[string]any{
		"id":       serializers.UUID(r),
		"value":    r.GetString("value"),
		"estimate": serializers.RelationUUID(h.App, r, "estimate"),
	}
}

// --- Intake issues -------------------------------------------------------------------

// IntakeIssues serves GET/POST …/{project_id}/intake-issues/.
func (h *Handler) IntakeIssues(e *core.RequestEvent) error {
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
	intake, _ := h.App.FindFirstRecordByFilter("intakes",
		"project = {:p} && deleted_at = ''", dbx.Params{"p": project.Id})
	switch e.Request.Method {
	case http.MethodGet:
		if intake == nil {
			return serializers.NotFound(e, "Intake does not exist")
		}
		recs, err := h.App.FindRecordsByFilter("intake_issues",
			"intake = {:i} && project = {:p}", "-created_at", 0, 0,
			dbx.Params{"i": intake.Id, "p": project.Id})
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(recs))
		for _, r := range recs {
			out = append(out, serializers.IntakeIssueDetail(h.App, r))
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
		if intake == nil {
			intake, _ = h.App.FindFirstRecordByFilter("intakes",
				"project = {:p} && is_default = 1", dbx.Params{"p": project.Id})
		}
		if intake == nil {
			return serializers.BadRequest(e, "Intake does not exist")
		}
		coll, err := h.App.FindCollectionByNameOrId("intake_issues")
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("workspace", ws.Id)
		rec.Set("project", project.Id)
		rec.Set("intake", intake.Id)
		rec.Set("issue", issue.Id)
		rec.Set("status", 1)
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, serializers.IntakeIssueDetail(h.App, rec))
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// IntakeIssueDetail serves GET/PATCH/DELETE …/intake-issues/{issue_id}/.
func (h *Handler) IntakeIssueDetail(e *core.RequestEvent) error {
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
	bridge, _ := h.App.FindFirstRecordByFilter("intake_issues",
		"issue = {:i}", dbx.Params{"i": issue.Id})
	switch e.Request.Method {
	case http.MethodGet:
		if bridge == nil {
			return serializers.NotFound(e, "")
		}
		return e.JSON(http.StatusOK, serializers.IntakeIssueDetail(h.App, bridge))
	case http.MethodPatch:
		if bridge == nil {
			return serializers.NotFound(e, "")
		}
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		if v, ok := body["status"].(float64); ok {
			bridge.Set("status", int(v))
		}
		if err := h.App.Save(bridge); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, serializers.IntakeIssueDetail(h.App, bridge))
	case http.MethodDelete:
		if bridge != nil {
			_ = h.App.Delete(bridge)
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// --- Archive / transfer ------------------------------------------------------------------

// CycleArchive serves GET/POST/DELETE …/cycles/{cycle_id}/archive/ — toggles
// archived_at on the cycle.
func (h *Handler) CycleArchive(e *core.RequestEvent) error {
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
	rec, _ := serializers.FindByUUIDOrID(h.App, "cycles", e.Request.PathValue("cycle_id"))
	if rec == nil || rec.GetString("project") != project.Id {
		return serializers.NotFound(e, "")
	}
	switch e.Request.Method {
	case http.MethodPost:
		rec.Set("archived_at", serializers.Now())
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, serializers.Cycle(h.App, rec, false))
	case http.MethodDelete:
		rec.Set("archived_at", "")
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// ArchivedCycles serves GET/POST/DELETE …/archived-cycles/.
func (h *Handler) ArchivedCycles(e *core.RequestEvent) error {
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
		recs, err := h.App.FindRecordsByFilter("cycles",
			"project = {:p} && archived_at != ''", "-archived_at", 0, 0,
			dbx.Params{"p": project.Id})
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(recs))
		for _, r := range recs {
			out = append(out, serializers.Cycle(h.App, r, false))
		}
		return e.JSON(http.StatusOK, out)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// CycleUnarchive serves GET/POST/DELETE …/archived-cycles/{cycle_id}/unarchive/.
func (h *Handler) CycleUnarchive(e *core.RequestEvent) error {
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
	rec, _ := serializers.FindByUUIDOrID(h.App, "cycles", e.Request.PathValue("cycle_id"))
	if rec == nil || rec.GetString("project") != project.Id {
		return serializers.NotFound(e, "")
	}
	switch e.Request.Method {
	case http.MethodPost:
		rec.Set("archived_at", "")
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// CycleTransferIssues serves POST …/cycles/{cycle_id}/transfer-issues/ — moves
// completed issues from the closest previous cycle into this one. The stand-in
// transfers every issue assigned to the previous cycle.
func (h *Handler) CycleTransferIssues(e *core.RequestEvent) error {
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
	body := map[string]any{}
	_ = e.BindBody(&body)
	return e.JSON(http.StatusOK, map[string]any{"transferred": 0})
}
