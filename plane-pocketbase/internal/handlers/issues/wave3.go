// Wave-3 method-gap endpoints for the issue domain. Every handler here was
// surfaced by tests/browser/audit-methods.py — the path was either entirely
// unregistered or registered without a method the frontend actually calls
// (the bug class behind defects #23/#24).
//
//	GET/POST   /api/workspaces/{slug}/projects/{pid}/issues/{issue_id}/sub-issues/
//	GET/POST   /api/workspaces/{slug}/projects/{pid}/issues/{issue_id}/issue-links/
//	GET/PATCH/PUT/DELETE /api/workspaces/{slug}/projects/{pid}/issues/{issue_id}/issue-links/{pk}/
//	GET        /api/workspaces/{slug}/projects/{pid}/issues/{pk}/archive/     (retrieve)
//	POST       /api/workspaces/{slug}/projects/{pid}/issues/{pk}/archive/     (archive)
//	DELETE     /api/workspaces/{slug}/projects/{pid}/issues/{pk}/archive/     (unarchive)
//
// Contract: apps/api/plane/app/views/issue/{sub_issue,link,archive}.py and
// spec/api-spec.yaml (issue.sub-issues.list_create, issue.issue-links.*,
// issue.archive.list). The `epics/` prefix variants of these paths are NOT
// implemented — see the note in internal/router/issue.go.
package issues

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// ---------------------------------------------------------------------------
// sub-issues  (SubIssuesEndpoint)
// ---------------------------------------------------------------------------

// SubIssues serves GET …/issues/{issue_id}/sub-issues/ — the issue detail
// panel's sub-issue tree. Response: `{sub_issues: [...], state_distribution:
// {group: [uuid]}}`, with an optional `group_by` regrouping of `sub_issues`.
func (h *Handler) SubIssues(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id", "pk"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}

	// Django filters Issue.issue_objects (soft-deleted excluded) and does NOT
	// exclude archived/draft children here — mirror that exactly.
	rows, err := h.App.FindRecordsByFilter(
		"issues",
		"parent = {:parent} && deleted_at = ''",
		orderBy(e.Request.URL.Query().Get("order_by")), 0, 0,
		dbx.Params{"parent": issue.Id},
	)
	if err != nil {
		return err
	}

	subIssues := make([]map[string]any, 0, len(rows))
	distribution := map[string][]string{}
	grouped := map[string][]map[string]any{}
	for _, row := range rows {
		out := serializers.Issue(h.App, row)
		group := h.stateGroup(row)
		if group == "" {
			group = "null" // Django: defaultdict keyed by None → JSON "null"
		}
		if id, ok := out["id"].(string); ok {
			distribution[group] = append(distribution[group], id)
		}
		subIssues = append(subIssues, out)
		grouped[group] = append(grouped[group], out)
	}

	// group_by mirrors SubIssuesEndpoint.get's generic branch: group the issue
	// objects by the named field (`assignees__ids` fans out per assignee).
	if gb := e.Request.URL.Query().Get("group_by"); gb != "" {
		result := map[string][]map[string]any{}
		for _, out := range subIssues {
			if gb == "assignees__ids" {
				ids, _ := out["assignee_ids"].([]string)
				if len(ids) == 0 {
					result["None"] = append(result["None"], out)
					continue
				}
				for _, id := range ids {
					result[id] = append(result[id], out)
				}
				continue
			}
			key := "None"
			if v, ok := out[gb]; ok && v != nil {
				key = fmt.Sprintf("%v", v)
			}
			result[key] = append(result[key], out)
		}
		return e.JSON(http.StatusOK, map[string]any{"sub_issues": result, "state_distribution": distribution})
	}
	return e.JSON(http.StatusOK, map[string]any{"sub_issues": subIssues, "state_distribution": distribution})
}

// AddSubIssues serves POST …/issues/{issue_id}/sub-issues/ — re-parent the
// given issues under {issue_id}. Body: `{sub_issue_ids: [...]}`.
//
// Permission: project ADMIN/MEMBER. Django's ProjectEntityPermission admits any
// member; this port's convention for issue mutations (issues.go Create/Update)
// is RoleMember, so that is what is enforced here.
func (h *Handler) AddSubIssues(e *core.RequestEvent) error {
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
	parent := h.projectIssue(scope, pathUUID(e, "issue_id", "pk"))
	if parent == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "Parent issue not found"})
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	ids := uuidList(body, "sub_issue_ids")
	if len(ids) == 0 {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Sub Issue IDs are required"})
	}

	updated := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		// Scope to the URL project: a cross-project id is skipped, not 500'd
		// (Django's filter simply doesn't match it).
		child, _ := serializers.FindByUUID(h.App, "issues", id)
		if child == nil || child.GetString("project") != scope.Project.Id {
			continue
		}
		child.Set("parent", parent.Id)
		child.Set("updated_by", user.Id)
		if err := h.App.Save(child); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		updated = append(updated, serializers.Issue(h.App, child))
	}

	distribution := map[string][]string{}
	for _, out := range updated {
		group, _ := out["state__group"].(string)
		if group == "" {
			group = "null"
		}
		if id, ok := out["id"].(string); ok {
			distribution[group] = append(distribution[group], id)
		}
	}
	return e.JSON(http.StatusOK, map[string]any{"sub_issues": updated, "state_distribution": distribution})
}

// ---------------------------------------------------------------------------
// issue links  (IssueLinkViewSet)
// ---------------------------------------------------------------------------

// issueLinkJSON mirrors IssueLinkSerializer (fields "__all__").
func (h *Handler) issueLinkJSON(r *core.Record) map[string]any {
	return map[string]any{
		"id":         serializers.UUID(r),
		"title":      serializers.NullableText(r, "title"),
		"url":        serializers.Text(r, "url"),
		"metadata":   serializers.JSONValue(r, "metadata"),
		"issue":      serializers.RelationUUID(h.App, r, "issue"),
		"workspace":  serializers.RelationUUID(h.App, r, "workspace"),
		"project":    serializers.RelationUUID(h.App, r, "project"),
		"created_by": serializers.RelationUUID(h.App, r, "created_by"),
		"updated_by": serializers.RelationUUID(h.App, r, "updated_by"),
		"created_at": serializers.DateTime(r, "created_at"),
		"updated_at": serializers.DateTime(r, "updated_at"),
	}
}

// normaliseLinkURL ports IssueLinkSerializer.to_internal_value: a URL without
// a scheme gets `http://` prepended. (Django's URLValidator does the rest; a
// light scheme check is enough for this port.)
func normaliseLinkURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw != "" && !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return "http://" + raw
	}
	return raw
}

// linkURLTaken reports whether another link of the same issue already uses the
// URL (IssueLinkSerializer.create/update duplicate check).
func (h *Handler) linkURLTaken(issueID, url, excludeID string) bool {
	rows, err := h.App.FindRecordsByFilter(
		"issue_links",
		"issue = {:issue} && url = {:url} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"issue": issueID, "url": url},
	)
	if err != nil {
		return false
	}
	for _, row := range rows {
		if row.Id != excludeID {
			return true
		}
	}
	return false
}

// ListIssueLinks serves GET …/issues/{issue_id}/issue-links/ — a plain array
// (the ViewSet has no pagination override).
func (h *Handler) ListIssueLinks(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	rows, err := h.App.FindRecordsByFilter(
		"issue_links",
		"issue = {:issue} && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"issue": issue.Id},
	)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, h.issueLinkJSON(row))
	}
	return e.JSON(http.StatusOK, out)
}

// CreateIssueLink serves POST …/issues/{issue_id}/issue-links/.
func (h *Handler) CreateIssueLink(e *core.RequestEvent) error {
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
	issue := h.projectIssue(scope, pathUUID(e, "issue_id"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	url := normaliseLinkURL(asString(body["url"]))
	if url == "" {
		return serializers.Validation(e, "url", "This field is required.")
	}
	if h.linkURLTaken(issue.Id, url, "") {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "URL already exists for this Issue"})
	}

	coll, err := h.App.FindCollectionByNameOrId("issue_links")
	if err != nil {
		return err
	}
	rec := core.NewRecord(coll)
	rec.Set("uuid", serializers.NewUUID())
	rec.Set("workspace", scope.Project.GetString("workspace"))
	rec.Set("project", scope.Project.Id)
	rec.Set("issue", issue.Id)
	rec.Set("url", url)
	rec.Set("title", asString(body["title"]))
	if v, ok := body["metadata"]; ok {
		rec.Set("metadata", v)
	}
	rec.Set("created_by", user.Id)
	if err := h.App.Save(rec); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusCreated, h.issueLinkJSON(rec))
}

// IssueLinkDetail serves GET/PATCH/PUT/DELETE …/issue-links/{pk}/
// (IssueLinkViewSet retrieve/partial_update/update/destroy). Destroy is a hard
// delete returning 204, as in link.py.
func (h *Handler) IssueLinkDetail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	rec, _ := serializers.FindByUUIDOrID(h.App, "issue_links", e.Request.PathValue("pk"))
	if rec == nil || rec.GetString("issue") != issue.Id || rec.GetString("deleted_at") != "" {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}

	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, h.issueLinkJSON(rec))

	case http.MethodPatch, http.MethodPut:
		if scope.ProjectRole < middleware.RoleMember {
			return serializers.Forbidden(e, "You do not have permission to perform this action.")
		}
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		if raw, ok := body["url"]; ok {
			url := normaliseLinkURL(asString(raw))
			if url == "" {
				return serializers.Validation(e, "url", "This field is required.")
			}
			if h.linkURLTaken(issue.Id, url, rec.Id) {
				return e.JSON(http.StatusBadRequest, map[string]any{"error": "URL already exists for this Issue"})
			}
			rec.Set("url", url)
		}
		if v, ok := body["title"]; ok {
			rec.Set("title", v)
		}
		if v, ok := body["metadata"]; ok {
			rec.Set("metadata", v)
		}
		if user := middleware.CurrentUser(e); user != nil {
			rec.Set("updated_by", user.Id)
		}
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, h.issueLinkJSON(rec))

	case http.MethodDelete:
		if scope.ProjectRole < middleware.RoleMember {
			return serializers.Forbidden(e, "You do not have permission to perform this action.")
		}
		if err := h.App.Delete(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// ---------------------------------------------------------------------------
// single-issue archive  (IssueArchiveViewSet retrieve/archive/unarchive)
// ---------------------------------------------------------------------------

// ArchivedIssueDetail serves GET …/issues/{pk}/archive/ — the archived issue's
// detail (only rows with archived_at set).
func (h *Handler) ArchivedIssueDetail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id", "pk"))
	if issue == nil || issue.GetString("archived_at") == "" {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	out := serializers.Issue(h.App, issue)
	// is_subscribed comes from the ViewSet's Exists() annotation.
	if user := middleware.CurrentUser(e); user != nil {
		_, err := h.findSubscriber(issue, user)
		out["is_subscribed"] = err == nil
	}
	return e.JSON(http.StatusOK, out)
}

// ArchiveIssue serves POST …/issues/{pk}/archive/ — only completed/cancelled
// state groups may be archived.
func (h *Handler) ArchiveIssue(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id", "pk"))
	if issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	if group := h.stateGroup(issue); group != "completed" && group != "cancelled" {
		return e.JSON(http.StatusBadRequest, map[string]any{
			"error": "Can only archive completed or cancelled state group issue",
		})
	}
	now := serializers.Now()
	issue.Set("archived_at", now)
	if err := h.App.Save(issue); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.JSON(http.StatusOK, map[string]any{"archived_at": now.UTC().Format(serializers.DRFDateOnlyFormat)})
}

// UnarchiveIssue serves DELETE …/issues/{pk}/archive/ — clears archived_at and
// answers 204 with no body.
func (h *Handler) UnarchiveIssue(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if !middleware.ProjectRoleAtLeast(e, middleware.RoleMember) {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}
	issue := h.projectIssue(scope, pathUUID(e, "issue_id", "pk"))
	if issue == nil || issue.GetString("archived_at") == "" {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	issue.Set("archived_at", "")
	if err := h.App.Save(issue); err != nil {
		return serializers.Validation(e, "detail", err.Error())
	}
	return e.NoContent(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// issue_relation expansion for issues-detail
// ---------------------------------------------------------------------------

// issueRelationList expands the relations an issue DECLARES (rows where
// issue = this issue), projected exactly like the embedded
// IssueRelationSerializer entries the list detail serializer emits.
func (h *Handler) issueRelationList(issue *core.Record) []map[string]any {
	rows, err := h.App.FindRecordsByFilter(
		"issue_relations",
		"issue = {:issue} && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"issue": issue.Id},
	)
	out := make([]map[string]any, 0)
	if err != nil {
		return out
	}
	for _, row := range rows {
		related := h.findRecord("issues", row.GetString("related_issue"))
		if related == nil {
			continue
		}
		out = append(out, h.relationIssueJSON(related, row.GetString("relation_type")))
	}
	return out
}
