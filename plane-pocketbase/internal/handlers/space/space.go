// Package space implements Plane's public deploy-board API
// (`/api/public/…`, group `space`). Everything readable here is anonymous by
// design, but strictly scoped to the project behind the `anchor` (the
// published DeployBoard). Django source: apps/api/plane/space/**.
package space

import (
	"errors"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Handler carries the app handle for the space domain.
type Handler struct {
	App core.App
}

// New builds the handler.
func New(app core.App) *Handler { return &Handler{App: app} }

// board resolves the published DeployBoard behind an anchor, or nil.
func (h *Handler) board(anchor string) *core.Record {
	rec, err := h.App.FindFirstRecordByFilter(
		"deploy_boards",
		"anchor = {:anchor} && entity_name = 'project'",
		dbx.Params{"anchor": anchor},
	)
	if err != nil {
		return nil
	}
	return rec
}

// boardByID resolves a board by raw record id (the anchor endpoints pass the
// anchor string, which Plane's DeployBoard.anchor is a distinct unique value
// from the row id — look it up by anchor first, id second).
func (h *Handler) boardByID(idOrAnchor string) *core.Record {
	if b := h.board(idOrAnchor); b != nil {
		return b
	}
	rec, err := h.App.FindFirstRecordByFilter(
		"deploy_boards",
		"id = {:id} && entity_name = 'project'",
		dbx.Params{"id": idOrAnchor},
	)
	if err != nil {
		return nil
	}
	return rec
}

// AnchorForProject serves GET /api/public/workspaces/{slug}/projects/{project_id}/anchor/
// (WorkspaceProjectAnchorEndpoint): the published board for a project, 404 if
// the project is not published.
func (h *Handler) AnchorForProject(e *core.RequestEvent) error {
	slug := e.Request.PathValue("slug")
	projectID := e.Request.PathValue("project_id")
	ws, err := h.App.FindFirstRecordByFilter("workspaces", "slug = {:slug}", dbx.Params{"slug": slug})
	if err != nil || ws == nil {
		return serializers.NotFound(e, "")
	}
	project, _ := serializers.FindByUUIDOrID(h.App, "projects", projectID)
	if project == nil || project.GetString("workspace") != ws.Id {
		return serializers.NotFound(e, "")
	}
	board, err := h.App.FindFirstRecordByFilter(
		"deploy_boards",
		"workspace = {:ws} && project = {:project} && entity_name = 'project'",
		dbx.Params{"ws": ws.Id, "project": project.Id},
	)
	if err != nil || board == nil {
		return serializers.NotFound(e, "")
	}
	return e.JSON(http.StatusOK, serializers.DeployBoard(h.App, board))
}

// Meta serves GET /api/public/anchor/{anchor}/meta/ (ProjectMetaDataEndpoint).
func (h *Handler) Meta(e *core.RequestEvent) error {
	board := h.boardByID(e.Request.PathValue("anchor"))
	if board == nil {
		return serializers.NotFound(e, "Project is not published")
	}
	project, _ := serializers.FindByUUIDOrID(h.App, "projects", board.GetString("project"))
	if project == nil {
		return serializers.NotFound(e, "Project is not published")
	}
	return e.JSON(http.StatusOK, serializers.ProjectLite(project))
}

// Settings serves GET /api/public/anchor/{anchor}/settings/
// (ProjectDeployBoardPublicSettingsEndpoint).
func (h *Handler) Settings(e *core.RequestEvent) error {
	board := h.boardByID(e.Request.PathValue("anchor"))
	if board == nil {
		return serializers.NotFound(e, "Project is not published")
	}
	return e.JSON(http.StatusOK, serializers.DeployBoard(h.App, board))
}

// Members serves GET /api/public/anchor/{anchor}/members/ (ProjectMembersEndpoint).
func (h *Handler) Members(e *core.RequestEvent) error {
	board := h.boardByID(e.Request.PathValue("anchor"))
	if board == nil {
		return serializers.NotFound(e, "Invalid anchor")
	}
	recs, err := h.App.FindRecordsByFilter(
		"project_members",
		"project = {:project} && workspace = {:ws} && is_active = 1",
		"created_at",
		0, 0,
		dbx.Params{"project": board.GetString("project"), "ws": board.GetString("workspace")},
	)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(recs))
	for _, pm := range recs {
		member := map[string]any{"id": serializers.UUID(pm), "member": serializers.RelationUUID(h.App, pm, "member")}
		if u, _ := serializers.FindByUUIDOrID(h.App, "users", pm.GetString("member")); u != nil {
			member["member__display_name"] = u.GetString("display_name")
			member["member__avatar"] = u.GetString("avatar")
		} else {
			member["member__display_name"] = nil
			member["member__avatar"] = nil
		}
		out = append(out, member)
	}
	return e.JSON(http.StatusOK, out)
}

// States serves GET /api/public/anchor/{anchor}/states/ (ProjectStatesEndpoint
// — excludes Triage).
func (h *Handler) States(e *core.RequestEvent) error {
	board := h.boardByID(e.Request.PathValue("anchor"))
	if board == nil {
		return serializers.NotFound(e, "Invalid anchor")
	}
	recs, err := h.App.FindRecordsByFilter(
		"states",
		// Django writes `~Q(name="Triage")` but the exclusion actually comes
		// from the default manager dropping `group="triage"`. Keep both: the
		// name filter for literal parity, the group filter for robustness.
		"project = {:project} && workspace = {:ws} && name != 'Triage' && group != 'triage'",
		"sequence",
		0, 0,
		dbx.Params{"project": board.GetString("project"), "ws": board.GetString("workspace")},
	)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(recs))
	for _, s := range recs {
		out = append(out, map[string]any{
			"id":       serializers.UUID(s),
			"name":     s.GetString("name"),
			"group":    s.GetString("group"),
			"color":    s.GetString("color"),
			"sequence": s.GetInt("sequence"),
		})
	}
	return e.JSON(http.StatusOK, out)
}

// Labels serves GET /api/public/anchor/{anchor}/labels/ (ProjectLabelsEndpoint).
func (h *Handler) Labels(e *core.RequestEvent) error {
	board := h.boardByID(e.Request.PathValue("anchor"))
	if board == nil {
		return serializers.NotFound(e, "Invalid anchor")
	}
	recs, err := h.App.FindRecordsByFilter(
		"labels",
		"project = {:project} && workspace = {:ws}",
		"created_at",
		0, 0,
		dbx.Params{"project": board.GetString("project"), "ws": board.GetString("workspace")},
	)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(recs))
	for _, l := range recs {
		out = append(out, map[string]any{
			"id":     serializers.UUID(l),
			"name":   l.GetString("name"),
			"color":  l.GetString("color"),
			"parent": serializers.RelationUUID(h.App, l, "parent"),
		})
	}
	return e.JSON(http.StatusOK, out)
}

// simpleNamed is the shared shape of the public cycles/modules lists:
// Django `.values("id", "name")`.
func (h *Handler) simpleNamed(e *core.RequestEvent, collection string, extra string) error {
	board := h.boardByID(e.Request.PathValue("anchor"))
	if board == nil {
		return serializers.NotFound(e, "Invalid anchor")
	}
	filter := "project = {:project} && workspace = {:ws} && deleted_at = ''"
	if extra != "" {
		filter += " && " + extra
	}
	recs, err := h.App.FindRecordsByFilter(collection, filter, "-created_at", 0, 0,
		dbx.Params{"project": board.GetString("project"), "ws": board.GetString("workspace")})
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		out = append(out, map[string]any{"id": serializers.UUID(r), "name": r.GetString("name")})
	}
	return e.JSON(http.StatusOK, out)
}

// Cycles serves GET /api/public/anchor/{anchor}/cycles/.
func (h *Handler) Cycles(e *core.RequestEvent) error {
	return h.simpleNamed(e, "cycles", "")
}

// Modules serves GET /api/public/anchor/{anchor}/modules/.
func (h *Handler) Modules(e *core.RequestEvent) error {
	return h.simpleNamed(e, "modules", "")
}

// issueInBoard checks the issue belongs to the board's project.
func (h *Handler) issueInBoard(board *core.Record, issueID string) *core.Record {
	issue, _ := serializers.FindByUUIDOrID(h.App, "issues", issueID)
	if issue == nil || issue.GetString("project") != board.GetString("project") {
		return nil
	}
	return issue
}

// Issues serves GET /api/public/anchor/{anchor}/issues/
// (ProjectIssuesPublicEndpoint).
//
// The Space app *always* asks for `group_by=state_id`: its list and kanban
// layouts both group by state (`store/helpers/filter.helpers.ts` maps
// `groupedBy: "state"` through `EIssueGroupByToServerOptions`), and
// `BasePaginator` answers a grouped request with an OBJECT keyed by state id
// rather than an array. The Space store consumes exactly that shape —
// `processIssueResponse` walks `results[groupId].results` /
// `results[groupId].total_results`, and the layout reads
// `groupedIssueIds[stateId]` (store/helpers/base-issues.store.ts).
//
// Returning a flat array (what this handler used to do) puts every issue into
// the synthetic "All Issues" bucket instead, so every real state group renders
// as 0 and the published board looks empty even though the issues *were*
// fetched — the exact symptom that made run 5's screenshot 29 a bare shell.
func (h *Handler) Issues(e *core.RequestEvent) error {
	board := h.boardByID(e.Request.PathValue("anchor"))
	if board == nil {
		return serializers.NotFound(e, "Project is not published")
	}

	perPage, cursor := serializers.ListParams(e, 50)
	query := e.Request.URL.Query()

	params := dbx.Params{"project": board.GetString("project"), "ws": board.GetString("workspace")}
	filter := "project = {:project} && workspace = {:ws} && deleted_at = '' && is_draft = false"
	sort := issueOrderBy(query.Get("order_by"))

	if groupBy := query.Get("group_by"); groupBy != "" {
		return h.groupedIssues(e, board, groupBy, filter, params, sort, perPage, cursor)
	}

	env, err := serializers.Paginate(h.App, serializers.ListQuery{
		Collection: "issues",
		Filter:     filter,
		Params:     params,
		Sort:       sort,
		PerPage:    perPage,
		Cursor:     cursor,
	}, func(r *core.Record) any { return serializers.Issue(h.App, r) })
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, env)
}

// groupedIssues answers the grouped form of the issue list.
//
// Only the two groupings the published board can actually reach are supported;
// Django's `issue_group_values` returns an empty list for anything it does not
// know, and the paginator then emits `results: {}` — so an unknown `group_by`
// yields an empty board rather than an error. We keep that behaviour.
func (h *Handler) groupedIssues(
	e *core.RequestEvent,
	board *core.Record,
	groupBy, filter string,
	params dbx.Params,
	sort string,
	perPage int,
	cursor string,
) error {
	projectID := board.GetString("project")
	wsID := board.GetString("workspace")

	var (
		groupKeys   []serializers.GroupKey
		groupFilter string
	)

	switch groupBy {
	case "state_id":
		// `State.objects.filter(is_triage=False, workspace__slug=slug)` then
		// `.filter(project_id=project_id)` — and `State.objects` is
		// `StateManager`, which drops `group="triage"` (db/models/state.py:65).
		// Triage is therefore excluded by GROUP, not by the `is_triage` flag:
		// Django's `DEFAULT_STATES` never sets that flag, so every seeded state
		// — Triage included — carries `is_triage=False`.
		states, err := h.App.FindRecordsByFilter(
			"states",
			"project = {:project} && workspace = {:ws} && group != 'triage' && is_triage = false",
			"sequence", 0, 0,
			dbx.Params{"project": projectID, "ws": wsID},
		)
		if err != nil {
			return err
		}
		// Bucket keys are Plane UUIDs because that is what the Space app keys
		// `groupedIssueIds` by (the states endpoint hands it UUIDs); the
		// PocketBase record id is only what the `state` relation stores.
		groupKeys = make([]serializers.GroupKey, 0, len(states))
		for _, s := range states {
			groupKeys = append(groupKeys, serializers.GroupKey{
				ID:    serializers.UUID(s),
				Match: s.Id,
			})
		}
		groupFilter = "state = {:group}"
	case "state__group":
		for _, g := range []string{"backlog", "unstarted", "started", "completed", "cancelled"} {
			groupKeys = append(groupKeys, serializers.GroupKey{ID: g, Match: g})
		}
		groupFilter = "state.group = {:group}"
	}

	env, err := serializers.PaginateGrouped(h.App, serializers.GroupQuery{
		Collection:  "issues",
		Filter:      filter,
		Params:      params,
		Sort:        sort,
		PerPage:     perPage,
		Cursor:      cursor,
		GroupedBy:   groupBy,
		GroupFilter: groupFilter,
		GroupKeys:   groupKeys,
	}, func(r *core.Record) any { return serializers.Issue(h.App, r) })
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, env)
}

// issueOrderBy maps Plane's `order_by` values onto PocketBase sort expressions.
//
// The previous inline version stripped the leading "-" and then passed the bare
// field name to PocketBase, which reads a bare name as ASCENDING — so the
// board's default (`-created_at`, newest first) was silently served oldest
// first. This mirrors `issues.orderBy` and keeps the direction.
func issueOrderBy(param string) string {
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

// IssueDetail serves GET /api/public/anchor/{anchor}/issues/{issue_id}/
// (IssueRetrievePublicEndpoint) — the issue plus reactions and votes with
// actor details.
func (h *Handler) IssueDetail(e *core.RequestEvent) error {
	board := h.boardByID(e.Request.PathValue("anchor"))
	if board == nil {
		return serializers.NotFound(e, "Project is not published")
	}
	issue := h.issueInBoard(board, e.Request.PathValue("issue_id"))
	if issue == nil {
		return serializers.NotFound(e, "Issue not found")
	}
	out := serializers.Issue(h.App, issue)
	out["reactions"] = h.reactionList("issue_reactions", "issue", issue.Id)
	out["votes"] = h.voteList(issue.Id)
	out["comments"] = h.commentList(issue.Id)
	return e.JSON(http.StatusOK, out)
}

// commentList loads all comments of an issue (the public detail prefetches
// without the EXTERNAL filter — only the comments LIST endpoint filters).
func (h *Handler) commentList(issueID string) []map[string]any {
	recs, err := h.App.FindRecordsByFilter("issue_comments", "issue = {:id}", "created_at", 0, 0, dbx.Params{"id": issueID})
	if err != nil {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(recs))
	for _, c := range recs {
		out = append(out, serializers.Comment(h.App, c))
	}
	return out
}

// reactionList loads reactions with actor details for a parent record.
func (h *Handler) reactionList(collection, field, id string) []map[string]any {
	recs, err := h.App.FindRecordsByFilter(collection, field+" = {:id}", "created_at", 0, 0, dbx.Params{"id": id})
	if err != nil {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		actor := map[string]any{"id": nil, "first_name": "", "last_name": "", "avatar": "", "avatar_url": nil}
		if u, _ := serializers.FindByUUIDOrID(h.App, "users", r.GetString("actor")); u != nil {
			actor["id"] = serializers.UUID(u)
			actor["first_name"] = u.GetString("first_name")
			actor["last_name"] = u.GetString("last_name")
			actor["avatar"] = u.GetString("avatar")
		}
		out = append(out, map[string]any{
			"id":            serializers.UUID(r),
			"reaction":      r.GetString("reaction"),
			"actor":         serializers.RelationUUID(h.App, r, "actor"),
			"actor_details": actor,
		})
	}
	return out
}

// voteList loads votes with actor details for an issue.
func (h *Handler) voteList(issueID string) []map[string]any {
	recs, err := h.App.FindRecordsByFilter("issue_votes", "issue = {:id}", "created_at", 0, 0, dbx.Params{"id": issueID})
	if err != nil {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		actor := map[string]any{"id": nil, "first_name": "", "last_name": "", "avatar": "", "avatar_url": nil}
		if u, _ := serializers.FindByUUIDOrID(h.App, "users", r.GetString("actor")); u != nil {
			actor["id"] = serializers.UUID(u)
			actor["first_name"] = u.GetString("first_name")
			actor["last_name"] = u.GetString("last_name")
			actor["avatar"] = u.GetString("avatar")
		}
		out = append(out, map[string]any{
			"id":            serializers.UUID(r),
			"vote":          r.GetInt("vote"),
			"actor":         serializers.RelationUUID(h.App, r, "actor"),
			"actor_details": actor,
		})
	}
	return out
}

// Comments serves GET/POST /api/public/anchor/{anchor}/issues/{issue_id}/comments/.
// GET is anonymous but only returns EXTERNAL comments and only when the board
// enables them; POST requires a session (IssueCommentPublicViewSet).
func (h *Handler) Comments(e *core.RequestEvent) error {
	board := h.boardByID(e.Request.PathValue("anchor"))
	if board == nil {
		return serializers.NotFound(e, "Project is not published")
	}
	issue := h.issueInBoard(board, e.Request.PathValue("issue_id"))
	if issue == nil {
		return serializers.NotFound(e, "Issue not found")
	}
	switch e.Request.Method {
	case http.MethodGet:
		if !board.GetBool("is_comments_enabled") {
			return e.JSON(http.StatusOK, []map[string]any{})
		}
		recs, err := h.App.FindRecordsByFilter(
			"issue_comments",
			"issue = {:issue} && access = 'EXTERNAL'",
			"created_at",
			0, 0, dbx.Params{"issue": issue.Id},
		)
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(recs))
		for _, c := range recs {
			out = append(out, serializers.Comment(h.App, c))
		}
		return e.JSON(http.StatusOK, out)
	case http.MethodPost:
		if !board.GetBool("is_comments_enabled") {
			return serializers.BadRequest(e, "Comments are not enabled for this project")
		}
		user := middleware.CurrentUser(e)
		if user == nil {
			return serializers.Unauthorized(e, "Authentication credentials were not provided.")
		}
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
		rec.Set("project", board.GetString("project"))
		rec.Set("workspace", board.GetString("workspace"))
		rec.Set("actor", user.Id)
		rec.Set("comment_html", html)
		rec.Set("comment_stripped", stripHTML(html))
		rec.Set("access", "EXTERNAL")
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, serializers.Comment(h.App, rec))
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// CommentDetail serves GET/PATCH/DELETE on a single public comment.
func (h *Handler) CommentDetail(e *core.RequestEvent) error {
	board := h.boardByID(e.Request.PathValue("anchor"))
	if board == nil {
		return serializers.NotFound(e, "Project is not published")
	}
	comment, _ := serializers.FindByUUIDOrID(h.App, "issue_comments", e.Request.PathValue("pk"))
	if comment == nil || comment.GetString("issue") != h.issueIdForBoard(board, e.Request.PathValue("issue_id")) {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, serializers.Comment(h.App, comment))
	case http.MethodPatch, http.MethodPut:
		user := middleware.CurrentUser(e)
		if user == nil {
			return serializers.Unauthorized(e, "Authentication credentials were not provided.")
		}
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		if html, ok := body["comment_html"].(string); ok {
			comment.Set("comment_html", html)
			comment.Set("comment_stripped", stripHTML(html))
			comment.Set("edited_at", serializers.Time(serializers.Now()))
		}
		if err := h.App.Save(comment); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, serializers.Comment(h.App, comment))
	case http.MethodDelete:
		user := middleware.CurrentUser(e)
		if user == nil {
			return serializers.Unauthorized(e, "Authentication credentials were not provided.")
		}
		if err := h.App.Delete(comment); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// issueIdForBoard resolves the internal issue id if it belongs to the board.
func (h *Handler) issueIdForBoard(board *core.Record, issueID string) string {
	issue := h.issueInBoard(board, issueID)
	if issue == nil {
		return "\x00none"
	}
	return issue.Id
}

// IssueReactions serves GET/POST on issue reactions; DELETE happens by
// reaction code (see IssueReactionDelete).
func (h *Handler) IssueReactions(e *core.RequestEvent) error {
	board := h.boardByID(e.Request.PathValue("anchor"))
	if board == nil {
		return serializers.NotFound(e, "Project is not published")
	}
	issue := h.issueInBoard(board, e.Request.PathValue("issue_id"))
	if issue == nil {
		return serializers.NotFound(e, "Issue not found")
	}
	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, h.reactionList("issue_reactions", "issue", issue.Id))
	case http.MethodPost:
		if !board.GetBool("is_reactions_enabled") {
			return serializers.BadRequest(e, "Reactions are not enabled for this project")
		}
		user := middleware.CurrentUser(e)
		if user == nil {
			return serializers.Unauthorized(e, "Authentication credentials were not provided.")
		}
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		reaction, _ := body["reaction"].(string)
		if reaction == "" {
			return serializers.Validation(e, "reaction", "This field is required.")
		}
		coll, err := h.App.FindCollectionByNameOrId("issue_reactions")
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("issue", issue.Id)
		rec.Set("project", board.GetString("project"))
		rec.Set("workspace", board.GetString("workspace"))
		rec.Set("actor", user.Id)
		rec.Set("reaction", reaction)
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, serializers.Reaction(h.App, rec))
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// IssueReactionDelete serves DELETE
// /api/public/anchor/{anchor}/issues/{issue_id}/reactions/{reaction_code}/ —
// removes the caller's own reaction matching the code.
func (h *Handler) IssueReactionDelete(e *core.RequestEvent) error {
	board := h.boardByID(e.Request.PathValue("anchor"))
	if board == nil {
		return serializers.NotFound(e, "Project is not published")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	issue := h.issueInBoard(board, e.Request.PathValue("issue_id"))
	if issue == nil {
		return serializers.NotFound(e, "Issue not found")
	}
	code := e.Request.PathValue("reaction_code")
	recs, _ := h.App.FindRecordsByFilter("issue_reactions",
		"issue = {:issue} && actor = {:actor} && reaction = {:reaction}",
		"-created_at", 0, 0,
		dbx.Params{"issue": issue.Id, "actor": user.Id, "reaction": code})
	for _, r := range recs {
		_ = h.App.Delete(r)
	}
	return e.NoContent(http.StatusNoContent)
}

// CommentReactions serves GET/POST on comment reactions.
func (h *Handler) CommentReactions(e *core.RequestEvent) error {
	board := h.boardByID(e.Request.PathValue("anchor"))
	if board == nil {
		return serializers.NotFound(e, "Project is not published")
	}
	comment, _ := serializers.FindByUUIDOrID(h.App, "issue_comments", e.Request.PathValue("comment_id"))
	if comment == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, h.reactionList("comment_reactions", "comment", comment.Id))
	case http.MethodPost:
		if !board.GetBool("is_reactions_enabled") {
			return serializers.BadRequest(e, "Reactions are not enabled for this project")
		}
		user := middleware.CurrentUser(e)
		if user == nil {
			return serializers.Unauthorized(e, "Authentication credentials were not provided.")
		}
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		reaction, _ := body["reaction"].(string)
		if reaction == "" {
			return serializers.Validation(e, "reaction", "This field is required.")
		}
		coll, err := h.App.FindCollectionByNameOrId("comment_reactions")
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("comment", comment.Id)
		rec.Set("actor", user.Id)
		rec.Set("reaction", reaction)
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, serializers.Reaction(h.App, rec))
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// CommentReactionDelete serves DELETE …/comments/{comment_id}/reactions/{reaction_code}/.
func (h *Handler) CommentReactionDelete(e *core.RequestEvent) error {
	board := h.boardByID(e.Request.PathValue("anchor"))
	if board == nil {
		return serializers.NotFound(e, "Project is not published")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	comment, _ := serializers.FindByUUIDOrID(h.App, "issue_comments", e.Request.PathValue("comment_id"))
	if comment == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	code := e.Request.PathValue("reaction_code")
	recs, _ := h.App.FindRecordsByFilter("comment_reactions",
		"comment = {:comment} && actor = {:actor} && reaction = {:reaction}",
		"-created_at", 0, 0,
		dbx.Params{"comment": comment.Id, "actor": user.Id, "reaction": code})
	for _, r := range recs {
		_ = h.App.Delete(r)
	}
	return e.NoContent(http.StatusNoContent)
}

// Votes serves GET/POST/DELETE on issue votes (IssueVotePublicViewSet):
// one vote per actor, get_or_create semantics on POST.
func (h *Handler) Votes(e *core.RequestEvent) error {
	board := h.boardByID(e.Request.PathValue("anchor"))
	if board == nil {
		return serializers.NotFound(e, "Project is not published")
	}
	issue := h.issueInBoard(board, e.Request.PathValue("issue_id"))
	if issue == nil {
		return serializers.NotFound(e, "Issue not found")
	}
	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, h.voteList(issue.Id))
	case http.MethodPost:
		if !board.GetBool("is_votes_enabled") {
			return serializers.BadRequest(e, "Votes are not enabled for this project board")
		}
		user := middleware.CurrentUser(e)
		if user == nil {
			return serializers.Unauthorized(e, "Authentication credentials were not provided.")
		}
		existing, err := h.App.FindFirstRecordByFilter("issue_votes",
			"issue = {:issue} && actor = {:actor}",
			dbx.Params{"issue": issue.Id, "actor": user.Id})
		body := map[string]any{}
		_ = e.BindBody(&body)
		vote := 1
		if v, ok := body["vote"].(float64); ok {
			vote = int(v)
		}
		if err == nil && existing != nil {
			existing.Set("vote", vote)
			_ = h.App.Save(existing)
			return e.JSON(http.StatusCreated, serializers.Vote(h.App, existing))
		}
		coll, err := h.App.FindCollectionByNameOrId("issue_votes")
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("issue", issue.Id)
		rec.Set("project", board.GetString("project"))
		rec.Set("workspace", board.GetString("workspace"))
		rec.Set("actor", user.Id)
		rec.Set("vote", vote)
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusCreated, serializers.Vote(h.App, rec))
	case http.MethodDelete:
		user := middleware.CurrentUser(e)
		if user == nil {
			return serializers.Unauthorized(e, "Authentication credentials were not provided.")
		}
		recs, _ := h.App.FindRecordsByFilter("issue_votes",
			"issue = {:issue} && actor = {:actor}", "-created_at", 0, 0,
			dbx.Params{"issue": issue.Id, "actor": user.Id})
		for _, r := range recs {
			_ = h.App.Delete(r)
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// IntakeIssues serves GET/POST on the public inbox
// (/api/public/anchor/{anchor}/intakes/{intake_id}/intake-issues/ and the
// inbox-issues alias). POST is anonymous: it is how site visitors submit
// tickets into a published project's inbox.
func (h *Handler) IntakeIssues(e *core.RequestEvent) error {
	board := h.boardByID(e.Request.PathValue("anchor"))
	if board == nil {
		return serializers.NotFound(e, "Project is not published")
	}
	intakeID := e.Request.PathValue("intake_id")
	intake, _ := serializers.FindByUUIDOrID(h.App, "intakes", intakeID)
	if intake == nil || intake.GetString("project") != board.GetString("project") {
		return serializers.BadRequest(e, "Intake does not belong to this Project Board")
	}
	boardIntake := serializers.RelationUUID(h.App, board, "intake")
	if boardIntake == nil || boardIntake.(string) == "" {
		return serializers.BadRequest(e, "Intake is not enabled for this Project Board")
	}

	switch e.Request.Method {
	case http.MethodGet:
		// List the intake's issues with their intake bridge data.
		recs, err := h.App.FindRecordsByFilter(
			"intake_issues",
			"intake = {:intake} && project = {:project}",
			"created_at",
			0, 0,
			dbx.Params{"intake": intake.Id, "project": board.GetString("project")},
		)
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(recs))
		for _, ii := range recs {
			out = append(out, h.intakeIssueOut(ii))
		}
		return e.JSON(http.StatusOK, out)
	case http.MethodPost:
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		issueBody, _ := body["issue"].(map[string]any)
		if issueBody == nil {
			issueBody = body
		}
		name, _ := issueBody["name"].(string)
		if strings.TrimSpace(name) == "" {
			return serializers.BadRequest(e, "Name is required")
		}
		priority, _ := issueBody["priority"].(string)
		switch priority {
		case "", "none", "low", "medium", "high", "urgent":
		default:
			return serializers.BadRequest(e, "Invalid priority")
		}
		created, err := h.createIntakeIssue(board, intake, issueBody)
		if err != nil {
			return err
		}
		return e.JSON(http.StatusCreated, created)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// createIntakeIssue creates the issue in the Triage state plus its
// intake_issues bridge row.
func (h *Handler) createIntakeIssue(board *core.Record, intake *core.Record, issueBody map[string]any) (map[string]any, error) {
	name, _ := issueBody["name"].(string)
	priority, _ := issueBody["priority"].(string)
	if priority == "" {
		priority = "none"
	}
	triage, err := h.App.FindFirstRecordByFilter("states",
		"project = {:project} && is_triage = 1",
		dbx.Params{"project": board.GetString("project")})
	if err != nil || triage == nil {
		return nil, errors.New("Triage state is missing for this project")
	}
	issuesColl, err := h.App.FindCollectionByNameOrId("issues")
	if err != nil {
		return nil, err
	}
	issue := core.NewRecord(issuesColl)
	issue.Set("uuid", serializers.NewUUID())
	issue.Set("name", name)
	issue.Set("project", board.GetString("project"))
	issue.Set("workspace", board.GetString("workspace"))
	issue.Set("state", triage.Id)
	issue.Set("priority", priority)
	if desc, ok := issueBody["description_html"].(string); ok {
		issue.Set("description_html", desc)
	}
	if err := h.App.Save(issue); err != nil {
		return nil, err
	}
	coll, err := h.App.FindCollectionByNameOrId("intake_issues")
	if err != nil {
		return nil, err
	}
	bridge := core.NewRecord(coll)
	bridge.Set("uuid", serializers.NewUUID())
	bridge.Set("intake", intake.Id)
	bridge.Set("issue", issue.Id)
	bridge.Set("project", board.GetString("project"))
	bridge.Set("workspace", board.GetString("workspace"))
	bridge.Set("status", 1) // PENDING
	bridge.Set("source", "INBOX")
	if err := h.App.Save(bridge); err != nil {
		return nil, err
	}
	return h.intakeIssueOut(bridge), nil
}

// intakeIssueOut serializes an intake bridge row with the nested issue.
func (h *Handler) intakeIssueOut(bridge *core.Record) map[string]any {
	out := map[string]any{
		"id":           serializers.UUID(bridge),
		"status":       bridge.GetInt("status"),
		"snoozed_till": serializers.NullableDateTime(bridge, "snoozed_till"),
		"source":       bridge.GetString("source"),
		"duplicate_to": serializers.RelationUUID(h.App, bridge, "duplicate_to"),
	}
	if issue, _ := serializers.FindByUUIDOrID(h.App, "issues", bridge.GetString("issue")); issue != nil {
		out["issue_detail"] = serializers.Issue(h.App, issue)
		out["issue"] = serializers.UUID(issue)
	} else {
		out["issue_detail"] = nil
		out["issue"] = nil
	}
	return out
}

// IntakeIssueDetail serves GET/PATCH/DELETE on a single public intake issue.
func (h *Handler) IntakeIssueDetail(e *core.RequestEvent) error {
	board := h.boardByID(e.Request.PathValue("anchor"))
	if board == nil {
		return serializers.NotFound(e, "Project is not published")
	}
	bridge, _ := serializers.FindByUUIDOrID(h.App, "intake_issues", e.Request.PathValue("pk"))
	if bridge == nil || bridge.GetString("project") != board.GetString("project") {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, h.intakeIssueOut(bridge))
	case http.MethodPatch:
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
		return e.JSON(http.StatusOK, h.intakeIssueOut(bridge))
	case http.MethodDelete:
		if err := h.App.Delete(bridge); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// stripHTML removes tags for comment_stripped (mirrors the app-side helper).
func stripHTML(html string) string {
	out := strings.Builder{}
	depth := 0
	for _, r := range html {
		switch {
		case r == '<':
			depth++
		case r == '>':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			out.WriteRune(r)
		}
	}
	return out.String()
}
