package publicapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// --- Issue links -----------------------------------------------------------------

// Links serves GET/POST …/{issue-resource}/{issue_id}/links/ for both the
// issues/ and work-items/ spellings.
func (h *Handler) Links(e *core.RequestEvent) error {
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
	switch e.Request.Method {
	case http.MethodGet:
		recs, err := h.App.FindRecordsByFilter("issue_links",
			"issue = {:i} && deleted_at = ''", "-created_at", 0, 0,
			dbx.Params{"i": issue.Id})
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(recs))
		for _, l := range recs {
			out = append(out, map[string]any{
				"id":         serializers.UUID(l),
				"title":      l.GetString("title"),
				"url":        l.GetString("url"),
				"issue":      serializers.RelationUUID(h.App, l, "issue"),
				"created_at": serializers.NullableDateTime(l, "created_at"),
			})
		}
		return e.JSON(http.StatusOK, out)
	case http.MethodPost:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		url, _ := body["url"].(string)
		if strings.TrimSpace(url) == "" {
			return serializers.Validation(e, "url", "This field is required.")
		}
		coll, err := h.App.FindCollectionByNameOrId("issue_links")
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("workspace", ws.Id)
		rec.Set("project", project.Id)
		rec.Set("issue", issue.Id)
		rec.Set("url", url)
		rec.Set("title", asString(body["title"]))
		rec.Set("created_by", user.Id)
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, map[string]any{
			"id": serializers.UUID(rec), "title": rec.GetString("title"), "url": rec.GetString("url"),
		})
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// LinkDetail serves GET/PATCH/DELETE …/links/{pk}/.
func (h *Handler) LinkDetail(e *core.RequestEvent) error {
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
	rec, _ := serializers.FindByUUIDOrID(h.App, "issue_links", e.Request.PathValue("pk"))
	if rec == nil || rec.GetString("project") != project.Id {
		return serializers.NotFound(e, "")
	}
	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, map[string]any{
			"id": serializers.UUID(rec), "title": rec.GetString("title"), "url": rec.GetString("url"),
		})
	case http.MethodPatch, http.MethodPut:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		for _, key := range []string{"title", "url"} {
			if v, ok := body[key]; ok {
				rec.Set(key, v)
			}
		}
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, map[string]any{
			"id": serializers.UUID(rec), "title": rec.GetString("title"), "url": rec.GetString("url"),
		})
	case http.MethodDelete:
		if err := h.App.Delete(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// --- Issue relations ---------------------------------------------------------------

// Relations serves GET/POST …/work-items/{issue_id}/relations/.
func (h *Handler) Relations(e *core.RequestEvent) error {
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
	switch e.Request.Method {
	case http.MethodGet:
		recs, err := h.App.FindRecordsByFilter("issue_relations",
			"issue = {:i} && deleted_at = ''", "-created_at", 0, 0,
			dbx.Params{"i": issue.Id})
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(recs))
		for _, r := range recs {
			out = append(out, map[string]any{
				"id":            serializers.UUID(r),
				"relation_type": r.GetString("relation_type"),
				"issue":         serializers.RelationUUID(h.App, r, "issue"),
				"related_issue": serializers.RelationUUID(h.App, r, "related_issue"),
			})
		}
		return e.JSON(http.StatusOK, out)
	case http.MethodPost:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		relatedList := h.uuidListFromBody(body)
		if len(relatedList) == 0 {
			return serializers.Validation(e, "related_list", "This field is required.")
		}
		relationType, _ := body["relation_type"].(string)
		coll, err := h.App.FindCollectionByNameOrId("issue_relations")
		if err != nil {
			return err
		}
		created := make([]string, 0, len(relatedList))
		for _, rel := range relatedList {
			related := h.issueInProject(project, rel)
			if related == nil || related.Id == issue.Id {
				continue
			}
			rec := core.NewRecord(coll)
			rec.Set("uuid", serializers.NewUUID())
			rec.Set("workspace", ws.Id)
			rec.Set("project", project.Id)
			rec.Set("issue", issue.Id)
			rec.Set("related_issue", related.Id)
			rec.Set("relation_type", relationType)
			rec.Set("created_by", user.Id)
			if err := h.App.Save(rec); err != nil {
				continue
			}
			created = append(created, serializers.UUID(rec))
		}
		return e.JSON(http.StatusCreated, map[string]any{"ids": created})
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// uuidListFromBody reads a list of UUIDs from various plausible keys.
func (h *Handler) uuidListFromBody(body map[string]any) []string {
	for _, key := range []string{"related_issue", "related_list", "issues"} {
		switch v := body[key].(type) {
		case string:
			if v != "" {
				return []string{v}
			}
		case []any:
			out := make([]string, 0, len(v))
			for _, item := range v {
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return nil
}

// --- Activities ---------------------------------------------------------------------

// Activities serves GET …/{issue-resource}/{issue_id}/activities/ — the issue
// activity feed. The issue_activities collection is written by the app API's
// activity pipeline; the v1 API only reads it.
func (h *Handler) Activities(e *core.RequestEvent) error {
	if h.requireUser(e) == nil {
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
	recs, err := h.App.FindRecordsByFilter("issue_activities",
		"issue = {:i}", "-created_at", 0, 0, dbx.Params{"i": issue.Id})
	if err != nil {
		// The collection exists but the pipeline may not have written rows yet.
		recs = nil
	}
	out := make([]map[string]any, 0, len(recs))
	for _, a := range recs {
		out = append(out, map[string]any{
			"id":         serializers.UUID(a),
			"issue":      serializers.RelationUUID(h.App, a, "issue"),
			"actor":      serializers.RelationUUID(h.App, a, "actor"),
			"verb":       a.GetString("verb"),
			"field":      a.GetString("field"),
			"old_value":  a.GetString("old_value"),
			"new_value":  a.GetString("new_value"),
			"comment":    a.GetString("comment"),
			"created_at": serializers.NullableDateTime(a, "created_at"),
		})
	}
	return e.JSON(http.StatusOK, out)
}

// ActivityDetail serves GET …/activities/{pk}/.
func (h *Handler) ActivityDetail(e *core.RequestEvent) error {
	if h.requireUser(e) == nil {
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
	rec, _ := serializers.FindByUUIDOrID(h.App, "issue_activities", e.Request.PathValue("pk"))
	if rec == nil || rec.GetString("project") != project.Id {
		return serializers.NotFound(e, "")
	}
	return e.JSON(http.StatusOK, map[string]any{
		"id":         serializers.UUID(rec),
		"verb":       rec.GetString("verb"),
		"field":      rec.GetString("field"),
		"old_value":  rec.GetString("old_value"),
		"new_value":  rec.GetString("new_value"),
		"comment":    rec.GetString("comment"),
		"created_at": serializers.NullableDateTime(rec, "created_at"),
	})
}

// --- Project members ------------------------------------------------------------------

// ProjectMembers serves GET/POST …/{project_id}/project-members/ (and the
// members/ alias).
func (h *Handler) ProjectMembers(e *core.RequestEvent) error {
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
		recs, err := h.App.FindRecordsByFilter("project_members",
			"project = {:p} && is_active = 1", "created_at", 0, 0,
			dbx.Params{"p": project.Id})
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(recs))
		for _, m := range recs {
			out = append(out, h.projectMemberOut(m))
		}
		return e.JSON(http.StatusOK, out)
	case http.MethodPost:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		memberID, _ := body["member"].(string)
		member, _ := serializers.FindByUUIDOrID(h.App, "users", memberID)
		if member == nil {
			return serializers.NotFound(e, "Member not found")
		}
		coll, err := h.App.FindCollectionByNameOrId("project_members")
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("workspace", ws.Id)
		rec.Set("project", project.Id)
		rec.Set("member", member.Id)
		role := 15
		if r, ok := body["role"].(float64); ok {
			role = int(r)
		}
		rec.Set("role", role)
		rec.Set("is_active", true)
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, h.projectMemberOut(rec))
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// ProjectMemberDetail serves GET/PATCH/DELETE …/project-members/{pk}/.
func (h *Handler) ProjectMemberDetail(e *core.RequestEvent) error {
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
	rec, _ := serializers.FindByUUIDOrID(h.App, "project_members", e.Request.PathValue("pk"))
	if rec == nil || rec.GetString("project") != project.Id {
		return serializers.NotFound(e, "")
	}
	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, h.projectMemberOut(rec))
	case http.MethodPatch:
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
		return e.JSON(http.StatusOK, h.projectMemberOut(rec))
	case http.MethodDelete:
		if err := h.App.Delete(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

func (h *Handler) projectMemberOut(m *core.Record) map[string]any {
	out := map[string]any{
		"id":        serializers.UUID(m),
		"role":      m.GetInt("role"),
		"is_active": m.GetBool("is_active"),
		"member":    serializers.RelationUUID(h.App, m, "member"),
	}
	if u, _ := serializers.FindByUUIDOrID(h.App, "users", m.GetString("member")); u != nil {
		out["member_detail"] = map[string]any{
			"id":           serializers.UUID(u),
			"display_name": u.GetString("display_name"),
			"first_name":   u.GetString("first_name"),
			"last_name":    u.GetString("last_name"),
			"avatar":       u.GetString("avatar"),
			"is_bot":       u.GetBool("is_bot"),
		}
	} else {
		out["member_detail"] = nil
	}
	return out
}

// ProjectMembersLite serves GET …/{project_id}/project-members-lite/.
func (h *Handler) ProjectMembersLite(e *core.RequestEvent) error {
	if h.requireUser(e) == nil {
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
	recs, err := h.App.FindRecordsByFilter("project_members",
		"project = {:p} && is_active = 1", "created_at", 0, 0,
		dbx.Params{"p": project.Id})
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(recs))
	for _, m := range recs {
		item := map[string]any{"id": serializers.UUID(m), "role": m.GetInt("role")}
		if u, _ := serializers.FindByUUIDOrID(h.App, "users", m.GetString("member")); u != nil {
			item["member"] = serializers.UUID(u)
			item["display_name"] = u.GetString("display_name")
			item["avatar"] = u.GetString("avatar")
		}
		out = append(out, item)
	}
	return e.JSON(http.StatusOK, out)
}

// CyclesLite serves GET …/{project_id}/cycles-lite/.
func (h *Handler) CyclesLite(e *core.RequestEvent) error {
	if h.requireUser(e) == nil {
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
	recs, err := h.App.FindRecordsByFilter("cycles",
		"project = {:p} && deleted_at = ''", "-created_at", 0, 0,
		dbx.Params{"p": project.Id})
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(recs))
	for _, c := range recs {
		out = append(out, map[string]any{"id": serializers.UUID(c), "name": c.GetString("name")})
	}
	return e.JSON(http.StatusOK, out)
}

// ModulesLite serves GET …/{project_id}/modules-lite/.
func (h *Handler) ModulesLite(e *core.RequestEvent) error {
	if h.requireUser(e) == nil {
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
	recs, err := h.App.FindRecordsByFilter("modules",
		"project = {:p} && deleted_at = ''", "-created_at", 0, 0,
		dbx.Params{"p": project.Id})
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(recs))
	for _, m := range recs {
		out = append(out, map[string]any{"id": serializers.UUID(m), "name": m.GetString("name")})
	}
	return e.JSON(http.StatusOK, out)
}

// Summary serves GET …/{project_id}/summary/ — a lightweight stats roll-up.
func (h *Handler) Summary(e *core.RequestEvent) error {
	if h.requireUser(e) == nil {
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
	total, _ := h.App.CountRecords("issues", dbx.HashExp{"project": project.Id, "deleted_at": ""})
	states, _ := h.App.FindRecordsByFilter("states",
		"project = {:p}", "sequence", 0, 0, dbx.Params{"p": project.Id})
	distribution := map[string]any{}
	for _, s := range states {
		n, _ := h.App.CountRecords("issues", dbx.HashExp{"project": project.Id, "state": s.Id, "deleted_at": ""})
		distribution[s.GetString("group")] = n
	}
	return e.JSON(http.StatusOK, map[string]any{
		"total_issues":       total,
		"state_distribution": distribution,
	})
}
