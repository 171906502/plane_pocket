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

// This file implements the workspace invitation and membership-leave endpoints.
//
// Invitations (plane/app/views/workspace/invite.py):
//
//	GET    /api/workspaces/{slug}/invitations/             WorkspaceInvitationsViewset.list
//	POST   /api/workspaces/{slug}/invitations/             WorkspaceInvitationsViewset.create
//	GET    /api/workspaces/{slug}/invitations/{ik}/join/   WorkspaceJoinEndpoint.get   (AllowAny)
//	POST   /api/workspaces/{slug}/invitations/{ik}/join/   WorkspaceJoinEndpoint.post
//	PATCH  /api/workspaces/{slug}/invitations/{ik}/        WorkspaceInvitationsViewset.partial_update
//	DELETE /api/workspaces/{slug}/invitations/{ik}/        WorkspaceInvitationsViewset.destroy
//
// Leave (plane/app/views/workspace/member.py, plane/app/views/project/member.py):
//
//	POST   /api/workspaces/{slug}/members/leave/
//	POST   /api/workspaces/{slug}/projects/{pid}/members/leave/
//
// The `join/` routes are registered OUTSIDE the ResolveWorkspace group on
// purpose: an invited person follows an emailed link *before* they have a
// session, so wrapping the route in the membership check would 404 every
// invitation link. The GET is fully public (Django's AllowAny); the POST
// requires a session but resolves the workspace itself, without a membership
// check, so the accepting user can join a workspace they are not yet in.

// Invitations serves GET and POST /api/workspaces/{slug}/invitations/.
func (h *Handler) Invitations(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	if e.Request.Method == http.MethodPost {
		return h.createInvitations(e, scope, user)
	}

	perPage, cursor := serializers.ListParams(e, serializers.MaxPerPage)
	envelope, err := serializers.Paginate(h.App, serializers.ListQuery{
		Collection: "workspace_member_invites",
		Filter:     "workspace = {:workspace} && deleted_at = ''",
		Params:     dbx.Params{"workspace": scope.Workspace.Id},
		Sort:       "-created_at",
		PerPage:    perPage,
		Cursor:     cursor,
	}, func(r *core.Record) any {
		return workspaceMemberInviteJSON(h.App, r)
	})
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, envelope)
}

// createInvitations handles POST /api/workspaces/{slug}/invitations/
// (WorkspaceInvitationsViewset.create).
func (h *Handler) createInvitations(e *core.RequestEvent, scope *middleware.Scope, user *core.Record) error {
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	emails := emailEntries(body["emails"])
	if len(emails) == 0 {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Emails are required"})
	}

	// A requester may not invite anyone with a role above their own.
	for _, entry := range emails {
		if entry.role > scope.WorkspaceRole {
			return e.JSON(http.StatusBadRequest, map[string]any{"error": "You cannot invite a user with higher role"})
		}
	}

	// Reject the whole batch if any invitee is already an active member.
	existing := make([]*core.Record, 0)
	for _, entry := range emails {
		members, _ := h.App.FindRecordsByFilter(
			"workspace_members",
			"workspace = {:workspace} && is_active = true && deleted_at = '' && member.email = {:email}",
			"", 0, 0,
			dbx.Params{"workspace": scope.Workspace.Id, "email": entry.email},
		)
		existing = append(existing, members...)
	}
	if len(existing) > 0 {
		users := make([]any, 0, len(existing))
		for _, m := range existing {
			users = append(users, serializers.WorkspaceMember(h.App, m, false))
		}
		return e.JSON(http.StatusBadRequest, map[string]any{
			"error":           "Some users are already member of workspace",
			"workspace_users": users,
		})
	}

	collection, err := h.App.FindCollectionByNameOrId("workspace_member_invites")
	if err != nil {
		return err
	}
	for _, entry := range emails {
		if !validEmail(entry.email) {
			return e.JSON(http.StatusBadRequest, map[string]any{
				"error": "Invalid email - " + entry.email + " provided a valid email address is required to send the invite",
			})
		}
		// ignore_conflicts: the (email, workspace) partial unique index means a
		// re-invite is silently skipped, matching Django's bulk_create.
		if dup, _ := h.App.FindFirstRecordByFilter(
			"workspace_member_invites",
			"workspace = {:workspace} && email = {:email} && deleted_at = ''",
			dbx.Params{"workspace": scope.Workspace.Id, "email": entry.email},
		); dup != nil {
			continue
		}
		rec := core.NewRecord(collection)
		rec.Set("uuid", serializers.NewUUID())
		rec.Set("workspace", scope.Workspace.Id)
		rec.Set("email", entry.email)
		rec.Set("token", serializers.NewUUID()+serializers.NewUUID())
		rec.Set("role", entry.role)
		rec.Set("created_by", user.Id)
		rec.Set("updated_by", user.Id)
		if err := h.App.Save(rec); err != nil {
			return serializers.Validation(e, "email", err.Error())
		}
	}

	return e.JSON(http.StatusOK, map[string]any{"message": "Emails sent successfully"})
}

// InvitationJoin serves GET /api/workspaces/{slug}/invitations/{ik}/join/
// (WorkspaceJoinEndpoint.get) — public, so the invitation landing page can
// render before the recipient signs in. Uses the public serializer that omits
// `token`/`invite_link`.
func (h *Handler) InvitationJoin(e *core.RequestEvent) error {
	workspace := h.workspaceBySlug(e.Request.PathValue("slug"))
	if workspace == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	invite, _ := serializers.FindByUUID(h.App, "workspace_member_invites", e.Request.PathValue("ik"))
	if invite == nil || invite.GetString("workspace") != workspace.Id || invite.GetString("deleted_at") != "" {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	if e.Request.Method == http.MethodPost {
		return h.acceptInvitation(e, workspace, invite)
	}
	return e.JSON(http.StatusOK, workspaceMemberInvitePublicJSON(h.App, invite))
}

// acceptInvitation handles POST /api/workspaces/{slug}/invitations/{ik}/join/
// (WorkspaceJoinEndpoint.post). The token must match and the caller must be
// authenticated as the invited address — the accepting user may not yet be a
// member of the workspace, so no membership check is performed here.
func (h *Handler) acceptInvitation(e *core.RequestEvent, workspace, invite *core.Record) error {
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}

	token := asString(body["token"])
	if token == "" || token != invite.GetString("token") {
		return e.JSON(http.StatusForbidden, map[string]any{"error": "You do not have permission to join the workspace"})
	}

	user := middleware.CurrentUser(e)
	if user == nil {
		return e.JSON(http.StatusUnauthorized, map[string]any{"error": "Authentication required to accept workspace invitation"})
	}
	if !strings.EqualFold(user.GetString("email"), invite.GetString("email")) {
		return e.JSON(http.StatusForbidden, map[string]any{"error": "You do not have permission to accept this invitation"})
	}

	if !invite.GetDateTime("responded_at").IsZero() {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "You have already responded to the invitation request"})
	}

	accepted, _ := body["accepted"].(bool)
	invite.Set("accepted", accepted)
	invite.Set("responded_at", serializers.Now())
	invite.Set("updated_by", user.Id)
	if err := h.App.Save(invite); err != nil {
		return err
	}

	if !accepted {
		return e.JSON(http.StatusOK, map[string]any{"message": "Workspace Invitation was not accepted"})
	}

	membersColl, err := h.App.FindCollectionByNameOrId("workspace_members")
	if err != nil {
		return err
	}
	membership, _ := h.App.FindFirstRecordByFilter(
		"workspace_members",
		"workspace = {:workspace} && member = {:member}",
		dbx.Params{"workspace": workspace.Id, "member": user.Id},
	)
	if membership != nil {
		membership.Set("is_active", true)
		membership.Set("role", invite.GetInt("role"))
		if err := h.App.Save(membership); err != nil {
			return err
		}
	} else {
		membership = core.NewRecord(membersColl)
		membership.Set("uuid", serializers.NewUUID())
		membership.Set("workspace", workspace.Id)
		membership.Set("member", user.Id)
		membership.Set("role", invite.GetInt("role"))
		membership.Set("is_active", true)
		membership.Set("created_by", user.Id)
		if err := h.App.Save(membership); err != nil {
			return err
		}
	}

	// Point the user's "last workspace" at the one they just joined.
	if profile, _ := h.App.FindFirstRecordByFilter("profiles", "user = {:user}", dbx.Params{"user": user.Id}); profile != nil {
		profile.Set("last_workspace_id", serializers.UUID(workspace))
		_ = h.App.Save(profile)
	}

	// Accepted invites are consumed, as in Django.
	invite.Set("deleted_at", serializers.Now())
	if err := h.App.Save(invite); err != nil {
		return err
	}

	return e.JSON(http.StatusOK, map[string]any{"message": "Workspace Invitation Accepted"})
}

// InvitationDetail serves PATCH and DELETE
// /api/workspaces/{slug}/invitations/{ik}/.
func (h *Handler) InvitationDetail(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	invite, _ := serializers.FindByUUID(h.App, "workspace_member_invites", e.Request.PathValue("ik"))
	if invite == nil || invite.GetString("workspace") != scope.Workspace.Id || invite.GetString("deleted_at") != "" {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "The required object does not exist."})
	}

	if e.Request.Method == http.MethodDelete {
		invite.Set("deleted_at", serializers.Now())
		invite.Set("updated_by", user.Id)
		if err := h.App.Save(invite); err != nil {
			return err
		}
		return e.NoContent(http.StatusNoContent)
	}

	// partial_update: the serializer's writable fields are `role` and
	// `accepted` (everything else is read-only).
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	if v, ok := body["role"]; ok {
		if n, ok := numericInt(v); ok {
			invite.Set("role", n)
		}
	}
	if v, ok := body["accepted"]; ok {
		if b, ok := v.(bool); ok {
			invite.Set("accepted", b)
		}
	}
	invite.Set("updated_by", user.Id)
	if err := h.App.Save(invite); err != nil {
		return serializers.Validation(e, "role", err.Error())
	}
	return e.JSON(http.StatusOK, workspaceMemberInviteJSON(h.App, invite))
}

// LeaveWorkspace serves POST /api/workspaces/{slug}/members/leave/
// (WorkSpaceMemberViewSet.leave).
func (h *Handler) LeaveWorkspace(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil || scope.WorkspaceMember == nil {
		return serializers.NotFound(e, "")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	wsID := scope.Workspace.Id

	if scope.WorkspaceRole == middleware.RoleAdmin {
		admins, _ := h.App.FindRecordsByFilter(
			"workspace_members",
			"workspace = {:workspace} && role = 20 && is_active = true && deleted_at = ''",
			"", 0, 0, dbx.Params{"workspace": wsID},
		)
		if len(admins) <= 1 {
			return e.JSON(http.StatusBadRequest, map[string]any{
				"error": "You cannot leave the workspace as you are the only admin of the workspace you will have to either delete the workspace or promote another user to admin.",
			})
		}
	}

	if h.isSoleProjectAdmin(wsID, user.Id) {
		return e.JSON(http.StatusBadRequest, map[string]any{
			"error": "You are a part of some projects where you are the only admin, you should either leave the project or promote another user to admin.",
		})
	}

	if err := h.deactivateProjectMemberships(wsID, user.Id); err != nil {
		return err
	}

	member := scope.WorkspaceMember
	member.Set("is_active", false)
	member.Set("updated_by", user.Id)
	if err := h.App.Save(member); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// LeaveProject serves POST /api/workspaces/{slug}/projects/{pid}/members/leave/
// (ProjectMemberViewSet.leave).
func (h *Handler) LeaveProject(e *core.RequestEvent) error {
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil || scope.Project == nil || scope.ProjectMember == nil {
		return serializers.NotFound(e, "The required object does not exist.")
	}
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}

	if scope.ProjectMember.GetInt("role") == middleware.RoleAdmin {
		admins, _ := h.App.FindRecordsByFilter(
			"project_members",
			"workspace = {:workspace} && project = {:project} && role = 20 && is_active = true && deleted_at = ''",
			"", 0, 0,
			dbx.Params{"workspace": scope.Workspace.Id, "project": scope.Project.Id},
		)
		if len(admins) <= 1 {
			return e.JSON(http.StatusBadRequest, map[string]any{
				"error": "You cannot leave the project as your the only admin of the project you will have to either delete the project or create an another admin",
			})
		}
	}

	member := scope.ProjectMember
	member.Set("is_active", false)
	member.Set("updated_by", user.Id)
	if err := h.App.Save(member); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}

// isSoleProjectAdmin reports whether the user is the *only* member of some
// project in the workspace and holds admin there — Django's
// `Count(total_members=1, member_with_role=1)` annotation. Equivalent to: a
// project whose single project_members row is (this user, role 20).
func (h *Handler) isSoleProjectAdmin(workspaceID, userID string) bool {
	rows, err := h.App.FindRecordsByFilter(
		"project_members",
		"workspace = {:workspace}",
		"", 0, 0, dbx.Params{"workspace": workspaceID},
	)
	if err != nil {
		return false
	}
	byProject := map[string][]*core.Record{}
	for _, row := range rows {
		pid := row.GetString("project")
		byProject[pid] = append(byProject[pid], row)
	}
	for _, members := range byProject {
		if len(members) == 1 && members[0].GetString("member") == userID && members[0].GetInt("role") == middleware.RoleAdmin {
			return true
		}
	}
	return false
}

// deactivateProjectMemberships deactivates every active project membership the
// user holds in the workspace (WorkSpaceMemberViewSet.leave).
func (h *Handler) deactivateProjectMemberships(workspaceID, userID string) error {
	rows, err := h.App.FindRecordsByFilter(
		"project_members",
		"workspace = {:workspace} && member = {:member} && is_active = true",
		"", 0, 0,
		dbx.Params{"workspace": workspaceID, "member": userID},
	)
	if err != nil {
		return err
	}
	for _, row := range rows {
		row.Set("is_active", false)
		row.Set("updated_by", userID)
		if err := h.App.Save(row); err != nil {
			return err
		}
	}
	return nil
}

// workspaceBySlug resolves a non-deleted workspace without a membership check
// (the join endpoints must work for a not-yet-member).
func (h *Handler) workspaceBySlug(slug string) *core.Record {
	if slug == "" {
		return nil
	}
	ws, err := h.App.FindFirstRecordByFilter(
		"workspaces",
		"slug = {:slug} && deleted_at = ''",
		dbx.Params{"slug": slug},
	)
	if err != nil {
		return nil
	}
	return ws
}

// workspaceMemberInviteJSON mirrors Django's WorkSpaceMemberInviteSerializer:
// `fields = "__all__"` over WorkspaceMemberInvite, plus a nested workspace
// (WorkspaceLiteSerializer) and the computed `invite_link`.
func workspaceMemberInviteJSON(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	workspace, _ := app.FindRecordById("workspaces", r.GetString("workspace"))
	id := serializers.UUID(r)
	token := serializers.Text(r, "token")
	slug := ""
	if workspace != nil {
		slug = workspace.GetString("slug")
	}
	return map[string]any{
		"id":           id,
		"workspace":    serializers.WorkspaceLite(workspace),
		"email":        serializers.Text(r, "email"),
		"accepted":     r.GetBool("accepted"),
		"token":        token,
		"message":      serializers.NullableText(r, "message"),
		"responded_at": serializers.NullableDateTime(r, "responded_at"),
		"role":         r.GetInt("role"),
		"created_at":   serializers.DateTime(r, "created_at"),
		"updated_at":   serializers.DateTime(r, "updated_at"),
		"created_by":   serializers.RelationUUID(app, r, "created_by"),
		"updated_by":   serializers.RelationUUID(app, r, "updated_by"),
		"deleted_at":   serializers.NullableDateTime(r, "deleted_at"),
		"invite_link":  "/workspace-invitations/?invitation_id=" + id + "&slug=" + slug + "&token=" + token,
	}
}

// workspaceMemberInvitePublicJSON mirrors Django's
// WorkSpaceMemberInvitePublicSerializer, which deliberately omits `token` and
// `invite_link` (GHSA-86mg-259g-pwgg).
func workspaceMemberInvitePublicJSON(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	workspace, _ := app.FindRecordById("workspaces", r.GetString("workspace"))
	return map[string]any{
		"id":           serializers.UUID(r),
		"workspace":    serializers.WorkspaceLite(workspace),
		"email":        serializers.Text(r, "email"),
		"accepted":     r.GetBool("accepted"),
		"message":      serializers.NullableText(r, "message"),
		"responded_at": serializers.NullableDateTime(r, "responded_at"),
		"role":         r.GetInt("role"),
		"created_at":   serializers.DateTime(r, "created_at"),
		"updated_at":   serializers.DateTime(r, "updated_at"),
		"created_by":   serializers.RelationUUID(app, r, "created_by"),
	}
}

// emailEntry is one `{email, role}` pair of the invite payload.
type emailEntry struct {
	email string
	role  int
}

// emailEntries normalises the `emails` array. An absent/omitted role defaults
// to 5 (Guest), matching Django's `email.get("role", 5)`.
func emailEntries(raw any) []emailEntry {
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]emailEntry, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		email := strings.ToLower(strings.TrimSpace(asString(m["email"])))
		if email == "" {
			continue
		}
		role := 5
		if n, ok := numericInt(m["role"]); ok {
			role = n
		}
		out = append(out, emailEntry{email: email, role: role})
	}
	return out
}

// numericInt coerces a JSON number (float64) or a numeric string to int.
func numericInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

// validEmail is a deliberately small format check (local@domain.tld), matching
// the spirit of Django's validate_email without pulling in a full RFC parser.
func validEmail(email string) bool {
	at := strings.IndexByte(email, '@')
	if at <= 0 || at == len(email)-1 {
		return false
	}
	domain := email[at+1:]
	if strings.ContainsAny(email, " \t\r\n") || !strings.Contains(domain, ".") {
		return false
	}
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return false
	}
	return true
}
