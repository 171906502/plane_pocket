// Package search implements Plane's search and analytics endpoints:
//
//	GET  /api/workspaces/{slug}/search/                                    (GlobalSearchEndpoint)
//	GET  /api/workspaces/{slug}/projects/{project_id}/search-issues/       (IssueSearchEndpoint)
//	GET  /api/workspaces/{slug}/entity-search/                             (SearchEndpoint)
//
// and the analytics domain (analytic.go).
//
// Every search is workspace-scoped: the `/api/workspaces/{slug}/` group runs
// ResolveWorkspace, which returns 404 (never 403) when the caller has no active
// membership. On top of that, entity filters are restricted to the caller's
// active project memberships, so a workspace member cannot see rows from a
// project they are not in.
package search

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Handler holds the app dependency for the search/analytics routes.
type Handler struct {
	App core.App
}

// New returns a search handler bound to app.
func New(app core.App) *Handler {
	return &Handler{App: app}
}

// GlobalSearch serves GET /api/workspaces/{slug}/search/.
func (h *Handler) GlobalSearch(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	query := e.Request.URL.Query()
	search := query.Get("search")
	workspaceSearch := query.Get("workspace_search")
	projectID := query.Get("project_id")

	requested := []string{"workspace", "project", "issue", "cycle", "module", "issue_view", "page", "intake"}
	if raw := query.Get("entities"); raw != "" {
		requested = requested[:0]
		for _, name := range strings.Split(raw, ",") {
			name = strings.TrimSpace(name)
			if isSearchEntity(name) {
				requested = append(requested, name)
			}
		}
	}

	// The caller's active projects in this workspace bound every project-scoped
	// entity search.
	projectIDs := h.memberProjectIDs(user.Id, scope.Workspace.Id)
	// When scoping to a single project, validate it is one of the caller's.
	if workspaceSearch == "false" && projectID != "" {
		if pid := h.projectInternalID(projectID); pid != "" && contains(projectIDs, pid) {
			projectIDs = []string{pid}
		} else {
			projectIDs = nil
		}
	}

	results := map[string]any{}
	for _, entity := range requested {
		switch entity {
		case "workspace":
			results["workspace"] = h.searchWorkspaces(user.Id, search)
		case "project":
			results["project"] = h.searchProjects(scope.Workspace.Id, user.Id, search)
		case "issue":
			results["issue"] = h.searchIssues(scope.Workspace.Id, projectIDs, search)
		case "cycle":
			results["cycle"] = h.searchNamed(scope.Workspace.Id, "cycles", projectIDs, search)
		case "module":
			results["module"] = h.searchNamed(scope.Workspace.Id, "modules", projectIDs, search)
		case "issue_view":
			results["issue_view"] = h.searchNamed(scope.Workspace.Id, "issue_views", projectIDs, search)
		case "page":
			results["page"] = h.searchPages(scope.Workspace.Id, projectIDs, search)
		case "intake":
			results["intake"] = h.searchIntakes(scope.Workspace.Id, projectIDs, search)
		}
	}
	return e.JSON(200, map[string]any{"results": results})
}

// IssueSearch serves GET /api/workspaces/{slug}/projects/{project_id}/search-issues/.
func (h *Handler) IssueSearch(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	query := e.Request.URL.Query()
	search := query.Get("search")
	workspaceSearch := query.Get("workspace_search")

	projectIDs := h.memberProjectIDs(user.Id, scope.Workspace.Id)
	if workspaceSearch != "true" {
		projectIDs = []string{scope.Project.Id}
	}

	filter := "workspace = {:ws} && is_draft = false && archived_at = '' && deleted_at = ''"
	params := dbx.Params{"ws": scope.Workspace.Id}
	scopeFilter, scopeParams := projectInFilter("project", projectIDs)
	if scopeFilter == "" {
		return e.JSON(200, []any{})
	}
	filter += " && " + scopeFilter
	for k, v := range scopeParams {
		params[k] = v
	}
	if q := strings.TrimSpace(search); q != "" {
		filter += " && (name ~ {:q} || project.identifier ~ {:q} || sequence_id = {:seq})"
		params["q"] = q
		params["seq"] = firstInt(q)
	}
	if query.Get("target_date") == "none" {
		filter += " && target_date = ''"
	}
	// A guest without guest_view_all_features sees only their own issues.
	if scope.ProjectRole == middleware.RoleGuest && !scope.Project.GetBool("guest_view_all_features") {
		filter += " && created_by = {:user}"
		params["user"] = user.Id
	}

	issueID := query.Get("issue_id")
	if issueID != "" {
		excluded := h.issueExclusionIDs(query, issueID)
		if len(excluded) > 0 {
			clause, exParams := notInFilter("id", excluded)
			if clause != "" {
				filter += " && " + clause
				for k, v := range exParams {
					params[k] = v
				}
			}
		}
	}
	if query.Get("cycle") == "true" {
		cycleIssueIDs := h.allRelationIDs("cycle_issues", "issue")
		if clause, cParams := notInFilter("id", cycleIssueIDs); clause != "" {
			filter += " && " + clause
			for k, v := range cParams {
				params[k] = v
			}
		}
	}
	if moduleID := query.Get("module"); moduleID != "" {
		moduleIssueIDs := h.moduleIssueIDs(moduleID)
		if clause, mParams := notInFilter("id", moduleIssueIDs); clause != "" {
			filter += " && " + clause
			for k, v := range mParams {
				params[k] = v
			}
		}
	}

	records, err := h.App.FindRecordsByFilter("issues", filter, "-created_at", 100, 0, params)
	if err != nil {
		return err
	}
	out := make([]any, 0, len(records))
	for _, r := range records {
		out = append(out, h.issueSearchRow(r))
	}
	return e.JSON(200, out)
}

// EntitySearch serves GET /api/workspaces/{slug}/entity-search/ (SearchEndpoint).
func (h *Handler) EntitySearch(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	user := middleware.CurrentUser(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	query := e.Request.URL.Query()
	search := query.Get("query")
	count := 5
	if raw := query.Get("count"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			count = parsed
		}
	}
	projectID := query.Get("project_id")
	types := []string{"user_mention"}
	if raw := query.Get("query_type"); raw != "" {
		types = nil
		for _, t := range strings.Split(raw, ",") {
			if t = strings.TrimSpace(t); t != "" {
				types = append(types, t)
			}
		}
	}

	projectIDs := h.memberProjectIDs(user.Id, scope.Workspace.Id)
	if projectID != "" {
		if pid := h.projectInternalID(projectID); pid != "" && contains(projectIDs, pid) {
			projectIDs = []string{pid}
		} else {
			projectIDs = nil
		}
	}

	response := map[string]any{}
	for _, t := range types {
		switch t {
		case "user_mention":
			response["user_mention"] = h.searchUserMentions(scope, user, projectID, search, count)
		case "project":
			response["project"] = h.searchProjectLite(scope.Workspace.Id, user.Id, search, count)
		case "issue":
			response["issue"] = h.searchIssueLite(scope.Workspace.Id, projectIDs, search, count)
		case "cycle":
			response["cycle"] = h.searchNamed(scope.Workspace.Id, "cycles", projectIDs, search)
		case "module":
			response["module"] = h.searchNamed(scope.Workspace.Id, "modules", projectIDs, search)
		case "page":
			response["page"] = h.searchPageLite(scope.Workspace.Id, projectIDs, search, count)
		}
	}
	return e.JSON(200, response)
}

// ---------------------------------------------------------------------------
// entity search implementations
// ---------------------------------------------------------------------------

// searchWorkspaces matches workspaces the caller is an active member of.
func (h *Handler) searchWorkspaces(userID, search string) []any {
	members, err := h.App.FindRecordsByFilter("workspace_members", "member = {:user} && is_active = true", "", 0, 0, dbx.Params{"user": userID})
	if err != nil {
		return []any{}
	}
	out := []any{}
	for _, member := range members {
		ws, err := h.App.FindRecordById("workspaces", member.GetString("workspace"))
		if err != nil || ws == nil || ws.GetString("deleted_at") != "" {
			continue
		}
		if q := strings.TrimSpace(search); q != "" && !containsFold(ws.GetString("name"), q) {
			continue
		}
		out = append(out, map[string]any{"name": ws.GetString("name"), "id": serializers.UUID(ws), "slug": ws.GetString("slug")})
	}
	return out
}

// searchProjects matches the caller's active projects in the workspace.
func (h *Handler) searchProjects(wsID, userID, search string) []any {
	projectIDs := h.memberProjectIDs(userID, wsID)
	out := []any{}
	for _, pid := range projectIDs {
		project, _ := h.App.FindRecordById("projects", pid)
		if project == nil || project.GetString("archived_at") != "" {
			continue
		}
		if q := strings.TrimSpace(search); q != "" &&
			!containsFold(project.GetString("name"), q) && !containsFold(project.GetString("identifier"), q) {
			continue
		}
		out = append(out, h.projectRow(project))
	}
	return out
}

// searchIssues returns the `filter_issues` projection (limit 100).
func (h *Handler) searchIssues(wsID string, projectIDs []string, search string) []any {
	out := []any{}
	for _, r := range h.findScopedIssues(wsID, projectIDs, search, 100) {
		project, _ := h.App.FindRecordById("projects", r.GetString("project"))
		out = append(out, map[string]any{
			"name":                r.GetString("name"),
			"id":                  serializers.UUID(r),
			"sequence_id":         r.GetInt("sequence_id"),
			"project__identifier": projectIdentifier(project),
			"project_id":          serializers.UUID(project),
			"workspace__slug":     h.workspaceSlug(wsID),
		})
	}
	return out
}

// searchNamed handles the cycle/module/issue_view projections, which share the
// {name, id, project_id, project__identifier, workspace__slug} shape.
func (h *Handler) searchNamed(wsID, collection string, projectIDs []string, search string) []any {
	out := []any{}
	if len(projectIDs) == 0 {
		return out
	}
	filter := "workspace = {:ws} && deleted_at = ''"
	params := dbx.Params{"ws": wsID}
	if clause, p := projectInFilter("project", projectIDs); clause != "" {
		filter += " && " + clause
		for k, v := range p {
			params[k] = v
		}
	}
	if q := strings.TrimSpace(search); q != "" {
		filter += " && name ~ {:q}"
		params["q"] = q
	}
	records, err := h.App.FindRecordsByFilter(collection, filter, "-created_at", 0, 0, params)
	if err != nil {
		return out
	}
	for _, r := range records {
		project, _ := h.App.FindRecordById("projects", r.GetString("project"))
		if project == nil || project.GetString("archived_at") != "" {
			continue
		}
		out = append(out, map[string]any{
			"name":                r.GetString("name"),
			"id":                  serializers.UUID(r),
			"project_id":          serializers.UUID(project),
			"project__identifier": projectIdentifier(project),
			"workspace__slug":     h.workspaceSlug(wsID),
		})
	}
	return out
}

// searchPages returns pages linked to the caller's projects.
func (h *Handler) searchPages(wsID string, projectIDs []string, search string) []any {
	out := []any{}
	seen := map[string]bool{}
	for _, pid := range projectIDs {
		links, err := h.App.FindRecordsByFilter("project_pages", "project = {:p} && deleted_at = ''", "", 0, 0, dbx.Params{"p": pid})
		if err != nil {
			continue
		}
		for _, link := range links {
			page, _ := h.App.FindRecordById("pages", link.GetString("page"))
			if page == nil || page.GetString("workspace") != wsID || page.GetString("deleted_at") != "" {
				continue
			}
			if q := strings.TrimSpace(search); q != "" && !containsFold(page.GetString("name"), q) {
				continue
			}
			if seen[page.Id] {
				continue
			}
			seen[page.Id] = true
			project, _ := h.App.FindRecordById("projects", pid)
			out = append(out, map[string]any{
				"name":                page.GetString("name"),
				"id":                  serializers.UUID(page),
				"project_ids":         []string{serializers.UUID(project)},
				"project_identifiers": []string{projectIdentifier(project)},
				"workspace__slug":     h.workspaceSlug(wsID),
			})
		}
	}
	return out
}

// searchIntakes returns issues with a pending/rejected intake (status 0 or -2).
func (h *Handler) searchIntakes(wsID string, projectIDs []string, search string) []any {
	out := []any{}
	intakeIssueIDs := map[string]bool{}
	rows, err := h.App.FindRecordsByFilter("intake_issues", "status = 0 || status = -2", "", 0, 0, nil)
	if err == nil {
		for _, row := range rows {
			intakeIssueIDs[row.GetString("issue")] = true
		}
	}
	for _, r := range h.findScopedIssues(wsID, projectIDs, search, 100) {
		if !intakeIssueIDs[r.Id] {
			continue
		}
		project, _ := h.App.FindRecordById("projects", r.GetString("project"))
		out = append(out, map[string]any{
			"name":                r.GetString("name"),
			"id":                  serializers.UUID(r),
			"sequence_id":         r.GetInt("sequence_id"),
			"project__identifier": projectIdentifier(project),
			"project_id":          serializers.UUID(project),
			"workspace__slug":     h.workspaceSlug(wsID),
		})
	}
	return out
}

// searchUserMentions resolves workspace or project members by name.
func (h *Handler) searchUserMentions(scope *middleware.Scope, user *core.Record, projectID, search string, count int) []any {
	out := []any{}
	seen := map[string]bool{}
	appendUser := func(member *core.Record) {
		if member == nil || seen[member.Id] || member.GetBool("is_bot") {
			return
		}
		if q := strings.TrimSpace(search); q != "" &&
			!containsFold(member.GetString("first_name"), q) &&
			!containsFold(member.GetString("last_name"), q) &&
			!containsFold(member.GetString("display_name"), q) {
			return
		}
		seen[member.Id] = true
		out = append(out, map[string]any{
			"member__avatar_url":   h.avatarURL(member),
			"member__display_name": member.GetString("display_name"),
			"member__id":           serializers.UUID(member),
		})
	}
	if projectID != "" {
		pid := h.projectInternalID(projectID)
		if pid == "" {
			return out
		}
		rows, _ := h.App.FindRecordsByFilter("project_members", "project = {:p} && is_active = true", "", 0, 0, dbx.Params{"p": pid})
		for _, row := range rows {
			appendUser(h.userRecord(row.GetString("member")))
		}
	} else {
		rows, _ := h.App.FindRecordsByFilter("workspace_members", "workspace = {:ws} && is_active = true", "", 0, 0, dbx.Params{"ws": scope.Workspace.Id})
		for _, row := range rows {
			appendUser(h.userRecord(row.GetString("member")))
		}
	}
	if len(out) > count {
		out = out[:count]
	}
	return out
}

// searchProjectLite returns the `project` query_type projection.
func (h *Handler) searchProjectLite(wsID, userID, search string, count int) []any {
	out := []any{}
	for _, pid := range h.memberProjectIDs(userID, wsID) {
		project, _ := h.App.FindRecordById("projects", pid)
		if project == nil {
			continue
		}
		if q := strings.TrimSpace(search); q != "" &&
			!containsFold(project.GetString("name"), q) && !containsFold(project.GetString("identifier"), q) {
			continue
		}
		out = append(out, map[string]any{
			"name":            project.GetString("name"),
			"id":              serializers.UUID(project),
			"identifier":      project.GetString("identifier"),
			"logo_props":      serializers.JSONValue(project, "logo_props"),
			"workspace__slug": h.workspaceSlug(wsID),
		})
		if len(out) >= count {
			break
		}
	}
	return out
}

// searchIssueLite returns the entity-search `issue` projection.
func (h *Handler) searchIssueLite(wsID string, projectIDs []string, search string, count int) []any {
	out := []any{}
	for _, r := range h.findScopedIssues(wsID, projectIDs, search, count) {
		project, _ := h.App.FindRecordById("projects", r.GetString("project"))
		out = append(out, map[string]any{
			"name":                r.GetString("name"),
			"id":                  serializers.UUID(r),
			"sequence_id":         r.GetInt("sequence_id"),
			"project__identifier": projectIdentifier(project),
			"project_id":          serializers.UUID(project),
			"priority":            r.GetString("priority"),
			"state_id":            serializers.RelationUUID(h.App, r, "state"),
			"type_id":             serializers.RelationUUID(h.App, r, "type"),
		})
	}
	return out
}

// searchPageLite returns the entity-search `page` projection.
func (h *Handler) searchPageLite(wsID string, projectIDs []string, search string, count int) []any {
	out := []any{}
	seen := map[string]bool{}
	for _, pid := range projectIDs {
		links, _ := h.App.FindRecordsByFilter("project_pages", "project = {:p} && deleted_at = ''", "", 0, 0, dbx.Params{"p": pid})
		for _, link := range links {
			page, _ := h.App.FindRecordById("pages", link.GetString("page"))
			if page == nil || page.GetString("workspace") != wsID || page.GetString("access") != "0" || seen[page.Id] {
				continue
			}
			if q := strings.TrimSpace(search); q != "" && !containsFold(page.GetString("name"), q) {
				continue
			}
			seen[page.Id] = true
			project, _ := h.App.FindRecordById("projects", pid)
			out = append(out, map[string]any{
				"name":            page.GetString("name"),
				"id":              serializers.UUID(page),
				"logo_props":      serializers.JSONValue(page, "logo_props"),
				"projects__id":    serializers.UUID(project),
				"workspace__slug": h.workspaceSlug(wsID),
			})
			if len(out) >= count {
				return out
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

// findScopedIssues returns non-draft, non-archived issues in the workspace that
// belong to one of projectIDs and match search.
func (h *Handler) findScopedIssues(wsID string, projectIDs []string, search string, limit int) []*core.Record {
	if len(projectIDs) == 0 {
		return nil
	}
	filter := "workspace = {:ws} && is_draft = false && archived_at = '' && deleted_at = ''"
	params := dbx.Params{"ws": wsID}
	clause, p := projectInFilter("project", projectIDs)
	if clause == "" {
		return nil
	}
	filter += " && " + clause
	for k, v := range p {
		params[k] = v
	}
	if q := strings.TrimSpace(search); q != "" {
		filter += " && (name ~ {:q} || sequence_id = {:seq})"
		params["q"] = q
		params["seq"] = firstInt(q)
	}
	records, err := h.App.FindRecordsByFilter("issues", filter, "-created_at", limit, 0, params)
	if err != nil {
		return nil
	}
	return records
}

// issueSearchRow renders the `search-issues` projection for one issue.
func (h *Handler) issueSearchRow(r *core.Record) map[string]any {
	project, _ := h.App.FindRecordById("projects", r.GetString("project"))
	state, _ := h.App.FindRecordById("states", r.GetString("state"))
	var stateName, stateGroup, stateColor any
	if state != nil {
		stateName = state.GetString("name")
		stateGroup = state.GetString("group")
		stateColor = state.GetString("color")
	}
	return map[string]any{
		"name":                r.GetString("name"),
		"id":                  serializers.UUID(r),
		"start_date":          serializers.DateOnly(r, "start_date"),
		"sequence_id":         r.GetInt("sequence_id"),
		"project__name":       projectName(project),
		"project__identifier": projectIdentifier(project),
		"project_id":          serializers.UUID(project),
		"workspace__slug":     projectWorkspaceSlug(h, project),
		"state__name":         stateName,
		"state__group":        stateGroup,
		"state__color":        stateColor,
	}
}

// projectRow is the global-search `project` projection.
func (h *Handler) projectRow(project *core.Record) map[string]any {
	return map[string]any{
		"name":            project.GetString("name"),
		"id":              serializers.UUID(project),
		"identifier":      project.GetString("identifier"),
		"workspace__slug": h.workspaceSlug(project.GetString("workspace")),
	}
}

// issueExclusionIDs computes the id set to exclude for the parent/sub_issue/
// issue_relation query flags.
func (h *Handler) issueExclusionIDs(query map[string][]string, issueID string) []string {
	issue, _ := serializers.FindByUUID(h.App, "issues", issueID)
	if issue == nil {
		return nil
	}
	out := []string{issue.Id}
	if get(query, "parent") == "true" || get(query, "sub_issue") == "true" {
		if parent := issue.GetString("parent"); parent != "" {
			out = append(out, parent)
		}
		children, _ := h.App.FindRecordsByFilter("issues", "parent = {:p}", "", 0, 0, dbx.Params{"p": issue.Id})
		for _, child := range children {
			out = append(out, child.Id)
		}
	}
	if get(query, "issue_relation") == "true" {
		rows, _ := h.App.FindRecordsByFilter("issue_relations", "issue = {:id} || related_issue = {:id}", "", 0, 0, dbx.Params{"id": issue.Id})
		for _, row := range rows {
			out = append(out, row.GetString("issue"), row.GetString("related_issue"))
		}
	}
	return out
}

// relationIDs collects the `column` values of rows in `collection` whose
// `matchField` equals matchValue.
func (h *Handler) relationIDs(collection, column, matchField, matchValue string, extra dbx.Params) []string {
	if matchValue == "" {
		return nil
	}
	params := dbx.Params{matchField: matchValue}
	for k, v := range extra {
		params[k] = v
	}
	rows, err := h.App.FindRecordsByFilter(collection, matchField+" = {:"+matchField+"}", "", 0, 0, params)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if v := row.GetString(column); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// allRelationIDs collects every non-empty `column` value in `collection` whose
// row is not soft-deleted — used to exclude all cycle-linked issues.
func (h *Handler) allRelationIDs(collection, column string) []string {
	rows, err := h.App.FindRecordsByFilter(collection, "deleted_at = ''", "", 0, 0, nil)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if v := row.GetString(column); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// moduleIssueIDs returns the issue ids linked to a module (Plane UUID or id).
func (h *Handler) moduleIssueIDs(moduleID string) []string {
	module, _ := serializers.FindByUUIDOrID(h.App, "modules", moduleID)
	if module == nil {
		return nil
	}
	return h.relationIDs("module_issues", "issue", "module", module.Id, nil)
}

// memberProjectIDs returns the ids of the workspace's projects the user is an
// active member of.
func (h *Handler) memberProjectIDs(userID, wsID string) []string {
	rows, err := h.App.FindRecordsByFilter(
		"project_members",
		"workspace = {:ws} && member = {:user} && is_active = true",
		"", 0, 0,
		dbx.Params{"ws": wsID, "user": userID},
	)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if pid := row.GetString("project"); pid != "" {
			out = append(out, pid)
		}
	}
	return out
}

// projectInternalID resolves a Plane project UUID (or PocketBase id) to its
// internal id.
func (h *Handler) projectInternalID(idOrUUID string) string {
	project, _ := serializers.FindByUUIDOrID(h.App, "projects", idOrUUID)
	if project == nil {
		return ""
	}
	return project.Id
}

// userRecord loads a user by internal id.
func (h *Handler) userRecord(id string) *core.Record {
	user, _ := h.App.FindRecordById("users", id)
	return user
}

// workspaceSlug resolves an internal workspace id to its slug.
func (h *Handler) workspaceSlug(id string) string {
	ws, _ := h.App.FindRecordById("workspaces", id)
	if ws == nil {
		return ""
	}
	return ws.GetString("slug")
}

// avatarURL mirrors User.avatar_url: the static asset URL when an avatar asset
// is set, otherwise the raw avatar string.
func (h *Handler) avatarURL(user *core.Record) any {
	if assetID := user.GetString("avatar_asset"); assetID != "" {
		return "/api/assets/v2/static/" + serializers.UUID(mustRecord(h.App, "file_assets", assetID)) + "/"
	}
	if avatar := user.GetString("avatar"); avatar != "" {
		return avatar
	}
	return nil
}

// projectInFilter builds a `(field = {:p0} || field = {:p1})` clause.
func projectInFilter(field string, ids []string) (string, dbx.Params) {
	if len(ids) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(ids))
	params := dbx.Params{}
	for i, id := range ids {
		key := field + "_" + strconv.Itoa(i)
		parts = append(parts, field+" = {:"+key+"}")
		params[key] = id
	}
	return "(" + strings.Join(parts, " || ") + ")", params
}

// notInFilter builds a `(field != {:n0} && field != {:n1})` clause.
func notInFilter(field string, ids []string) (string, dbx.Params) {
	if len(ids) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(ids))
	params := dbx.Params{}
	for i, id := range ids {
		if id == "" {
			continue
		}
		key := field + "_n" + strconv.Itoa(i)
		parts = append(parts, field+" != {:"+key+"}")
		params[key] = id
	}
	if len(parts) == 0 {
		return "", nil
	}
	return "(" + strings.Join(parts, " && ") + ")", params
}

// projectIdentifier reads a project's identifier, tolerating a nil record.
func projectIdentifier(project *core.Record) string {
	if project == nil {
		return ""
	}
	return project.GetString("identifier")
}

// projectName reads a project's name, tolerating a nil record.
func projectName(project *core.Record) any {
	if project == nil {
		return nil
	}
	return project.GetString("name")
}

// projectWorkspaceSlug resolves the slug for a project record.
func projectWorkspaceSlug(h *Handler, project *core.Record) any {
	if project == nil {
		return nil
	}
	return h.workspaceSlug(project.GetString("workspace"))
}

// mustRecord loads a record by internal id (nil-safe).
func mustRecord(app core.App, collection, id string) *core.Record {
	record, _ := app.FindRecordById(collection, id)
	return record
}

// isSearchEntity reports whether name is one of GlobalSearchEndpoint's models.
func isSearchEntity(name string) bool {
	switch name {
	case "workspace", "project", "issue", "cycle", "module", "issue_view", "page", "intake":
		return true
	}
	return false
}

// contains reports whether ids contains value.
func contains(ids []string, value string) bool {
	for _, id := range ids {
		if id == value {
			return true
		}
	}
	return false
}

// containsFold is a case-insensitive substring check.
func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// firstInt extracts the first whole integer from a query string (Plane matches
// `sequence_id` on whole numbers only).
func firstInt(s string) int {
	start := -1
	for i, r := range s {
		if r >= '0' && r <= '9' {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			if n, err := strconv.Atoi(s[start:i]); err == nil {
				return n
			}
			start = -1
		}
	}
	if start >= 0 {
		if n, err := strconv.Atoi(s[start:]); err == nil {
			return n
		}
	}
	return -1
}

// get reads a query value from a url.Values-like map.
func get(query map[string][]string, key string) string {
	if values, ok := query[key]; ok && len(values) > 0 {
		return values[0]
	}
	return ""
}

// bindBody decodes a JSON body, tolerating an empty body.
func bindBody(e *core.RequestEvent) (map[string]any, error) {
	body := map[string]any{}
	if e.Request.ContentLength == 0 {
		return body, nil
	}
	if err := e.BindBody(&body); err != nil {
		return body, nil
	}
	return body, nil
}

// asString reads a string field.
func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// jsonMap normalises a PocketBase JSON column (decoded map or types.JSONRaw).
func jsonMap(v any) map[string]any {
	switch value := v.(type) {
	case map[string]any:
		return value
	case types.JSONRaw:
		out := map[string]any{}
		if err := json.Unmarshal(value, &out); err != nil {
			return nil
		}
		return out
	case []byte:
		out := map[string]any{}
		if err := json.Unmarshal(value, &out); err != nil {
			return nil
		}
		return out
	case string:
		out := map[string]any{}
		if err := json.Unmarshal([]byte(value), &out); err != nil {
			return nil
		}
		return out
	}
	return nil
}
