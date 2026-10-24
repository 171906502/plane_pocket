// Method-level gaps on the project member and state collections, surfaced by
// tests/browser/audit-methods.py (the paths were registered but the write
// methods the frontend calls were not — the #23/#24 defect class):
//
//	POST          …/projects/{project_id}/members/          (bulk add)
//	GET/PATCH/DELETE …/projects/{project_id}/members/{pk}/  (detail)
//	POST          …/projects/{project_id}/states/           (create)
//	GET/PATCH/PUT/DELETE …/projects/{project_id}/states/{state_id}/ (detail)
//
// Contracts: ProjectMemberViewSet (project.member.update / .remove → project
// ADMIN, permissions.md §3.2) and StateViewSet (`state.manage` → project ADMIN,
// `state/base.py:46,105,113`). The GET halves answer for every project role.
package projects

import (
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// ---------------------------------------------------------------------------
// project members
// ---------------------------------------------------------------------------

// MembersCreate serves POST …/projects/{project_id}/members/
// (ProjectMemberViewSet.create). Body: `{members: [{member_id, role}]}` — the
// frontend's `IProjectBulkAddFormData` (project-member.service.ts:31). Django
// bulk-creates the rows with `ignore_conflicts=True`, so entries that are
// already members (or not active workspace members) are skipped rather than
// rejected. Answer 201 with the resulting memberships.
func (h *Handler) MembersCreate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	if scope.ProjectRole < middleware.RoleAdmin {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	entries, ok := body["members"].([]any)
	if !ok || len(entries) == 0 {
		return serializers.Validation(e, "members", "This field is required.")
	}

	collection, err := h.App.FindCollectionByNameOrId("project_members")
	if err != nil {
		return err
	}

	out := make([]any, 0, len(entries))
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		memberID := asString(entry["member_id"])
		if memberID == "" {
			continue
		}
		member, _ := serializers.FindByUUID(h.App, "users", memberID)
		if member == nil {
			continue
		}
		// Only active workspace members can be added (Django filters
		// WorkspaceMember by is_active before bulk-creating).
		wm, _ := h.App.FindFirstRecordByFilter(
			"workspace_members",
			"workspace = {:ws} && member = {:m} && is_active = true && deleted_at = ''",
			dbx.Params{"ws": scope.Workspace.Id, "m": member.Id},
		)
		if wm == nil {
			continue
		}

		role := middleware.RoleMember
		if r, ok := entry["role"].(float64); ok && int(r) > 0 {
			role = int(r)
		}

		// ignore_conflicts: re-activate an existing row instead of erroring on
		// the (project, member) unique pair.
		membership, _ := h.App.FindFirstRecordByFilter(
			"project_members",
			"project = {:p} && member = {:m} && deleted_at = ''",
			dbx.Params{"p": scope.Project.Id, "m": member.Id},
		)
		if membership == nil {
			membership = core.NewRecord(collection)
			membership.Set("uuid", serializers.NewUUID())
			membership.Set("project", scope.Project.Id)
			membership.Set("workspace", scope.Workspace.Id)
			membership.Set("member", member.Id)
			membership.Set("created_by", user.Id)
		}
		membership.Set("role", role)
		membership.Set("is_active", true)
		membership.Set("updated_by", user.Id)
		if err := h.App.Save(membership); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		out = append(out, serializers.ProjectMemberAdmin(h.App, membership))
	}
	return e.JSON(http.StatusCreated, out)
}

// MemberDetail serves GET/PATCH/DELETE …/projects/{project_id}/members/{pk}/
// (ProjectMemberViewSet retrieve / partial_update / destroy). Mutations are
// project-ADMIN only (permissions.md §3.2 project.member.update / .remove);
// Django's destroy blocks self-removal — use the `members/leave/` endpoint —
// and deactivates the row.
func (h *Handler) MemberDetail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	membership, _ := serializers.FindByUUID(h.App, "project_members", e.Request.PathValue("pk"))
	if membership == nil || membership.GetString("project") != scope.Project.Id ||
		membership.GetString("deleted_at") != "" {
		return serializers.NotFound(e, "Not found.")
	}

	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, serializers.ProjectMemberAdmin(h.App, membership))

	case http.MethodPatch:
		if scope.ProjectRole < middleware.RoleAdmin {
			return serializers.Forbidden(e, "You do not have permission to perform this action.")
		}
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		if raw, ok := body["role"]; ok {
			if r, ok := raw.(float64); ok && int(r) > 0 {
				membership.Set("role", int(r))
			}
		}
		if v, ok := body["is_active"]; ok {
			membership.Set("is_active", v)
		}
		if v, ok := body["sort_order"]; ok {
			membership.Set("sort_order", v)
		}
		if v, ok := body["comment"]; ok {
			membership.Set("comment", v)
		}
		membership.Set("updated_by", user.Id)
		if err := h.App.Save(membership); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.JSON(http.StatusOK, serializers.ProjectMemberAdmin(h.App, membership))

	case http.MethodDelete:
		if scope.ProjectRole < middleware.RoleAdmin {
			return serializers.Forbidden(e, "You do not have permission to perform this action.")
		}
		if membership.GetString("member") == user.Id {
			// Django routes self-removal through POST …/members/leave/.
			return e.JSON(http.StatusBadRequest, map[string]any{"error": "You cannot remove yourself from the project"})
		}
		membership.Set("is_active", false)
		membership.Set("deleted_at", serializers.Now())
		membership.Set("updated_by", user.Id)
		if err := h.App.Save(membership); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}

// ---------------------------------------------------------------------------
// states
// ---------------------------------------------------------------------------

// stateOrder computes the `order` value StateViewSet.list derives per group:
// the state's position (by sequence) within its group over the group's size.
func (h *Handler) stateOrder(state *core.Record) float64 {
	group := state.GetString("group")
	states, err := h.App.FindRecordsByFilter(
		"states",
		"project = {:project} && group = {:group} && deleted_at = ''",
		"sequence", 0, 0,
		dbx.Params{"project": state.GetString("project"), "group": group},
	)
	if err != nil || len(states) == 0 {
		return 0
	}
	for i, row := range states {
		if row.Id == state.Id {
			return float64(i+1) / float64(len(states))
		}
	}
	return 0
}

// stateNameTaken reports whether another live state of the project uses the
// name (State.name is unique per project; Django maps the IntegrityError to a
// 400, mirroring the label handlers).
func (h *Handler) stateNameTaken(projectID, name, excludeID string) bool {
	existing, _ := h.App.FindFirstRecordByFilter(
		"states",
		"project = {:p} && name = {:n} && deleted_at = ''",
		dbx.Params{"p": projectID, "n": name},
	)
	return existing != nil && existing.Id != excludeID
}

// clearOtherDefaults drops `default` from every other state of the project.
func (h *Handler) clearOtherDefaults(projectID, keepID string) {
	states, err := h.App.FindRecordsByFilter(
		"states",
		"project = {:p} && default = true && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"p": projectID},
	)
	if err != nil {
		return
	}
	for _, row := range states {
		if row.Id == keepID {
			continue
		}
		row.Set("default", false)
		_ = h.App.Save(row)
	}
}

// StatesCreate serves POST …/projects/{project_id}/states/ (StateViewSet
// create, project ADMIN only). `sequence` is MAX+1 within the project; a
// state created with `default: true` takes the default flag from the others.
func (h *Handler) StatesCreate(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	if scope.ProjectRole < middleware.RoleAdmin {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	name := strings.TrimSpace(asString(body["name"]))
	if name == "" {
		return serializers.Validation(e, "name", "This field is required.")
	}
	if h.stateNameTaken(scope.Project.Id, name, "") {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "State with the same name already exists in the project"})
	}
	group := asString(body["group"])
	switch group {
	case "backlog", "unstarted", "started", "completed", "cancelled":
	case "":
		group = "unstarted"
	default:
		return serializers.Validation(e, "group", `"group" is not a valid choice.`)
	}

	var maxSequence int
	if err := h.App.DB().
		NewQuery("SELECT COALESCE(MAX(sequence), 0) FROM states WHERE project = {:project}").
		Bind(dbx.Params{"project": scope.Project.Id}).
		Row(&maxSequence); err != nil {
		return err
	}

	collection, err := h.App.FindCollectionByNameOrId("states")
	if err != nil {
		return err
	}
	state := core.NewRecord(collection)
	state.Set("uuid", serializers.NewUUID())
	state.Set("project", scope.Project.Id)
	state.Set("workspace", scope.Workspace.Id)
	state.Set("name", name)
	state.Set("color", asString(body["color"]))
	state.Set("description", asString(body["description"]))
	state.Set("group", group)
	state.Set("sequence", maxSequence+1)
	state.Set("default", false)
	state.Set("is_triage", false)
	state.Set("slug", slugify(name))
	state.Set("created_by", user.Id)
	if v, ok := body["default"]; ok {
		state.Set("default", v)
	}
	if err := h.App.Save(state); err != nil {
		return serializers.Validation(e, "name", err.Error())
	}
	if state.GetBool("default") {
		h.clearOtherDefaults(scope.Project.Id, state.Id)
	}
	return e.JSON(http.StatusCreated, serializers.State(h.App, state, h.stateOrder(state)))
}

// StateDetail serves GET/PATCH/PUT/DELETE
// …/projects/{project_id}/states/{state_id}/ (StateViewSet retrieve /
// partial_update / update / destroy). Mutations are project ADMIN only; DELETE
// is a soft delete, like every other collection in this port.
func (h *Handler) StateDetail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Project == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	state, _ := serializers.FindByUUID(h.App, "states", e.Request.PathValue("state_id"))
	if state == nil || state.GetString("project") != scope.Project.Id ||
		state.GetString("deleted_at") != "" {
		return serializers.NotFound(e, "Not found.")
	}

	switch e.Request.Method {
	case http.MethodGet:
		return e.JSON(http.StatusOK, serializers.State(h.App, state, h.stateOrder(state)))

	case http.MethodPatch, http.MethodPut:
		if scope.ProjectRole < middleware.RoleAdmin {
			return serializers.Forbidden(e, "You do not have permission to perform this action.")
		}
		body := map[string]any{}
		if err := e.BindBody(&body); err != nil {
			return serializers.BadRequest(e, "Invalid request body.")
		}
		if raw, ok := body["name"]; ok {
			name := strings.TrimSpace(asString(raw))
			if name == "" {
				return serializers.Validation(e, "name", "This field cannot be blank.")
			}
			if h.stateNameTaken(scope.Project.Id, name, state.Id) {
				return e.JSON(http.StatusBadRequest, map[string]any{"error": "State with the same name already exists in the project"})
			}
			state.Set("name", name)
			state.Set("slug", slugify(name))
		}
		for _, field := range []string{"color", "description", "group"} {
			if v, ok := body[field]; ok && asString(v) != "" {
				state.Set(field, v)
			}
		}
		if v, ok := body["default"]; ok {
			state.Set("default", v)
		}
		if v, ok := body["sequence"]; ok {
			state.Set("sequence", v)
		}
		state.Set("updated_by", user.Id)
		if err := h.App.Save(state); err != nil {
			return serializers.Validation(e, "name", err.Error())
		}
		if state.GetBool("default") {
			h.clearOtherDefaults(scope.Project.Id, state.Id)
		}
		return e.JSON(http.StatusOK, serializers.State(h.App, state, h.stateOrder(state)))

	case http.MethodDelete:
		if scope.ProjectRole < middleware.RoleAdmin {
			return serializers.Forbidden(e, "You do not have permission to perform this action.")
		}
		state.Set("deleted_at", serializers.Now())
		state.Set("updated_by", user.Id)
		if err := h.App.Save(state); err != nil {
			return serializers.Validation(e, "detail", err.Error())
		}
		return e.NoContent(http.StatusNoContent)
	}
	return serializers.BadRequest(e, "Unsupported method")
}
