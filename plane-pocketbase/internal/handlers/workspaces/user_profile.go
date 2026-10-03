package workspaces

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// This file implements Plane's workspace user-profile surface, which the web
// app's `/[workspaceSlug]/profile/[userId]/` route tree reads:
//
//	GET /api/workspaces/{slug}/user-profile/{user_id}/
//	GET /api/workspaces/{slug}/user-stats/{user_id}/
//	GET /api/workspaces/{slug}/user-activity/{user_id}/
//
// Contract: apps/api/plane/app/views/workspace/user.py
// (WorkspaceUserProfileEndpoint / WorkspaceUserProfileStatsEndpoint /
// WorkspaceUserActivityEndpoint) and apps/api/plane/app/urls/workspace.py.
//
// These three paths are easy to mistake for "already working": before they were
// registered, Go's ServeMux matched them against the *subtree* pattern
// `/api/workspaces/{slug}/` and answered with the workspace object plus HTTP
// 200. The frontend then crashed on `userProfile.state_distribution.find(...)`
// (a workspace has no `state_distribution`) instead of surfacing a 404. The
// route fix lives in internal/router/workspace.go (`/{$}`).

// issueObjectsPredicate is the SQL translation of Django's `Issue.issue_objects`
// (plane/db/models/issue.py IssueManager.get_queryset): issues that are not
// soft-deleted, not archived, not drafts, not in an archived project, and not
// in the Triage state group. `p` must be the projects table alias and `s` the
// states alias.
const issueObjectsPredicate = `i.deleted_at = ''
	AND i.archived_at = ''
	AND i.is_draft = 0
	AND p.archived_at = ''
	AND s."group" != 'triage'`

// targetMember resolves the `{user_id}` path parameter to a user who is an
// active member of the resolved workspace. Django uses `<uuid:user_id>` and
// `.get(... is_active=True)`, so an unknown id, a non-member, or an inactive
// membership are all 404s. On failure it has already written the response.
func (h *Handler) targetMember(e *core.RequestEvent) *core.Record {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		_ = serializers.NotFound(e, "")
		return nil
	}

	user, _ := serializers.FindByUUIDOrID(h.App, "users", e.Request.PathValue("user_id"))
	if user == nil {
		_ = serializers.NotFound(e, "")
		return nil
	}

	member, _ := h.App.FindFirstRecordByFilter(
		"workspace_members",
		"workspace = {:workspace} && member = {:member} && is_active = true && deleted_at = ''",
		dbx.Params{"workspace": scope.Workspace.Id, "member": user.Id},
	)
	if member == nil {
		_ = serializers.NotFound(e, "")
		return nil
	}
	return user
}

// UserProfile serves GET /api/workspaces/{slug}/user-profile/{user_id}/
// (WorkspaceUserProfileEndpoint.get).
//
// `project_data` is only populated for a requesting member with role >= 15
// (Member or Admin); a Guest gets an empty array. Each row carries the target
// user's per-project issue tallies, not the caller's.
func (h *Handler) UserProfile(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	target := h.targetMember(e)
	if target == nil {
		return nil
	}

	projectData := []any{}
	if scope.WorkspaceRole >= middleware.RoleMember && user != nil {
		rows, err := h.profileProjects(scope, user, target)
		if err != nil {
			return err
		}
		projectData = rows
	}

	avatar := serializers.NullableText(target, "avatar")
	cover := serializers.NullableText(target, "cover_image")
	return e.JSON(http.StatusOK, map[string]any{
		"project_data": projectData,
		"user_data": map[string]any{
			"email":           serializers.NullableText(target, "email"),
			"first_name":      serializers.Text(target, "first_name"),
			"last_name":       serializers.Text(target, "last_name"),
			"avatar_url":      avatar,
			"cover_image_url": cover,
			"date_joined":     serializers.DateTime(target, "date_joined"),
			"user_timezone":   serializers.TextOr(target, "user_timezone", "UTC"),
			"display_name":    serializers.Text(target, "display_name"),
		},
	})
}

// profileProjects mirrors the annotated `Project.objects.filter(...)` of
// WorkspaceUserProfileEndpoint.get: the projects of this workspace that the
// *requesting* user is an active member of and that are not archived, each
// annotated with the *target* user's issue counts.
//
// `projects/details/`-style visibility does not apply here — Django filters on
// `project_projectmember__member=request.user`, so an Admin who is not a member
// of a project does not see it.
func (h *Handler) profileProjects(scope *middleware.Scope, requesting, target *core.Record) ([]any, error) {
	const query = `
		SELECT
			p.id AS id,
			(SELECT COUNT(DISTINCT i.id)
				FROM issues i
				LEFT JOIN states s ON s.id = i.state
				WHERE i.project = p.id AND ` + issueObjectsPredicate + `
					AND i.created_by = {:target}) AS created_issues,
			(SELECT COUNT(DISTINCT i.id)
				FROM issues i
				JOIN issue_assignees ia ON ia.issue = i.id AND ia.deleted_at = '' AND ia.assignee = {:target}
				LEFT JOIN states s ON s.id = i.state
				WHERE i.project = p.id AND ` + issueObjectsPredicate + `) AS assigned_issues,
			(SELECT COUNT(DISTINCT i.id)
				FROM issues i
				JOIN issue_assignees ia ON ia.issue = i.id AND ia.deleted_at = '' AND ia.assignee = {:target}
				LEFT JOIN states s ON s.id = i.state
				WHERE i.project = p.id AND ` + issueObjectsPredicate + `
					AND i.completed_at != '') AS completed_issues,
			(SELECT COUNT(DISTINCT i.id)
				FROM issues i
				JOIN issue_assignees ia ON ia.issue = i.id AND ia.deleted_at = '' AND ia.assignee = {:target}
				LEFT JOIN states s ON s.id = i.state
				WHERE i.project = p.id AND ` + issueObjectsPredicate + `
					AND s."group" IN ('backlog', 'unstarted', 'started')) AS pending_issues
		FROM projects p
		JOIN project_members pm
			ON pm.project = p.id AND pm.is_active = 1 AND pm.deleted_at = ''
		WHERE p.workspace = {:workspace}
			AND p.deleted_at = ''
			AND p.archived_at = ''
			AND pm.member = {:requesting}
		ORDER BY p.name`

	// NOTE: Plane's Project model has no `sort_order` column (checked against
	// plane/db/models/project.py), so Django's `.values(...)` here is unordered
	// and the name ordering below is ours, matching `ProjectViewSet.list_detail`.

	var rows []struct {
		ID              string `db:"id"`
		CreatedIssues   int    `db:"created_issues"`
		AssignedIssues  int    `db:"assigned_issues"`
		CompletedIssues int    `db:"completed_issues"`
		PendingIssues   int    `db:"pending_issues"`
	}
	err := h.App.DB().NewQuery(query).Bind(dbx.Params{
		"workspace":  scope.Workspace.Id,
		"requesting": requesting.Id,
		"target":     target.Id,
	}).All(&rows)
	if err != nil {
		return nil, err
	}

	out := make([]any, 0, len(rows))
	for _, row := range rows {
		project, _ := h.App.FindRecordById("projects", row.ID)
		if project == nil {
			continue
		}
		out = append(out, map[string]any{
			"id":               serializers.UUID(project),
			"logo_props":       serializers.JSONOrDefault(project, "logo_props", map[string]any{"in_use": "icon"}),
			"created_issues":   row.CreatedIssues,
			"assigned_issues":  row.AssignedIssues,
			"completed_issues": row.CompletedIssues,
			"pending_issues":   row.PendingIssues,
		})
	}
	return out, nil
}

// UserStats serves GET /api/workspaces/{slug}/user-stats/{user_id}/
// (WorkspaceUserProfileStatsEndpoint.get) — the numbers behind the profile
// overview's stat cards and its two distribution charts.
//
// Django also applies `issue_filters(request.query_params, "GET")` here, but
// the frontend calls this endpoint with no query parameters at all
// (`getUserProfileData` passes none), so only the unfiltered projection is
// implemented. A filtered request gets the unfiltered answer rather than an
// error, which is the safer failure for a read-only dashboard.
func (h *Handler) UserStats(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	target := h.targetMember(e)
	if target == nil {
		return nil
	}
	if user == nil {
		return serializers.NotFound(e, "")
	}

	// Every aggregate below shares this join: the issue must live in a project
	// the *requesting* user is an active member of (Django's
	// `project__project_projectmember__member=request.user`).
	const from = `FROM issues i
		JOIN projects p ON p.id = i.project
		LEFT JOIN states s ON s.id = i.state
		JOIN project_members pm
			ON pm.project = p.id AND pm.is_active = 1 AND pm.deleted_at = ''
		WHERE i.workspace = {:workspace}
			AND pm.member = {:requesting}
			AND ` + issueObjectsPredicate

	// `assigned` additionally requires a live assignee row for the target user.
	const assigned = ` AND EXISTS (
			SELECT 1 FROM issue_assignees ia
			WHERE ia.issue = i.id AND ia.assignee = {:target} AND ia.deleted_at = ''
		)`

	params := dbx.Params{
		"workspace":  scope.Workspace.Id,
		"requesting": user.Id,
		"target":     target.Id,
	}

	var stateRows []struct {
		StateGroup string `db:"state_group"`
		StateCount int    `db:"state_count"`
	}
	if err := h.App.DB().NewQuery(
		`SELECT s."group" AS state_group, COUNT(*) AS state_count ` + from + assigned +
			` GROUP BY s."group" ORDER BY s."group"`,
	).Bind(params).All(&stateRows); err != nil {
		return err
	}
	stateDistribution := make([]any, 0, len(stateRows))
	for _, r := range stateRows {
		stateDistribution = append(stateDistribution, map[string]any{
			"state_group": r.StateGroup,
			"state_count": r.StateCount,
		})
	}

	// Django orders the priority buckets urgent → none via a Case expression and
	// drops empty buckets (`filter(priority_count__gte=1)`).
	var priorityRows []struct {
		Priority      string `db:"priority"`
		PriorityCount int    `db:"priority_count"`
	}
	if err := h.App.DB().NewQuery(
		`SELECT i.priority AS priority, COUNT(*) AS priority_count ` + from + assigned +
			` GROUP BY i.priority`,
	).Bind(params).All(&priorityRows); err != nil {
		return err
	}
	counts := map[string]int{}
	for _, r := range priorityRows {
		counts[r.Priority] = r.PriorityCount
	}
	priorityDistribution := []any{}
	for _, p := range []string{"urgent", "high", "medium", "low", "none"} {
		if counts[p] > 0 {
			priorityDistribution = append(priorityDistribution, map[string]any{
				"priority":       p,
				"priority_count": counts[p],
			})
		}
	}

	total := func(where string) int {
		var n int
		if err := h.App.DB().NewQuery(`SELECT COUNT(DISTINCT i.id) ` + from + where).
			Bind(params).Row(&n); err != nil {
			return 0
		}
		return n
	}

	createdIssues := total(` AND i.created_by = {:target}`)
	assignedIssues := total(assigned)
	completedIssues := total(assigned + ` AND s."group" = 'completed'`)
	pendingIssues := total(assigned + ` AND s."group" NOT IN ('completed', 'cancelled')`)

	var subscribedIssues int
	if err := h.App.DB().NewQuery(`
		SELECT COUNT(DISTINCT i.id)
		FROM issue_subscribers sub
		JOIN issues i ON i.id = sub.issue
		JOIN projects p ON p.id = i.project
		LEFT JOIN states s ON s.id = i.state
		JOIN project_members pm
			ON pm.project = p.id AND pm.is_active = 1 AND pm.deleted_at = ''
		WHERE i.workspace = {:workspace}
			AND pm.member = {:requesting}
			AND sub.subscriber = {:target}
			AND sub.deleted_at = ''
			AND ` + issueObjectsPredicate,
	).Bind(params).Row(&subscribedIssues); err != nil {
		subscribedIssues = 0
	}

	present, upcoming := h.profileCycles(scope, user, target)

	return e.JSON(http.StatusOK, map[string]any{
		"state_distribution":    stateDistribution,
		"priority_distribution": priorityDistribution,
		"created_issues":        createdIssues,
		"assigned_issues":       assignedIssues,
		"completed_issues":      completedIssues,
		"pending_issues":        pendingIssues,
		"subscribed_issues":     subscribedIssues,
		"present_cycles":        present,
		"upcoming_cycles":       upcoming,
	})
}

// profileCycles reproduces the `present_cycles` / `upcoming_cycles` values of
// WorkspaceUserProfileStatsEndpoint: the cycles whose date window contains now
// (present) or starts in the future (upcoming), restricted to cycles holding at
// least one issue assigned to the target user. Django deliberately does not
// scope these by project membership, so neither does this.
func (h *Handler) profileCycles(scope *middleware.Scope, _ *core.Record, target *core.Record) ([]any, []any) {
	var rows []struct {
		Name      string `db:"name"`
		ID        string `db:"id"`
		ProjectID string `db:"project_id"`
		State     string `db:"state"`
	}
	err := h.App.DB().NewQuery(`
		SELECT c.name AS name, c.id AS id, c.project AS project_id,
			CASE
				WHEN c.start_date > :now THEN 'upcoming'
				WHEN c.end_date > :now THEN 'present'
				ELSE 'past'
			END AS state
		FROM cycle_issues ci
		JOIN cycles c ON c.id = ci.cycle
		JOIN issue_assignees ia ON ia.issue = ci.issue AND ia.deleted_at = '' AND ia.assignee = {:target}
		WHERE c.workspace = {:workspace}
			AND c.deleted_at = ''
			AND ci.deleted_at = ''
			AND c.end_date > :now
		GROUP BY c.id
		ORDER BY c.start_date`).Bind(dbx.Params{
		"workspace": scope.Workspace.Id,
		"target":    target.Id,
		"now":       serializers.Now(),
	}).All(&rows)
	if err != nil {
		return []any{}, []any{}
	}

	present := []any{}
	upcoming := []any{}
	for _, r := range rows {
		project, _ := h.App.FindRecordById("projects", r.ProjectID)
		entry := map[string]any{
			"cycle__name":       r.Name,
			"cycle__id":         r.ID,
			"cycle__project_id": serializers.UUID(project),
		}
		if r.State == "upcoming" {
			upcoming = append(upcoming, entry)
		} else {
			present = append(present, entry)
		}
	}
	return present, upcoming
}

// activityOrderBy mirrors plane.utils.order_queryset.sanitize_order_by with
// ACTIVITY_ORDER_BY_ALLOWLIST = {created_at, updated_at}. Only a single leading
// "-" is honoured; anything else falls back to the default.
func activityOrderBy(raw string) string {
	const def = "-created_at"
	if raw == "" {
		return def
	}
	desc := strings.HasPrefix(raw, "-")
	bare := raw
	if desc {
		bare = raw[1:]
	}
	if strings.HasPrefix(bare, "-") {
		return def
	}
	if bare != "created_at" && bare != "updated_at" {
		return def
	}
	if desc {
		return "-" + bare
	}
	return bare
}

// UserActivity serves GET /api/workspaces/{slug}/user-activity/{user_id}/
// (WorkspaceUserActivityEndpoint.get) — the profile page's activity feed,
// paginated with Plane's cursor envelope.
//
// The feed excludes the `comment`/`vote`/`reaction`/`draft` verbs (Django
// filters them out because those have their own renderers), and is scoped to
// projects the *requesting* user is an active member of.
func (h *Handler) UserActivity(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	target := h.targetMember(e)
	if target == nil {
		return nil
	}
	if user == nil {
		return serializers.NotFound(e, "")
	}

	perPage, cursor := serializers.ListParams(e, 100)

	// Django scopes the feed to projects the *requesting* user is an active
	// member of, that are not archived. Resolve that set up front and express it
	// as an explicit id list rather than a relation traversal inside the filter
	// expression — the traversal is easy to get subtly wrong, and an empty set
	// must produce an empty feed rather than every activity in the workspace.
	projectIDs := h.activityProjectIDs(scope, user, e.Request.URL.Query()["project"])
	if len(projectIDs) == 0 {
		return e.JSON(http.StatusOK, emptyActivityEnvelope(perPage))
	}

	params := dbx.Params{"workspace": scope.Workspace.Id, "actor": target.Id}
	clauses := make([]string, 0, len(projectIDs))
	for i, id := range projectIDs {
		key := "proj" + strconv.Itoa(i)
		clauses = append(clauses, "project = {:"+key+"}")
		params[key] = id
	}

	// The four excluded `field` values have dedicated renderers in the UI and
	// are filtered out server-side by WorkspaceUserActivityEndpoint.
	filter := `workspace = {:workspace} && actor = {:actor} && deleted_at = ''
		&& field != 'comment' && field != 'vote' && field != 'reaction' && field != 'draft'
		&& (` + strings.Join(clauses, " || ") + `)`

	envelope, err := serializers.Paginate(h.App, serializers.ListQuery{
		Collection: "issue_activities",
		Filter:     filter,
		Params:     params,
		Sort:       activityOrderBy(e.Request.URL.Query().Get("order_by")),
		PerPage:    perPage,
		Cursor:     cursor,
	}, func(activity *core.Record) any {
		return serializers.IssueActivity(h.App, activity)
	})
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, envelope)
}

// activityProjectIDs returns the PocketBase ids of the workspace's projects the
// requesting user may see in the feed: active project membership, project not
// archived and not deleted.
//
// `requested` is the repeated `?project=<uuid>` query parameter, given as Plane
// UUIDs; when non-empty the result is intersected with it. A UUID that does not
// resolve is simply dropped, matching Django's `project__in=[...]` semantics.
func (h *Handler) activityProjectIDs(scope *middleware.Scope, requesting *core.Record, requested []string) []string {
	members, err := h.App.FindRecordsByFilter(
		"project_members",
		"member = {:member} && workspace = {:workspace} && is_active = true && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"member": requesting.Id, "workspace": scope.Workspace.Id},
	)
	if err != nil {
		return nil
	}

	var allowed map[string]struct{}
	if len(requested) > 0 {
		allowed = make(map[string]struct{}, len(requested))
		for _, uuid := range requested {
			if project, _ := serializers.FindByUUID(h.App, "projects", uuid); project != nil {
				allowed[project.Id] = struct{}{}
			}
		}
	}

	ids := make([]string, 0, len(members))
	seen := make(map[string]struct{}, len(members))
	for _, m := range members {
		id := m.GetString("project")
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		if allowed != nil {
			if _, ok := allowed[id]; !ok {
				continue
			}
		}
		project, _ := h.App.FindRecordById("projects", id)
		if project == nil || project.GetString("archived_at") != "" || project.GetString("deleted_at") != "" {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

// emptyActivityEnvelope is the cursor envelope Plane expects for an empty feed.
// The cursor shape matches what serializers.Paginate emits for page 0 so the
// frontend's "load more" logic behaves identically.
func emptyActivityEnvelope(perPage int) *serializers.Envelope {
	return &serializers.Envelope{
		NextCursor: serializers.Cursor{Value: float64(perPage), Offset: 1}.String(),
		PrevCursor: serializers.Cursor{Value: float64(perPage), Offset: -1, IsPrev: true}.String(),
		Results:    []any{},
	}
}
