package middleware

import (
	"net/http"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// Plane's role codes. Both workspace and project memberships use the same
// scale, and they are independent axes — a workspace Admin has no implicit
// project role. See spec/permissions.md.
const (
	RoleGuest  = 5
	RoleMember = 15
	RoleAdmin  = 20
)

// Collection names referenced during scope resolution.
const (
	workspacesCollection       = "workspaces"
	workspaceMembersCollection = "workspace_members"
	projectsCollection         = "projects"
	projectMembersCollection   = "project_members"
)

// Scope is the authorized request context: which workspace and project the
// caller is acting on, and what role they hold in each.
type Scope struct {
	Workspace *core.Record
	Project   *core.Record

	WorkspaceMember *core.Record
	ProjectMember   *core.Record

	WorkspaceRole int
	ProjectRole   int
}

const ctxScopeKey = contextKey("plane.scope")

// ResolveWorkspace reads the `slug` route parameter and loads the workspace
// together with the caller's active membership.
//
// A caller with no active membership gets the same 404 as a non-existent
// workspace: Plane deliberately does not distinguish the two, so that the
// existence of a private workspace is not leaked.
func ResolveWorkspace(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		slug := e.Request.PathValue("slug")
		if slug == "" {
			return e.Next()
		}

		workspace, err := app.FindFirstRecordByFilter(
			workspacesCollection,
			"slug = {:slug} && deleted_at = ''",
			dbx.Params{"slug": slug},
		)
		if err != nil || workspace == nil {
			return serializers.NotFound(e, "")
		}

		scope := &Scope{Workspace: workspace}

		if user := CurrentUser(e); user != nil {
			member, err := app.FindFirstRecordByFilter(
				workspaceMembersCollection,
				"workspace = {:workspace} && member = {:member} && is_active = true",
				dbx.Params{"workspace": workspace.Id, "member": user.Id},
			)
			if err == nil && member != nil {
				scope.WorkspaceMember = member
				scope.WorkspaceRole = member.GetInt("role")
			}
		}

		if scope.WorkspaceRole == 0 {
			return serializers.NotFound(e, "")
		}

		e.Set(string(ctxScopeKey), scope)
		return e.Next()
	}
}

// ResolveProject reads the `project_id` route parameter (a Plane UUID) and
// loads the project, enforcing that it belongs to the workspace already
// resolved by ResolveWorkspace.
func ResolveProject(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		scope := ScopeOf(e)
		if scope == nil || scope.Workspace == nil {
			return e.Next()
		}

		projectUUID := e.Request.PathValue("project_id")
		if projectUUID == "" {
			return e.Next()
		}

		project, err := serializers.FindByUUID(app, projectsCollection, projectUUID)
		if err != nil || project == nil {
			return serializers.NotFound(e, "")
		}
		// A project from another workspace must look absent, not forbidden.
		if project.GetString("workspace") != scope.Workspace.Id || project.GetString("deleted_at") != "" {
			return serializers.NotFound(e, "")
		}

		scope.Project = project

		if user := CurrentUser(e); user != nil {
			member, err := app.FindFirstRecordByFilter(
				projectMembersCollection,
				"project = {:project} && member = {:member} && is_active = true",
				dbx.Params{"project": project.Id, "member": user.Id},
			)
			if err == nil && member != nil {
				scope.ProjectMember = member
				scope.ProjectRole = member.GetInt("role")
			}
		}

		// Plane's project guards: the caller needs a project role, OR must be an
		// active workspace Admin who is also an active project member. The
		// "also a member" clause matters — workspace Admins are not automatically
		// project members.
		if scope.ProjectRole == 0 {
			if scope.WorkspaceRole >= RoleAdmin && scope.ProjectMember != nil {
				scope.ProjectRole = RoleAdmin
			} else {
				return serializers.NotFound(e, "")
			}
		}

		return e.Next()
	}
}

// ScopeOf returns the resolved scope, or nil when the route is not
// workspace-scoped.
func ScopeOf(e *core.RequestEvent) *Scope {
	if v, ok := e.Get(string(ctxScopeKey)).(*Scope); ok {
		return v
	}
	return nil
}

// RequireWorkspaceRole rejects callers below the given workspace role.
func RequireWorkspaceRole(minRole int) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		scope := ScopeOf(e)
		if scope == nil || scope.WorkspaceRole < minRole {
			return serializers.Forbidden(e, "You do not have permission to perform this action.")
		}
		return e.Next()
	}
}

// RequireProjectRole rejects callers below the given project role.
func RequireProjectRole(minRole int) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		scope := ScopeOf(e)
		if scope == nil || scope.ProjectRole < minRole {
			return serializers.Forbidden(e, "You do not have permission to perform this action.")
		}
		return e.Next()
	}
}

// IsCreator reports whether the given record was created by the current user.
//
// Plane evaluates this *before* the role check on several endpoints, which is
// why a Guest can edit or delete their own comment, cycle or module. Handlers
// that mirror those endpoints must call this before RequireProjectRole.
func IsCreator(e *core.RequestEvent, record *core.Record) bool {
	user := CurrentUser(e)
	if user == nil || record == nil {
		return false
	}
	return record.GetString("created_by") == user.Id
}

// CanWrite reports whether the caller may mutate project-scoped content.
// Guests are read-only except for resources they created (see IsCreator).
func CanWrite(scope *Scope) bool {
	return scope != nil && scope.ProjectRole >= RoleMember
}

// noContent is a small helper for DELETE handlers that return 204, matching
// DRF's behaviour for successful destroys.
func noContent(e *core.RequestEvent) error {
	return e.NoContent(http.StatusNoContent)
}
