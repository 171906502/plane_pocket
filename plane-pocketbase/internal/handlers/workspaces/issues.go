package workspaces

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// This file implements the workspace-level (cross-project) work-item reads:
//
//	GET /api/workspaces/{slug}/issues/         WorkspaceViewIssuesViewSet.list
//	GET /api/workspaces/{slug}/issues-detail/  (no Django route; see below)
//	GET /api/workspaces/{slug}/my-issues/      (no Django route; see below)
//
// `issues/` is the only one with a Django source of truth: it returns the same
// cursor-paginated envelope as the project-scoped list, but spans every project
// in the workspace the caller is an active member of (Django's
// `_get_project_permission_filters`). The other two are frontend call sites with
// no matching Django view; their shapes are documented at each handler.

// projectAccess is one row of "this caller can read issues in this project".
type projectAccess struct {
	// ID is the PocketBase project id (not the Plane UUID).
	ID string
	// Role is the caller's project role (5 Guest / 15 Member / 20 Admin).
	Role int
	// GuestAll mirrors Project.guest_view_all_features.
	GuestAll bool
}

// accessibleProjects returns the projects in the workspace the caller is an
// active member of, together with the role that governs what issues they may
// see. This mirrors Django's `_get_project_permission_filters`:
//
//	role > 5                                  -> all issues
//	role = 5 && guest_view_all_features        -> all issues
//	role = 5 && !guest_view_all_features       -> only issues they created
func (h *Handler) accessibleProjects(workspaceID, userID string) []projectAccess {
	var rows []struct {
		ID       string `db:"id"`
		Role     int    `db:"role"`
		GuestAll int    `db:"guest_all"`
	}
	err := h.App.DB().NewQuery(`
		SELECT p.id AS id, pm.role AS role, p.guest_view_all_features AS guest_all
		FROM project_members pm
		JOIN projects p ON p.id = pm.project
		WHERE pm.member = {:member}
			AND pm.is_active = 1
			AND pm.deleted_at = ''
			AND p.deleted_at = ''
			AND p.workspace = {:workspace}
		ORDER BY p.id`).Bind(dbx.Params{
		"member":    userID,
		"workspace": workspaceID,
	}).All(&rows)
	if err != nil {
		return nil
	}
	out := make([]projectAccess, 0, len(rows))
	for _, row := range rows {
		out = append(out, projectAccess{ID: row.ID, Role: row.Role, GuestAll: row.GuestAll != 0})
	}
	return out
}

// issueVisibilityClause folds the accessible projects into a PocketBase filter
// fragment plus the params it binds. It returns an empty string when the caller
// has no readable project (callers must then short-circuit to an empty result,
// since an empty OR group would otherwise match nothing anyway — but an empty
// filter fragment is easier to detect).
func issueVisibilityClause(accesses []projectAccess, userID string, params dbx.Params) string {
	ors := make([]string, 0, len(accesses))
	for i, pa := range accesses {
		key := fmt.Sprintf("proj%d", i)
		params[key] = pa.ID
		if pa.Role > middleware.RoleGuest || pa.GuestAll {
			ors = append(ors, fmt.Sprintf("project = {:%s}", key))
			continue
		}
		// Guest without guest_view_all_features: only their own issues.
		ors = append(ors, fmt.Sprintf("(project = {:%s} && created_by = {:member})", key))
	}
	if len(ors) == 0 {
		return ""
	}
	return "(" + strings.Join(ors, " || ") + ")"
}

// WorkspaceIssues serves GET /api/workspaces/{slug}/issues/
// (WorkspaceViewIssuesViewSet.list) — the cursor-paginated envelope whose
// `results` are ViewIssueListSerializer objects, spanning every project in the
// workspace the caller can read.
func (h *Handler) WorkspaceIssues(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	params := dbx.Params{"workspace": scope.Workspace.Id, "member": user.Id}
	filter := "workspace = {:workspace} && deleted_at = '' && archived_at = '' && is_draft = false && state.group != \"triage\" && project.archived_at = ''"
	if visibility := issueVisibilityClause(h.accessibleProjects(scope.Workspace.Id, user.Id), user.Id, params); visibility != "" {
		filter += " && " + visibility
	} else {
		// No readable project: match nothing rather than everything.
		filter += " && id = {:none}"
		params["none"] = ""
	}

	perPage, cursor := serializers.ListParams(e, serializers.MaxPerPage)
	envelope, err := serializers.Paginate(h.App, serializers.ListQuery{
		Collection: "issues",
		Filter:     filter,
		Params:     params,
		Sort:       workspaceOrderBy(e.Request.URL.Query().Get("order_by")),
		PerPage:    perPage,
		Cursor:     cursor,
	}, func(issue *core.Record) any {
		return serializers.Issue(h.App, issue)
	})
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, envelope)
}

// WorkspaceIssuesDetail serves GET /api/workspaces/{slug}/issues-detail/.
//
// There is no Django route for this path at the workspace level: the frontend
// (`workspace.service.ts::getViewIssues`) picks it over `/issues/` whenever the
// requested params include `expand=issue_relation`. Plane's own workspace-level
// detail variant does not exist in this checkout, so the response is the same
// cursor-paginated envelope as `/issues/` — the frontend consumes both through
// the identical `TIssuesResponse` type.
//
// FLAGGED: shape inferred from the sibling `/issues/` endpoint, not from Django.
func (h *Handler) WorkspaceIssuesDetail(e *core.RequestEvent) error {
	return h.WorkspaceIssues(e)
}

// MyIssues serves GET /api/workspaces/{slug}/my-issues/.
//
// There is no Django route for this path and no live caller: the frontend
// declares `user.service.ts::userIssues()` but never invokes it (verified by
// grep across apps/web). The declared return type is
// `{ [key: string]: TIssue[] } | TIssue[]`, so this returns the flat-array
// branch: the work items in this workspace currently assigned to the caller,
// most recent first.
//
// FLAGGED: shape inferred from the frontend type; there is no Django contract.
func (h *Handler) MyIssues(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	allowed := map[string]projectAccess{}
	for _, pa := range h.accessibleProjects(scope.Workspace.Id, user.Id) {
		allowed[pa.ID] = pa
	}

	assignments, err := h.App.FindRecordsByFilter(
		"issue_assignees",
		"assignee = {:member} && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"member": user.Id},
	)
	if err != nil {
		return err
	}

	seen := map[string]struct{}{}
	issues := make([]*core.Record, 0, len(assignments))
	for _, assignment := range assignments {
		issue, _ := h.App.FindRecordById("issues", assignment.GetString("issue"))
		if issue == nil {
			continue
		}
		if _, ok := seen[issue.Id]; ok {
			continue
		}
		if !h.issueVisibleTo(issue, scope.Workspace.Id, user.Id, allowed) {
			continue
		}
		seen[issue.Id] = struct{}{}
		issues = append(issues, issue)
	}
	// Newest first, matching the other issue feeds (`-created_at`).
	sort.SliceStable(issues, func(i, j int) bool {
		return issues[i].GetString("created_at") > issues[j].GetString("created_at")
	})

	results := make([]any, 0, len(issues))
	for _, issue := range issues {
		results = append(results, serializers.Issue(h.App, issue))
	}
	return e.JSON(http.StatusOK, results)
}

// issueVisibleTo reports whether the caller may see the issue under the same
// permission rules as WorkspaceIssues: not deleted / archived / draft / triage,
// project readable, and — for a guest without guest_view_all_features — only
// issues they created.
func (h *Handler) issueVisibleTo(issue *core.Record, workspaceID, userID string, allowed map[string]projectAccess) bool {
	if issue.GetString("workspace") != workspaceID {
		return false
	}
	if issue.GetString("deleted_at") != "" || issue.GetString("archived_at") != "" || issue.GetBool("is_draft") {
		return false
	}
	if state, _ := h.App.FindRecordById("states", issue.GetString("state")); state != nil && state.GetString("group") == "triage" {
		return false
	}
	access, ok := allowed[issue.GetString("project")]
	if !ok {
		return false
	}
	if access.Role == middleware.RoleGuest && !access.GuestAll && issue.GetString("created_by") != userID {
		return false
	}
	return true
}

// workspaceOrderBy maps Plane's `order_by` query parameter onto a PocketBase
// sort expression. It mirrors the project-scoped issue list so both feeds order
// identically.
func workspaceOrderBy(param string) string {
	switch param {
	case "created_at":
		return "created_at"
	case "-created_at", "":
		return "-created_at"
	case "updated_at":
		return "updated_at"
	case "-updated_at":
		return "-updated_at"
	case "sort_order":
		return "sort_order"
	case "-sort_order":
		return "-sort_order"
	case "sequence_id":
		return "sequence_id"
	case "-sequence_id":
		return "-sequence_id"
	case "priority":
		return "priority"
	case "-priority":
		return "-priority"
	default:
		return "-created_at"
	}
}
