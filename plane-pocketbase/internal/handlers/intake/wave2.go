package intake

import (
	"math"
	"net/http"
	"strconv"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Description-version endpoints for the intake work-item detail screen.
//
//	GET …/intake-work-items/{work_item_id}/description-versions/
//	GET …/intake-work-items/{work_item_id}/description-versions/{pk}/
//
// TABLE CHOICE (read from Django, not guessed): the backing table is
// `issue_description_versions`, NOT the generic `description_versions`.
// `IntakeWorkItemDescriptionVersionEndpoint` (apps/api/plane/app/views/intake/base.py:572)
// reads `IssueDescriptionVersion` (apps/api/plane/db/models/issue.py:782,
// `db_table = "issue_description_versions"`), a ProjectBaseModel keyed by the
// `issue` FK. The generic `DescriptionVersion` model
// (apps/api/plane/db/models/description.py:32, `db_table = "description_versions"`)
// is keyed by a `description` FK and backs pages/descriptions, not issues.
//
// `{work_item_id}` is the underlying **issue UUID** (Django resolves
// `Issue.objects.get(..., pk=work_item_id)`); `{pk}` is the version UUID.

// DescriptionVersionsList serves GET …/description-versions/.
//
// The list uses Django's *global* paginator (`plane/utils/global_paginator.py`),
// whose envelope is distinct from the BasePaginator envelope used elsewhere:
//
//	{prev_cursor, cursor, next_cursor, prev_page_results, next_page_results,
//	 page_count, total_results, total_pages, results}
//
// and whose rows are a `.values()` projection of the 10 fields below (not the
// detail serializer).
func (h *Handler) DescriptionVersionsList(e *core.RequestEvent) error {
	scope, issue, err := h.scopedWorkItem(e)
	if err != nil || issue == nil {
		return err
	}
	if !h.canViewWorkItem(e, scope, issue) {
		return e.JSON(http.StatusForbidden, map[string]any{"error": "You are not allowed to view this issue"})
	}

	rawCursor := e.Request.URL.Query().Get("cursor")
	cursor, err := serializers.ParseCursor(rawCursor, serializers.MaxPerPage)
	if err != nil {
		return serializers.BadRequest(e, "Invalid cursor format")
	}
	pageSize := int(cursor.Value)
	if pageSize <= 0 || pageSize > serializers.MaxPerPage {
		pageSize = serializers.MaxPerPage
	}
	page := cursor.Offset
	if page < 0 {
		page = 0
	}

	params := dbx.Params{"issue": issue.Id, "project": scope.Project.Id, "workspace": scope.Workspace.Id}
	filter := "issue = {:issue} && project = {:project} && workspace = {:workspace} && deleted_at = ''"

	total, err := h.countDescriptionVersions(filter, params)
	if err != nil {
		return err
	}
	offset := page * pageSize
	records, err := h.App.FindRecordsByFilter(
		"issue_description_versions", filter, "-created_at", pageSize, offset, params,
	)
	if err != nil {
		return err
	}

	results := make([]any, 0, len(records))
	for _, r := range records {
		results = append(results, descriptionVersionListJSON(h.App, r))
	}

	prevCursor := cursorString(pageSize, page-1)
	cur := cursorString(pageSize, page)
	var nextCursor any
	if offset+len(records) < total {
		nextCursor = cursorString(pageSize, page+1)
	}

	return e.JSON(http.StatusOK, map[string]any{
		"prev_cursor":       prevCursor,
		"cursor":            cur,
		"next_cursor":       nextCursor,
		"prev_page_results": page > 0,
		"next_page_results": nextCursor != nil,
		"page_count":        len(results),
		"total_results":     total,
		"total_pages":       int(math.Ceil(float64(total) / float64(pageSize))),
		"results":           results,
	})
}

// DescriptionVersionDetail serves GET …/description-versions/{pk}/
// (IssueDescriptionVersionDetailSerializer).
func (h *Handler) DescriptionVersionDetail(e *core.RequestEvent) error {
	scope, issue, err := h.scopedWorkItem(e)
	if err != nil || issue == nil {
		return err
	}
	if !h.canViewWorkItem(e, scope, issue) {
		return e.JSON(http.StatusForbidden, map[string]any{"error": "You are not allowed to view this issue"})
	}

	version, _ := serializers.FindByUUID(h.App, "issue_description_versions", e.Request.PathValue("pk"))
	if version == nil ||
		version.GetString("issue") != issue.Id ||
		version.GetString("project") != scope.Project.Id ||
		version.GetString("workspace") != scope.Workspace.Id {
		return serializers.NotFound(e, "")
	}
	return e.JSON(http.StatusOK, descriptionVersionDetailJSON(h.App, version))
}

// scopedWorkItem resolves the route's `work_item_id` (an issue UUID) within the
// project scope.
func (h *Handler) scopedWorkItem(e *core.RequestEvent) (*middleware.Scope, *core.Record, error) {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return nil, nil, serializers.NotFound(e, "")
	}
	issue, _ := serializers.FindByUUID(h.App, "issues", e.Request.PathValue("work_item_id"))
	if issue == nil ||
		issue.GetString("project") != scope.Project.Id ||
		issue.GetString("workspace") != scope.Workspace.Id ||
		issue.GetString("deleted_at") != "" {
		return scope, nil, serializers.NotFound(e, "")
	}
	return scope, issue, nil
}

// canViewWorkItem reproduces the endpoint's guest guard: a Guest who cannot view
// all features and did not create the work item is forbidden.
func (h *Handler) canViewWorkItem(e *core.RequestEvent, scope *middleware.Scope, issue *core.Record) bool {
	if scope.ProjectRole != middleware.RoleGuest || scope.Project.GetBool("guest_view_all_features") {
		return true
	}
	user := middleware.CurrentUser(e)
	return user != nil && issue.GetString("created_by") == user.Id
}

// countDescriptionVersions counts rows matching the same filter as the page.
func (h *Handler) countDescriptionVersions(filter string, params dbx.Params) (int, error) {
	records, err := h.App.FindRecordsByFilter("issue_description_versions", filter, "", 0, 0, params)
	if err != nil {
		return 0, err
	}
	return len(records), nil
}

// cursorString renders Django's global-paginator cursor "<page_size>:<page>:0".
// The page is NOT clamped: at page 0 the prev cursor is "<size>:-1:0", exactly
// as `global_paginator.paginate` emits it.
func cursorString(pageSize, page int) string {
	return strconv.Itoa(pageSize) + ":" + strconv.Itoa(page) + ":0"
}

// descriptionVersionListJSON mirrors the `.values(*required_fields)` projection
// used for list rows (no description body).
func descriptionVersionListJSON(app core.App, r *core.Record) map[string]any {
	return map[string]any{
		"id":            serializers.UUID(r),
		"workspace":     serializers.RelationUUID(app, r, "workspace"),
		"project":       serializers.RelationUUID(app, r, "project"),
		"issue":         serializers.RelationUUID(app, r, "issue"),
		"last_saved_at": serializers.DateTime(r, "last_saved_at"),
		"owned_by":      serializers.RelationUUID(app, r, "owned_by"),
		"created_at":    serializers.DateTime(r, "created_at"),
		"updated_at":    serializers.DateTime(r, "updated_at"),
		"created_by":    serializers.RelationUUID(app, r, "created_by"),
		"updated_by":    serializers.RelationUUID(app, r, "updated_by"),
	}
}

// descriptionVersionDetailJSON mirrors IssueDescriptionVersionDetailSerializer.
func descriptionVersionDetailJSON(app core.App, r *core.Record) map[string]any {
	return map[string]any{
		"id":                   serializers.UUID(r),
		"workspace":            serializers.RelationUUID(app, r, "workspace"),
		"project":              serializers.RelationUUID(app, r, "project"),
		"issue":                serializers.RelationUUID(app, r, "issue"),
		"description_binary":   nil,
		"description_html":     serializers.Text(r, "description_html"),
		"description_stripped": serializers.NullableText(r, "description_stripped"),
		"description_json":     serializers.JSONOrDefault(r, "description_json", map[string]any{}),
		"last_saved_at":        serializers.DateTime(r, "last_saved_at"),
		"owned_by":             serializers.RelationUUID(app, r, "owned_by"),
		"created_at":           serializers.DateTime(r, "created_at"),
		"updated_at":           serializers.DateTime(r, "updated_at"),
		"created_by":           serializers.RelationUUID(app, r, "created_by"),
		"updated_by":           serializers.RelationUUID(app, r, "updated_by"),
	}
}
