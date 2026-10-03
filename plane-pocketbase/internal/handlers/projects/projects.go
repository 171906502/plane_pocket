// Package projects implements the project-scoped Plane endpoints:
// /api/workspaces/{slug}/projects/… including states and labels.
package projects

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// defaultState mirrors one entry of plane/db/models/state.py DEFAULT_STATES.
type defaultState struct {
	Name     string
	Color    string
	Sequence float64
	Group    string
	Default  bool
}

// defaultStates is Plane's project seed, copied verbatim from DEFAULT_STATES.
// The brief mentions five states, but the Django source seeds six — Triage is
// included — and ProjectViewSet.create bulk-creates all six.
var defaultStates = []defaultState{
	{Name: "Backlog", Color: "#60646C", Sequence: 15000, Group: "backlog", Default: true},
	{Name: "Todo", Color: "#60646C", Sequence: 25000, Group: "unstarted"},
	{Name: "In Progress", Color: "#F59E0B", Sequence: 35000, Group: "started"},
	{Name: "Done", Color: "#46A758", Sequence: 45000, Group: "completed"},
	{Name: "Cancelled", Color: "#9AA4BC", Sequence: 55000, Group: "cancelled"},
	{Name: "Triage", Color: "#4E5355", Sequence: 65000, Group: "triage"},
}

// Handler holds the app dependency for the project routes.
type Handler struct {
	App core.App
}

// New returns a project handler bound to app.
func New(app core.App) *Handler {
	return &Handler{App: app}
}

// List serves GET /api/workspaces/{slug}/projects/.
//
// ProjectViewSet.list returns a PLAIN ARRAY (a `.values(...)` projection), not
// the cursor-paginated envelope used elsewhere in Plane — the frontend's
// `fetchPartialProjects` calls `.forEach()` on the response directly. Returning
// an envelope here leaves the projects store permanently empty and the projects
// page stuck on its skeleton loaders.
func (h *Handler) List(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)

	projects, err := h.visibleProjects(e, scope, user)
	if err != nil {
		return err
	}

	results := make([]any, 0, len(projects))
	for _, project := range projects {
		results = append(results, serializers.PartialProject(h.App, project, user))
	}
	return e.JSON(http.StatusOK, results)
}

// Details serves GET /api/workspaces/{slug}/projects/details/.
//
// This is the endpoint `projectService.getProjects` (`/projects/details/`) reads;
// `ProjectViewSet.list_detail` returns a PLAIN ARRAY of full project objects
// ordered by (sort_order, name). Without it the request falls through to the
// `{project_id}` route, is looked up as a project whose UUID is literally
// "details", and 404s.
func (h *Handler) Details(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)

	projects, err := h.visibleProjects(e, scope, user)
	if err != nil {
		return err
	}
	sort.SliceStable(projects, func(i, j int) bool {
		return projects[i].GetString("name") < projects[j].GetString("name")
	})

	results := make([]any, 0, len(projects))
	for _, project := range projects {
		results = append(results, serializers.Project(h.App, project, user))
	}
	return e.JSON(http.StatusOK, results)
}

// visibleProjects returns the non-deleted projects of the workspace that the
// caller is allowed to see. Visibility mirrors ProjectViewSet: guests see only
// the projects they are members of, members additionally see public
// (network=2) projects, admins see everything.
func (h *Handler) visibleProjects(e *core.RequestEvent, scope *middleware.Scope, user *core.Record) ([]*core.Record, error) {
	filter := "workspace = {:workspace} && deleted_at = ''"
	params := dbx.Params{"workspace": scope.Workspace.Id}

	if scope.WorkspaceRole < middleware.RoleAdmin && user != nil {
		ids := h.projectIDsForMember(user.Id, scope.Workspace.Id)
		if len(ids) == 0 {
			filter += " && network = 2"
		} else {
			ors := make([]string, 0, len(ids))
			for i, id := range ids {
				key := "p" + strconv.Itoa(i)
				ors = append(ors, "id = {:"+key+"}")
				params[key] = id
			}
			filter += " && (network = 2 || " + strings.Join(ors, " || ") + ")"
		}
	}

	return h.App.FindRecordsByFilter("projects", filter, "name", 0, 0, params)
}

// Create serves POST /api/workspaces/{slug}/projects/. It creates the project,
// the creator's `project_members` row (role 20) and Plane's default state set
// (ProjectViewSet.create).
func (h *Handler) Create(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	name := strings.TrimSpace(asString(body["name"]))
	identifier := strings.TrimSpace(asString(body["identifier"]))
	if name == "" {
		return serializers.Validation(e, "name", "This field is required.")
	}
	if identifier == "" {
		return serializers.Validation(e, "identifier", "This field is required.")
	}

	collection, err := h.App.FindCollectionByNameOrId("projects")
	if err != nil {
		return err
	}
	project := core.NewRecord(collection)
	project.Set("uuid", serializers.NewUUID())
	project.Set("workspace", scope.Workspace.Id)
	project.Set("name", name)
	project.Set("identifier", identifier)
	project.Set("network", intValue(body["network"], 2))
	project.Set("created_by", user.Id)
	// Plane's model defaults: page_view=True, everything else False.
	project.Set("page_view", boolValue(body["page_view"], true))
	for _, flag := range []string{"module_view", "cycle_view", "issue_views_view", "intake_view", "is_time_tracking_enabled", "is_issue_type_enabled", "guest_view_all_features"} {
		if _, ok := body[flag]; ok {
			project.Set(flag, boolValue(body[flag], false))
		}
	}
	for _, field := range []string{"description", "description_text", "description_html", "emoji", "icon_prop", "cover_image", "logo_props", "timezone", "external_source", "external_id"} {
		if v, ok := body[field]; ok {
			project.Set(field, v)
		}
	}
	for _, field := range []string{"archive_in", "close_in"} {
		if _, ok := body[field]; ok {
			project.Set(field, intValue(body[field], 0))
		}
	}
	for _, rel := range []string{"default_assignee", "project_lead", "default_state"} {
		if v := asString(body[rel]); v != "" {
			if record, _ := serializers.FindByUUID(h.App, relationTarget(rel), v); record != nil {
				project.Set(rel, record.Id)
			}
		}
	}
	if err := h.App.Save(project); err != nil {
		return serializers.Validation(e, "name", err.Error())
	}

	// The creator becomes the project administrator.
	membersCollection, err := h.App.FindCollectionByNameOrId("project_members")
	if err != nil {
		return err
	}
	membership := core.NewRecord(membersCollection)
	membership.Set("uuid", serializers.NewUUID())
	membership.Set("project", project.Id)
	membership.Set("workspace", scope.Workspace.Id)
	membership.Set("member", user.Id)
	membership.Set("role", middleware.RoleAdmin)
	membership.Set("is_active", true)
	membership.Set("created_by", user.Id)
	if err := h.App.Save(membership); err != nil {
		return err
	}

	// The project lead, when it is someone other than the creator, is also an admin.
	if lead := asString(body["project_lead"]); lead != "" && lead != serializers.UUID(user) {
		if leadRecord, _ := serializers.FindByUUID(h.App, "users", lead); leadRecord != nil && leadRecord.Id != user.Id {
			leadMembership := core.NewRecord(membersCollection)
			leadMembership.Set("uuid", serializers.NewUUID())
			leadMembership.Set("project", project.Id)
			leadMembership.Set("workspace", scope.Workspace.Id)
			leadMembership.Set("member", leadRecord.Id)
			leadMembership.Set("role", middleware.RoleAdmin)
			leadMembership.Set("is_active", true)
			leadMembership.Set("created_by", user.Id)
			_ = h.App.Save(leadMembership)
		}
	}

	if err := h.seedDefaultStates(project, scope.Workspace.Id, user.Id); err != nil {
		return err
	}

	return e.JSON(http.StatusCreated, serializers.Project(h.App, project, user))
}

// Detail serves GET /api/workspaces/{slug}/projects/{project_id}/.
func (h *Handler) Detail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	return e.JSON(http.StatusOK, serializers.Project(h.App, scope.Project, middleware.CurrentUser(e)))
}

// Update serves PATCH /api/workspaces/{slug}/projects/{project_id}/. The router
// enforces the admin role (workspace admin or project admin).
func (h *Handler) Update(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	project := scope.Project
	if project.GetString("archived_at") != "" {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Archived projects cannot be updated"})
	}
	for _, field := range []string{"name", "identifier", "description", "description_text", "description_html", "emoji", "icon_prop", "cover_image", "logo_props", "timezone", "network", "archive_in", "close_in"} {
		if v, ok := body[field]; ok {
			project.Set(field, v)
		}
	}
	for _, flag := range []string{"module_view", "cycle_view", "issue_views_view", "page_view", "intake_view", "is_time_tracking_enabled", "is_issue_type_enabled", "guest_view_all_features"} {
		if v, ok := body[flag]; ok {
			project.Set(flag, boolValue(v, false))
		}
	}
	// Plane accepts `inbox_view` as an alias for `intake_view`.
	if v, ok := body["inbox_view"]; ok {
		project.Set("intake_view", boolValue(v, false))
	}
	if user := middleware.CurrentUser(e); user != nil {
		project.Set("updated_by", user.Id)
	}
	if err := h.App.Save(project); err != nil {
		return serializers.Validation(e, "name", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.Project(h.App, project, middleware.CurrentUser(e)))
}

// Delete serves DELETE /api/workspaces/{slug}/projects/{project_id}/. Plane
// soft-deletes, which frees the (name, workspace) unique index because it is
// partial on `deleted_at = ”`.
func (h *Handler) Delete(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	project := scope.Project
	project.Set("deleted_at", serializers.Now())
	if err := h.App.Save(project); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// States serves GET /api/workspaces/{slug}/projects/{project_id}/states/. Plane
// returns a plain array ordered by `sequence`, with a computed `order` giving
// each state's position within its group (StateViewSet.list).
func (h *Handler) States(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}

	states, err := h.App.FindRecordsByFilter(
		"states",
		// Triage is dropped by Django's default manager (`StateManager`, which
		// excludes `group="triage"`), NOT by the `is_triage` flag — Django's
		// `DEFAULT_STATES` never sets that flag, so the seeded Triage state has
		// `is_triage=False` like every other state. Filtering on `is_triage`
		// alone leaks Triage into the project's state list, which the frontends
		// then render as an extra group.
		"project = {:project} && deleted_at = '' && group != 'triage' && is_triage = false",
		"sequence", 0, 0,
		dbx.Params{"project": scope.Project.Id},
	)
	if err != nil {
		return err
	}

	// order = index / count within the state's group.
	groupCounts := map[string]int{}
	for _, state := range states {
		groupCounts[state.GetString("group")]++
	}
	groupSeen := map[string]int{}
	results := make([]any, 0, len(states))
	for _, state := range states {
		group := state.GetString("group")
		groupSeen[group]++
		order := float64(groupSeen[group]) / float64(groupCounts[group])
		results = append(results, serializers.State(h.App, state, order))
	}

	if e.Request.URL.Query().Get("grouped") == "true" {
		grouped := map[string][]any{}
		for i, state := range states {
			grouped[state.GetString("group")] = append(grouped[state.GetString("group")], results[i])
		}
		return e.JSON(http.StatusOK, grouped)
	}
	return e.JSON(http.StatusOK, results)
}

// Labels serves GET /api/workspaces/{slug}/projects/{project_id}/labels/.
// NOTE: Plane's LabelViewSet is mounted at `…/issue-labels/`; this path follows
// the endpoint list for this slice and returns the same LabelSerializer array,
// ordered by sort_order.
func (h *Handler) Labels(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	labels, err := h.App.FindRecordsByFilter(
		"labels",
		"project = {:project} && deleted_at = ''",
		"sort_order", 0, 0,
		dbx.Params{"project": scope.Project.Id},
	)
	if err != nil {
		return err
	}
	results := make([]any, 0, len(labels))
	for _, label := range labels {
		results = append(results, serializers.Label(h.App, label))
	}
	return e.JSON(http.StatusOK, results)
}

// seedDefaultStates bulk-creates Plane's six default states for a new project.
func (h *Handler) seedDefaultStates(project *core.Record, workspaceID, userID string) error {
	collection, err := h.App.FindCollectionByNameOrId("states")
	if err != nil {
		return err
	}
	for _, spec := range defaultStates {
		state := core.NewRecord(collection)
		state.Set("uuid", serializers.NewUUID())
		state.Set("project", project.Id)
		state.Set("workspace", workspaceID)
		state.Set("name", spec.Name)
		state.Set("color", spec.Color)
		state.Set("sequence", spec.Sequence)
		state.Set("group", spec.Group)
		state.Set("default", spec.Default)
		state.Set("is_triage", false)
		state.Set("slug", slugify(spec.Name))
		state.Set("created_by", userID)
		if err := h.App.Save(state); err != nil {
			return err
		}
	}
	return nil
}

// projectIDsForMember returns the PocketBase ids of the projects the user is an
// active member of.
func (h *Handler) projectIDsForMember(userID, workspaceID string) []string {
	members, err := h.App.FindRecordsByFilter(
		"project_members",
		"member = {:member} && workspace = {:workspace} && is_active = true && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"member": userID, "workspace": workspaceID},
	)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.GetString("project"))
	}
	return ids
}

// relationTarget maps a project relation field to its collection, for
// resolving a Plane UUID supplied in a request body.
func relationTarget(field string) string {
	switch field {
	case "default_assignee", "project_lead":
		return "users"
	case "default_state":
		return "states"
	default:
		return field
	}
}

// slugify mirrors Django's slugify closely enough for Plane's state names.
func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func boolValue(v any, fallback bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return fallback
}

func intValue(v any, fallback int) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case string:
		if n, err := strconv.Atoi(t); err == nil {
			return n
		}
	}
	return fallback
}
