// Two workspace/project-level issue reads surfaced by the method-level audit:
//
//	GET /api/workspaces/{slug}/projects/{project_id}/v2/issues/
//	    (IssuePaginatedViewSet.list — same paginated envelope as /issues/)
//	GET /api/workspaces/{slug}/work-items/{project_identifier}-{sequence_id}/
//	    (IssueDetailIdentifierEndpoint — the deep link by human reference, e.g.
//	    "ENG-42"; the issue detail store calls it via retrieveWithIdentifier)
package issues

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// ListV2 serves GET /api/workspaces/{slug}/projects/{project_id}/v2/issues/
// (IssuePaginatedViewSet). Same cursor-paginated envelope and IssueSerializer
// items as List — the "v2" variant differs from the Django v1 list only in
// pagination internals this port already implements.
func (h *Handler) ListV2(e *core.RequestEvent) error {
	return h.listIssues(e, false)
}

// DetailByIdentifier serves GET
// /api/workspaces/{slug}/work-items/{project_identifier}-{sequence_id}/
// (IssueDetailIdentifierEndpoint). The reference splits on the LAST hyphen so
// multi-part identifiers survive; the identifier compares case-insensitively
// (Django uses __iexact in spirit — Plane identifiers are stored uppercase).
func (h *Handler) DetailByIdentifier(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	ref := e.Request.PathValue("ref")
	dash := strings.LastIndex(ref, "-")
	if dash <= 0 {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	identifier := strings.ToUpper(ref[:dash])
	sequence, err := strconv.Atoi(strings.TrimSpace(ref[dash+1:]))
	if err != nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}

	projects, err := h.App.FindRecordsByFilter(
		"projects",
		"workspace = {:ws} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"ws": scope.Workspace.Id},
	)
	if err != nil {
		return err
	}
	var project *core.Record
	for _, row := range projects {
		if strings.EqualFold(row.GetString("identifier"), identifier) {
			project = row
			break
		}
	}
	if project == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}

	issue, err := h.App.FindFirstRecordByFilter(
		"issues",
		"project = {:p} && sequence_id = {:seq} && deleted_at = ''",
		dbx.Params{"p": project.Id, "seq": sequence},
	)
	if err != nil || issue == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}

	// issue.read: the caller must be an active project member; a guest without
	// guest_view_all_features only sees issues they created (permissions.md §3.2).
	membership, _ := h.App.FindFirstRecordByFilter(
		"project_members",
		"project = {:p} && member = {:m} && is_active = true && deleted_at = ''",
		dbx.Params{"p": project.Id, "m": user.Id},
	)
	if membership == nil {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}
	role := membership.GetInt("role")
	if role < middleware.RoleMember && !project.GetBool("guest_view_all_features") &&
		issue.GetString("created_by") != user.Id {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}

	return e.JSON(http.StatusOK, serializers.Issue(h.App, issue))
}
