package publicapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// --- Work items (UUID-addressed) and issues (identifier-addressed) -------------

// WorkItems serves GET/POST
// /api/v1/workspaces/{slug}/projects/{project_id}/work-items/ and the
// /issues/ alias (publicapi.issues.list_create).
func (h *Handler) WorkItems(e *core.RequestEvent) error {
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
		return h.list(e, "issues",
			"project = {:p} && workspace = {:ws} && deleted_at = ''",
			dbx.Params{"p": project.Id, "ws": ws.Id}, "-created_at",
			func(r *core.Record) any { return serializers.Issue(h.App, r) })
	case http.MethodPost:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		issue, apiErr := h.createIssue(ws, project, user, body)
		if apiErr != nil {
			return e.JSON(apiErr.status, apiErr.body)
		}
		return e.JSON(http.StatusCreated, serializers.Issue(h.App, issue))
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// apiErr is a pre-rendered API error returned from helper paths that have no
// request event of their own.
type apiErr struct {
	status int
	body   map[string]any
}

func validationErr(field, msg string) *apiErr {
	return &apiErr{status: http.StatusBadRequest, body: serializers.FieldError(field, msg)}
}

// createIssue implements the shared work-item creation path: default state,
// atomic per-project sequence, priority normalization.
func (h *Handler) createIssue(ws, project, user *core.Record, body map[string]any) (*core.Record, *apiErr) {
	name, _ := body["name"].(string)
	if strings.TrimSpace(name) == "" {
		return nil, validationErr("name", "This field is required.")
	}
	priority, _ := body["priority"].(string)
	switch priority {
	case "", "none", "low", "medium", "high", "urgent":
	default:
		return nil, validationErr("priority", "Invalid priority.")
	}
	if priority == "" {
		priority = "none"
	}
	// Default to the project's default non-triage state.
	state, _ := h.App.FindFirstRecordByFilter("states",
		"project = {:p} && `default` = 1 && is_triage = 0",
		dbx.Params{"p": project.Id})
	if state == nil {
		state, _ = h.App.FindFirstRecordByFilter("states",
			"project = {:p}", dbx.Params{"p": project.Id})
	}
	coll, err := h.App.FindCollectionByNameOrId("issues")
	if err != nil {
		return nil, &apiErr{status: http.StatusInternalServerError, body: map[string]any{"error": err.Error()}}
	}
	rec := core.NewRecord(coll)
	rec.Set("uuid", serializers.NewUUID())
	rec.Set("workspace", ws.Id)
	rec.Set("project", project.Id)
	rec.Set("name", name)
	rec.Set("priority", priority)
	rec.Set("description_html", asString(body["description_html"]))
	if state != nil {
		rec.Set("state", state.Id)
	}
	var created *core.Record
	err = h.App.RunInTransaction(func(txApp core.App) error {
		// Atomic per-project sequence via MAX() inside the transaction.
		next := h.nextSequence(txApp, project.Id)
		rec.Set("sequence_id", next)
		rec.Set("created_by", user.Id)
		if e := txApp.Save(rec); e != nil {
			return e
		}
		created = rec
		return nil
	})
	if err != nil {
		return nil, &apiErr{status: http.StatusBadRequest, body: map[string]any{"detail": err.Error()}}
	}
	return created, nil
}

// nextSequence computes MAX(sequence_id)+1 for the project inside a
// transaction (SQLite serializes writers, giving the same guarantee as
// Plane's issue_sequences row lock).
func (h *Handler) nextSequence(app core.App, projectID string) int {
	rows := []struct {
		Max int `db:"max"`
	}{}
	err := app.DB().NewQuery(
		"SELECT COALESCE(MAX(sequence_id), 0) AS max FROM issues WHERE project = {:project}").
		Bind(dbx.Params{"project": projectID}).All(&rows)
	if err != nil || len(rows) == 0 {
		return 1
	}
	return rows[0].Max + 1
}

// WorkItemDetail serves GET/PUT/PATCH/DELETE
// …/{project_id}/work-items/{pk}/ and …/issues/{pk}/.
func (h *Handler) WorkItemDetail(e *core.RequestEvent) error {
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
	issue, _ := serializers.FindByUUIDOrID(h.App, "issues", e.Request.PathValue("pk"))
	if issue == nil || issue.GetString("project") != project.Id {
		return serializers.NotFound(e, "")
	}
	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, serializers.Issue(h.App, issue))
	case http.MethodPatch, http.MethodPut:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		if v, ok := body["name"].(string); ok && strings.TrimSpace(v) != "" {
			issue.Set("name", v)
		}
		if v, ok := body["priority"].(string); ok {
			issue.Set("priority", v)
		}
		if v, ok := body["description_html"].(string); ok {
			issue.Set("description_html", v)
		}
		if v, ok := body["state"].(string); ok && v != "" {
			if st, _ := serializers.FindByUUIDOrID(h.App, "states", v); st != nil && st.GetString("project") == project.Id {
				issue.Set("state", st.Id)
			}
		}
		issue.Set("updated_by", user.Id)
		if err := h.App.Save(issue); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, serializers.Issue(h.App, issue))
	case http.MethodDelete:
		issue.Set("deleted_at", serializers.Now())
		if err := h.App.Save(issue); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// --- Comments -------------------------------------------------------------------

// Comments serves GET/POST …/{project_id}/{issue-resource}/{issue_id}/comments/
// for both the issues/ and work-items/ spellings.
func (h *Handler) Comments(e *core.RequestEvent) error {
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
		recs, err := h.App.FindRecordsByFilter("issue_comments",
			"issue = {:issue}", "created_at", 0, 0,
			dbx.Params{"issue": issue.Id})
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(recs))
		for _, c := range recs {
			out = append(out, serializers.Comment(h.App, c))
		}
		return e.JSON(http.StatusOK, out)
	case http.MethodPost:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		html, _ := body["comment_html"].(string)
		if strings.TrimSpace(html) == "" {
			return serializers.Validation(e, "comment_html", "This field is required.")
		}
		coll, err := h.App.FindCollectionByNameOrId("issue_comments")
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("issue", issue.Id)
		rec.Set("project", project.Id)
		rec.Set("workspace", ws.Id)
		rec.Set("actor", user.Id)
		rec.Set("comment_html", html)
		rec.Set("access", "INTERNAL")
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, serializers.Comment(h.App, rec))
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// CommentDetail serves GET/PATCH/DELETE …/comments/{pk}/.
func (h *Handler) CommentDetail(e *core.RequestEvent) error {
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
	rec, _ := serializers.FindByUUIDOrID(h.App, "issue_comments", e.Request.PathValue("pk"))
	if rec == nil || rec.GetString("project") != project.Id {
		return serializers.NotFound(e, "")
	}
	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, serializers.Comment(h.App, rec))
	case http.MethodPatch, http.MethodPut:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		if v, ok := body["comment_html"].(string); ok {
			rec.Set("comment_html", v)
			rec.Set("edited_at", serializers.Now())
		}
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, serializers.Comment(h.App, rec))
	case http.MethodDelete:
		if err := h.App.Delete(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// issueInProject resolves an issue by Plane UUID within a project.
func (h *Handler) issueInProject(project *core.Record, idOrUUID string) *core.Record {
	issue, _ := serializers.FindByUUIDOrID(h.App, "issues", idOrUUID)
	if issue == nil || issue.GetString("project") != project.Id {
		return nil
	}
	return issue
}
