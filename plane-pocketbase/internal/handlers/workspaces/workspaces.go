// Package workspaces implements the workspace-scoped Plane endpoints:
// /api/workspaces/ and /api/workspaces/{slug}/…
package workspaces

import (
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// backgroundPalette mirrors Plane's workspace avatar colours.
var backgroundPalette = []string{"#F44336", "#E91E63", "#9C27B0", "#673AB7", "#3F51B5", "#2196F3", "#03A9F4", "#00BCD4"}

// Handler holds the app dependency for the workspace routes.
type Handler struct {
	App core.App
}

// New returns a workspace handler bound to app.
func New(app core.App) *Handler {
	return &Handler{App: app}
}

// List serves GET /api/workspaces/: a cursor-paginated envelope of the
// workspaces the caller is an active member of (WorkSpaceViewSet.list).
//
// Plane orders by workspace name; this slice paginates over `workspace_members`
// (the only collection that can express "workspaces I belong to" as a
// PocketBase filter) and therefore orders by membership creation. The envelope
// shape is unaffected.
func (h *Handler) List(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	perPage, cursor := serializers.ListParams(e, serializers.MaxPerPage)
	envelope, err := serializers.Paginate(h.App, serializers.ListQuery{
		Collection: "workspace_members",
		Filter:     "member = {:member} && is_active = true && deleted_at = ''",
		Params:     dbx.Params{"member": user.Id},
		Sort:       "created_at",
		PerPage:    perPage,
		Cursor:     cursor,
	}, func(membership *core.Record) any {
		workspace, _ := h.App.FindRecordById("workspaces", membership.GetString("workspace"))
		if workspace == nil || workspace.GetString("deleted_at") != "" {
			return nil
		}
		return serializers.Workspace(h.App, workspace, membership.GetInt("role"), h.countActiveMembers(workspace.Id))
	})
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, envelope)
}

// Create serves POST /api/workspaces/. It creates the workspace and the owner's
// `workspace_members` row with role 20 (WorkSpaceViewSet.create).
func (h *Handler) Create(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	name := strings.TrimSpace(asString(body["name"]))
	slug := strings.TrimSpace(asString(body["slug"]))
	if name == "" || slug == "" {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Both name and slug are required"})
	}
	if len(name) > 80 || len(slug) > 48 {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "The maximum length for name is 80 and for slug is 48"})
	}

	collection, err := h.App.FindCollectionByNameOrId("workspaces")
	if err != nil {
		return err
	}
	workspace := core.NewRecord(collection)
	workspace.Set("uuid", serializers.NewUUID())
	workspace.Set("name", name)
	workspace.Set("slug", slug)
	workspace.Set("owner", user.Id)
	workspace.Set("created_by", user.Id)
	workspace.Set("background_color", backgroundPalette[rand.Intn(len(backgroundPalette))])
	if v := asString(body["organization_size"]); v != "" {
		workspace.Set("organization_size", v)
	}
	if v := asString(body["timezone"]); v != "" {
		workspace.Set("timezone", v)
	}
	if err := h.App.Save(workspace); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return e.JSON(http.StatusConflict, map[string]any{"slug": "The workspace with the slug already exists"})
		}
		return serializers.Validation(e, "slug", err.Error())
	}

	membersCollection, err := h.App.FindCollectionByNameOrId("workspace_members")
	if err != nil {
		return err
	}
	membership := core.NewRecord(membersCollection)
	membership.Set("uuid", serializers.NewUUID())
	membership.Set("workspace", workspace.Id)
	membership.Set("member", user.Id)
	membership.Set("role", middleware.RoleAdmin)
	membership.Set("is_active", true)
	membership.Set("created_by", user.Id)
	if v := asString(body["company_role"]); v != "" {
		membership.Set("company_role", v)
	}
	if err := h.App.Save(membership); err != nil {
		return err
	}

	return e.JSON(http.StatusCreated, serializers.Workspace(h.App, workspace, middleware.RoleAdmin, 1))
}

// Detail serves GET /api/workspaces/{slug}/ (WorkSpaceViewSet.retrieve).
func (h *Handler) Detail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	return e.JSON(http.StatusOK, serializers.Workspace(
		h.App, scope.Workspace, scope.WorkspaceRole, h.countActiveMembers(scope.Workspace.Id),
	))
}

// Update serves PATCH /api/workspaces/{slug}/ (admin only, enforced by the
// router).
func (h *Handler) Update(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	workspace := scope.Workspace
	for _, field := range []string{"name", "slug", "organization_size", "timezone", "background_color", "logo"} {
		if v, ok := body[field]; ok {
			workspace.Set(field, v)
		}
	}
	if user := middleware.CurrentUser(e); user != nil {
		workspace.Set("updated_by", user.Id)
	}
	if err := h.App.Save(workspace); err != nil {
		return serializers.Validation(e, "slug", err.Error())
	}
	return e.JSON(http.StatusOK, serializers.Workspace(
		h.App, workspace, scope.WorkspaceRole, h.countActiveMembers(workspace.Id),
	))
}

// Delete serves DELETE /api/workspaces/{slug}/ (admin only). Plane soft-deletes
// and appends the deletion epoch to the slug so the unique slug can be reused
// (Workspace.delete in plane/db/models/workspace.py).
func (h *Handler) Delete(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	workspace := scope.Workspace
	now := serializers.Now()
	workspace.Set("deleted_at", now)
	workspace.Set("slug", workspace.GetString("slug")+"__"+time.Now().UTC().Format("20060102150405"))
	if err := h.App.Save(workspace); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// Members serves GET /api/workspaces/{slug}/members/. Plane returns a plain,
// non-paginated array of `{id, member, role}` (WorkSpaceMemberViewSet.list);
// `member` is a nested user object, not an id.
func (h *Handler) Members(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}

	memberships, err := h.App.FindRecordsByFilter(
		"workspace_members",
		"workspace = {:workspace} && is_active = true && deleted_at = ''",
		"created_at", 0, 0,
		dbx.Params{"workspace": scope.Workspace.Id},
	)
	if err != nil {
		return err
	}

	admin := scope.WorkspaceRole > middleware.RoleGuest
	results := make([]any, 0, len(memberships))
	for _, membership := range memberships {
		results = append(results, serializers.WorkspaceMember(h.App, membership, admin))
	}
	return e.JSON(http.StatusOK, results)
}

// MemberMe serves GET /api/workspaces/{slug}/workspace-members/me/
// (WorkspaceMemberUserEndpoint.get) — the caller's own membership in this
// workspace, annotated with their draft-issue count.
//
// This is the web app's membership gate: `layouts/auth-layout/workspace-wrapper.tsx`
// reads `workspaceInfoBySlug(slug)` and renders "You're not a member of this
// workspace" whenever the entry is undefined. Django answers a non-member with a
// serialized `None` (all fields null, HTTP 200); this does the same rather than
// 404ing, because the frontend distinguishes "loaded, not a member" from
// "failed to load" by shape, not status.
func (h *Handler) MemberMe(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	// ResolveWorkspace already 404s a non-member, so reaching here means the
	// caller is active; the lookup mirrors Django's `.filter(...).first()`.
	membership, _ := h.App.FindFirstRecordByFilter(
		"workspace_members",
		"workspace = {:workspace} && member = {:member} && is_active = true",
		dbx.Params{"workspace": scope.Workspace.Id, "member": user.Id},
	)

	var draftIssueCount int
	if membership != nil {
		_ = h.App.DB().NewQuery(
			"SELECT COUNT(*) FROM draft_issues WHERE created_by = {:user} AND workspace = {:workspace} AND deleted_at = ''",
		).Bind(dbx.Params{"user": user.Id, "workspace": scope.Workspace.Id}).Row(&draftIssueCount)
	}

	return e.JSON(http.StatusOK, serializers.WorkspaceMemberMe(h.App, membership, draftIssueCount))
}

// countActiveMembers counts active, non-bot members (the `total_members`
// annotation Plane attaches to a workspace).
func (h *Handler) countActiveMembers(workspaceID string) int {
	members, err := h.App.FindRecordsByFilter(
		"workspace_members",
		"workspace = {:workspace} && is_active = true && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"workspace": workspaceID},
	)
	if err != nil {
		return 0
	}
	total := 0
	for _, m := range members {
		user, _ := h.App.FindRecordById("users", m.GetString("member"))
		if user == nil || user.GetBool("is_bot") {
			continue
		}
		total++
	}
	return total
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
