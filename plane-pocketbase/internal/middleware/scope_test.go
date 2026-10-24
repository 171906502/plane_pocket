package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// roleScope builds the scope ResolveWorkspace+ResolveProject produce for a
// caller holding the given workspace and project roles. ProjectRole is the
// **raw** project-member role: ResolveProject only promotes a workspace Admin
// when the caller has no project role at all, so a workspace Admin who is an
// active project member keeps their project role.
func roleScope(wsRole, pjRole int) *Scope {
	wsMember := &core.Record{}
	pjMember := &core.Record{}
	s := &Scope{
		Workspace:       &core.Record{},
		Project:         &core.Record{},
		WorkspaceRole:   wsRole,
		ProjectRole:     pjRole,
		WorkspaceMember: wsMember,
		ProjectMember:   pjMember,
	}
	if wsRole == 0 {
		s.WorkspaceMember = nil
	}
	if pjRole == 0 {
		s.ProjectMember = nil
	}
	return s
}

func guardEvent(t *testing.T, scope *Scope) (*core.RequestEvent, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: nil}
	e.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	e.Response = rec
	e.Set(string(ctxScopeKey), scope)
	return e, rec
}

// guardAllows runs a guard and reports whether it forwarded the request.
func guardAllows(t *testing.T, guard func(*core.RequestEvent) error, scope *Scope) bool {
	t.Helper()
	e, rec := guardEvent(t, scope)
	err := guard(e)
	if err != nil {
		return false
	}
	// e.Next() on an event with no registered handlers returns nil and leaves
	// the recorder untouched; a denial always writes a 403 body.
	return rec.Code == 0 || rec.Code != http.StatusForbidden
}

// TestRequireProjectRoleWorkspaceAdminBypass pins the second clause of
// plane.app.permissions.allow_permission's PROJECT branch: a workspace Admin who
// is an active project member passes every project-scoped row regardless of the
// project role they hold.
func TestRequireProjectRoleWorkspaceAdminBypass(t *testing.T) {
	cases := []struct {
		name   string
		scope  *Scope
		min    int
		expect bool
	}{
		{"ws-admin + project Member passes Admin-only", roleScope(RoleAdmin, RoleMember), RoleAdmin, true},
		{"ws-admin + project Guest passes Admin-only", roleScope(RoleAdmin, RoleGuest), RoleAdmin, true},
		{"ws-admin + project Member passes Member-only", roleScope(RoleAdmin, RoleMember), RoleMember, true},
		{"project Admin passes Admin-only", roleScope(RoleMember, RoleAdmin), RoleAdmin, true},
		{"project Member refused Admin-only", roleScope(RoleMember, RoleMember), RoleAdmin, false},
		{"project Guest refused Member-only", roleScope(RoleGuest, RoleGuest), RoleMember, false},
		// A workspace Admin with NO project membership has no scope at all —
		// ResolveProject 404s before any guard runs.
		{"ws-admin without project membership refused", roleScope(RoleAdmin, 0), RoleAdmin, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := guardAllows(t, RequireProjectRole(tc.min), tc.scope); got != tc.expect {
				t.Fatalf("RequireProjectRole(%d) allowed = %v, want %v", tc.min, got, tc.expect)
			}
		})
	}
}

// TestRequireProjectMemberRoleHasNoBypass pins ProjectMemberPermission's
// non-safe branch, which DeployBoardViewSet uses: a plain project role lookup
// with no workspace-Admin escape hatch.
func TestRequireProjectMemberRoleHasNoBypass(t *testing.T) {
	cases := []struct {
		name   string
		scope  *Scope
		min    int
		expect bool
	}{
		{"project Member passes Member-only", roleScope(RoleMember, RoleMember), RoleMember, true},
		{"project Admin passes Member-only", roleScope(RoleMember, RoleAdmin), RoleMember, true},
		{"project Guest refused Member-only", roleScope(RoleAdmin, RoleGuest), RoleMember, false},
		// The distinguishing case: a workspace Admin is NOT admitted here.
		{"ws-admin + project Member refused Admin-only", roleScope(RoleAdmin, RoleMember), RoleAdmin, false},
		{"ws-admin + project Admin passes Admin-only", roleScope(RoleAdmin, RoleAdmin), RoleAdmin, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := guardAllows(t, RequireProjectMemberRole(tc.min), tc.scope); got != tc.expect {
				t.Fatalf("RequireProjectMemberRole(%d) allowed = %v, want %v", tc.min, got, tc.expect)
			}
		})
	}
}

// TestRequireWorkspaceRole checks the workspace axis is unaffected by the
// project-side bypass.
func TestRequireWorkspaceRole(t *testing.T) {
	cases := []struct {
		scope  *Scope
		min    int
		expect bool
	}{
		{roleScope(RoleAdmin, RoleAdmin), RoleAdmin, true},
		{roleScope(RoleMember, RoleMember), RoleAdmin, false},
		{roleScope(RoleMember, RoleMember), RoleMember, true},
		{roleScope(RoleGuest, RoleGuest), RoleMember, false},
	}
	for _, tc := range cases {
		if got := guardAllows(t, RequireWorkspaceRole(tc.min), tc.scope); got != tc.expect {
			t.Fatalf("RequireWorkspaceRole(%d) allowed = %v, want %v", tc.min, got, tc.expect)
		}
	}
}

// TestProjectRoleAtLeast pins the predicate the inline handlers use. It must
// agree with RequireProjectRole (same allow_permission branch) and disagree
// with RequireProjectMemberRole, which deliberately has no workspace-Admin
// escape hatch.
func TestProjectRoleAtLeast(t *testing.T) {
	cases := []struct {
		name   string
		scope  *Scope
		min    int
		expect bool
	}{
		{"project admin", roleScope(RoleAdmin, RoleAdmin), RoleAdmin, true},
		{"project member for member floor", roleScope(RoleMember, RoleMember), RoleMember, true},
		{"project guest refused member floor", roleScope(RoleGuest, RoleGuest), RoleMember, false},
		// The clause this helper exists for: workspace Admin + Guest project role.
		{"ws admin with guest project role", roleScope(RoleAdmin, RoleGuest), RoleMember, true},
		{"ws admin with guest project role for admin floor", roleScope(RoleAdmin, RoleGuest), RoleAdmin, true},
		// A workspace Member is not an Admin, so no bypass.
		{"ws member with guest project role", roleScope(RoleMember, RoleGuest), RoleMember, false},
		// No project membership at all -> ResolveProject would have 404'd, but
		// the predicate must still answer false rather than panic.
		{"no project member record", roleScope(RoleAdmin, 0), RoleMember, false},
		{"nil scope", nil, RoleMember, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := guardEvent(t, tc.scope)
			if got := ProjectRoleAtLeast(e, tc.min); got != tc.expect {
				t.Fatalf("ProjectRoleAtLeast(%d) = %v, want %v", tc.min, got, tc.expect)
			}
		})
	}
}

// TestProjectRoleAtLeastMatchesRequireProjectRole guards against the two
// drifting apart: for every scope they must agree.
func TestProjectRoleAtLeastMatchesRequireProjectRole(t *testing.T) {
	scopes := []*Scope{
		roleScope(RoleAdmin, RoleAdmin),
		roleScope(RoleMember, RoleMember),
		roleScope(RoleGuest, RoleGuest),
		roleScope(RoleAdmin, RoleGuest),
		roleScope(RoleMember, RoleGuest),
		roleScope(RoleAdmin, 0),
		nil,
	}
	for _, min := range []int{RoleGuest, RoleMember, RoleAdmin} {
		for _, s := range scopes {
			e, _ := guardEvent(t, s)
			want := ProjectRoleAtLeast(e, min)
			if got := guardAllows(t, RequireProjectRole(min), s); got != want {
				t.Fatalf("min=%d scope=%+v: RequireProjectRole=%v but ProjectRoleAtLeast=%v", min, s, got, want)
			}
		}
	}
}
