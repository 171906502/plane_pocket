package users

import (
	"crypto/rand"
	"log"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/profiles"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// This file implements the remaining /api/users/… identity endpoints Plane's
// web app calls. Django sources:
//
//	GET   /api/users/me/accounts/                      user/base.py AccountEndpoint.get
//	GET   /api/users/me/instance-admin/                user/base.py UserEndpoint.retrieve_instance_admin
//	POST  /api/users/me/email/generate-code/           user/base.py UserEndpoint.generate_email_verification_code
//	PATCH /api/users/me/email/                          user/base.py UserEndpoint.update_email
//	GET   /api/users/last-visited-workspace/           workspace/user.py UserLastProjectWithWorkspaceEndpoint
//	GET   /api/users/me/workspaces/invitations/        workspace/invite.py UserWorkspaceInvitationsViewSet.list
//	POST  /api/users/me/workspaces/invitations/        workspace/invite.py UserWorkspaceInvitationsViewSet.create
//	POST  /api/users/me/workspaces/{slug}/projects/invitations/
//	                                                   project/invite.py UserProjectInvitationsViewset.create

// emailCodeTTL mirrors Plane's Redis magic-code expiry (600s) for the
// email-change verification code.
const emailCodeTTL = 600 * time.Second

// emailCodeEntry is the stored email-change verification code plus its expiry.
type emailCodeEntry struct {
	Code    string
	Expires time.Time
}

// Accounts serves GET /api/users/me/accounts/ (AccountEndpoint.get): a plain,
// non-paginated array of the caller's linked OAuth accounts.
func (h *Handler) Accounts(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	records, err := h.App.FindRecordsByFilter(
		"accounts",
		"user = {:user}",
		"-created_at", 0, 0,
		dbx.Params{"user": user.Id},
	)
	if err != nil {
		return err
	}
	out := make([]any, 0, len(records))
	for _, r := range records {
		out = append(out, accountJSON(h.App, r))
	}
	return e.JSON(http.StatusOK, out)
}

// InstanceAdmin serves GET /api/users/me/instance-admin/
// (UserEndpoint.retrieve_instance_admin).
func (h *Handler) InstanceAdmin(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	admin, _ := h.App.FindFirstRecordByFilter("instance_admins", "user = {:user}", dbx.Params{"user": user.Id})
	return e.JSON(http.StatusOK, map[string]any{"is_instance_admin": admin != nil})
}

// GenerateEmailCode serves POST /api/users/me/email/generate-code/
// (UserEndpoint.generate_email_verification_code). Email delivery is out of
// scope for this slice, so the code is stored in the app runtime store and
// logged instead of being sent — the same approach as auth.MagicGenerate.
func (h *Handler) GenerateEmailCode(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	newEmail := strings.ToLower(strings.TrimSpace(asString(body["email"])))
	if msg, ok := h.validateNewEmail(user, newEmail); !ok {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": msg})
	}

	code := sixDigitCode()
	h.App.Store().Set(emailCodeKey(user.Id, newEmail), emailCodeEntry{Code: code, Expires: time.Now().Add(emailCodeTTL)})
	log.Printf("[plane] email-change verification code for %s: %s", newEmail, code)

	return e.JSON(http.StatusOK, map[string]any{"message": "Verification code sent to email"})
}

// UpdateEmail serves PATCH /api/users/me/email/ (UserEndpoint.update_email):
// verify the code, then move the existing user record to the new address
// without changing its id. On success the session is flushed (Django calls
// logout) and the updated user is returned.
func (h *Handler) UpdateEmail(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	newEmail := strings.ToLower(strings.TrimSpace(asString(body["email"])))
	code := strings.TrimSpace(asString(body["code"]))

	if msg, ok := h.validateNewEmail(user, newEmail); !ok {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": msg})
	}
	if code == "" {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Verification code is required"})
	}

	key := emailCodeKey(user.Id, newEmail)
	entry, ok := h.App.Store().GetOk(key)
	if !ok {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Verification code has expired or is invalid"})
	}
	stored, _ := entry.(emailCodeEntry)
	if time.Now().After(stored.Expires) {
		h.App.Store().Remove(key)
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Verification code has expired or is invalid"})
	}
	if stored.Code != code {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "Invalid verification code"})
	}
	h.App.Store().Remove(key)

	user.Set("email", newEmail)
	user.Set("is_email_verified", false)
	if err := h.App.Save(user); err != nil {
		return e.JSON(http.StatusBadRequest, map[string]any{"error": "An account with this email already exists"})
	}

	// Django logs the user out after an email change.
	middleware.ClearSessionCookie(e, middleware.SessionCookieName)
	return e.JSON(http.StatusOK, serializers.UserMe(h.App, user))
}

// validateNewEmail mirrors UserEndpoint._validate_new_email. It returns the
// error message and false when the address is unusable.
func (h *Handler) validateNewEmail(user *core.Record, newEmail string) (string, bool) {
	if newEmail == "" {
		return "Email is required", false
	}
	if !validEmail(newEmail) {
		return "Invalid email format", false
	}
	if strings.EqualFold(newEmail, user.GetString("email")) {
		return "New email must be different from current email", false
	}
	if existing, _ := h.App.FindFirstRecordByFilter(
		"users", "email = {:email} && id != {:id}",
		dbx.Params{"email": newEmail, "id": user.Id},
	); existing != nil {
		return "An account with this email already exists", false
	}
	return "", true
}

// LastVisitedWorkspace serves GET /api/users/last-visited-workspace/
// (UserLastProjectWithWorkspaceEndpoint). It reads Profile.last_workspace_id
// and returns the workspace plus the caller's project memberships in it (all
// of them — Django does not filter on is_active here).
func (h *Handler) LastVisitedWorkspace(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	profile, err := profiles.Ensure(h.App, user)
	if err != nil {
		return err
	}

	wsUUID := profile.GetString("last_workspace_id")
	workspace, _ := serializers.FindByUUID(h.App, "workspaces", wsUUID)
	if workspace == nil || workspace.GetString("deleted_at") != "" {
		return e.JSON(http.StatusOK, map[string]any{
			"project_details":   []any{},
			"workspace_details": map[string]any{},
		})
	}

	role := 0
	if membership, _ := h.App.FindFirstRecordByFilter(
		"workspace_members",
		"workspace = {:workspace} && member = {:member} && is_active = true",
		dbx.Params{"workspace": workspace.Id, "member": user.Id},
	); membership != nil {
		role = membership.GetInt("role")
	}

	memberships, err := h.App.FindRecordsByFilter(
		"project_members",
		"workspace = {:workspace} && member = {:member}",
		"-created_at", 0, 0,
		dbx.Params{"workspace": workspace.Id, "member": user.Id},
	)
	if err != nil {
		return err
	}
	projects := make([]any, 0, len(memberships))
	for _, m := range memberships {
		projects = append(projects, serializers.ProjectMember(h.App, m))
	}

	return e.JSON(http.StatusOK, map[string]any{
		"workspace_details": serializers.Workspace(h.App, workspace, role, h.countActiveMembers(workspace.Id)),
		"project_details":   projects,
	})
}

// MyWorkspaceInvitations serves GET /api/users/me/workspaces/invitations/
// (UserWorkspaceInvitationsViewSet.list): a paginated envelope of the
// invitations addressed to the caller's email.
func (h *Handler) MyWorkspaceInvitations(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	perPage, cursor := serializers.ListParams(e, serializers.MaxPerPage)
	envelope, err := serializers.Paginate(h.App, serializers.ListQuery{
		Collection: "workspace_member_invites",
		Filter:     "email = {:email} && deleted_at = ''",
		Params:     dbx.Params{"email": user.GetString("email")},
		Sort:       "-created_at",
		PerPage:    perPage,
		Cursor:     cursor,
	}, func(r *core.Record) any {
		return workspaceInviteJSON(h.App, r)
	})
	if err != nil {
		return err
	}
	return e.JSON(http.StatusOK, envelope)
}

// JoinWorkspaces serves POST /api/users/me/workspaces/invitations/
// (UserWorkspaceInvitationsViewSet.create): accept a batch of invitations by
// id, activating or creating the matching workspace memberships, then consume
// the invitations. Returns 204.
func (h *Handler) JoinWorkspaces(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	ids := stringList(body["invitations"])

	membersColl, err := h.App.FindCollectionByNameOrId("workspace_members")
	if err != nil {
		return err
	}

	for _, id := range ids {
		invite, _ := serializers.FindByUUID(h.App, "workspace_member_invites", id)
		if invite == nil || invite.GetString("deleted_at") != "" {
			continue
		}
		if !strings.EqualFold(invite.GetString("email"), user.GetString("email")) {
			continue
		}
		wsID := invite.GetString("workspace")
		role := invite.GetInt("role")

		membership, _ := h.App.FindFirstRecordByFilter(
			"workspace_members",
			"workspace = {:workspace} && member = {:member}",
			dbx.Params{"workspace": wsID, "member": user.Id},
		)
		if membership != nil {
			membership.Set("is_active", true)
			membership.Set("role", role)
			if err := h.App.Save(membership); err != nil {
				return err
			}
		} else {
			membership = core.NewRecord(membersColl)
			membership.Set("uuid", serializers.NewUUID())
			membership.Set("workspace", wsID)
			membership.Set("member", user.Id)
			membership.Set("role", role)
			membership.Set("is_active", true)
			membership.Set("created_by", user.Id)
			if err := h.App.Save(membership); err != nil {
				return err
			}
		}

		invite.Set("deleted_at", serializers.Now())
		if err := h.App.Save(invite); err != nil {
			return err
		}
	}
	return e.NoContent(http.StatusNoContent)
}

// ProjectInvitations serves POST /api/users/me/workspaces/{slug}/projects/invitations/
// (UserProjectInvitationsViewset.create): the caller joins a batch of projects
// in a workspace they are already a member of.
func (h *Handler) ProjectInvitations(e *core.RequestEvent) error {
	user := middleware.CurrentUser(e)
	if user == nil {
		return serializers.Unauthorized(e, "Authentication credentials were not provided.")
	}
	scope := middleware.ScopeOf(e)
	if scope == nil || scope.Workspace == nil {
		return serializers.NotFound(e, "The requested workspace could not be found.")
	}
	// allow_permission([ROLE.ADMIN, ROLE.MEMBER], level="WORKSPACE").
	if scope.WorkspaceRole < middleware.RoleMember {
		return serializers.Forbidden(e, "You do not have permission to perform this action.")
	}

	body := map[string]any{}
	if err := e.BindBody(&body); err != nil {
		return serializers.BadRequest(e, "Invalid request body.")
	}
	projectUUIDs := stringList(body["project_ids"])

	// Resolve the requested ids scoped to this workspace: an id from another
	// workspace must not bypass the SECRET-network check below.
	validated := make([]*core.Record, 0, len(projectUUIDs))
	for _, uuid := range projectUUIDs {
		project, _ := serializers.FindByUUID(h.App, "projects", uuid)
		if project == nil || project.GetString("workspace") != scope.Workspace.Id || project.GetString("deleted_at") != "" {
			continue
		}
		if isSecretProject(project) && scope.WorkspaceRole != middleware.RoleAdmin {
			return serializers.Forbidden(e, "Only workspace admins can join private project")
		}
		validated = append(validated, project)
	}

	membersColl, err := h.App.FindCollectionByNameOrId("project_members")
	if err != nil {
		return err
	}
	propsColl, err := h.App.FindCollectionByNameOrId("project_user_properties")
	if err != nil {
		return err
	}

	for _, project := range validated {
		member, _ := h.App.FindFirstRecordByFilter(
			"project_members",
			"project = {:project} && member = {:member}",
			dbx.Params{"project": project.Id, "member": user.Id},
		)
		if member != nil {
			member.Set("is_active", true)
			if err := h.App.Save(member); err != nil {
				return err
			}
		} else {
			member = core.NewRecord(membersColl)
			member.Set("uuid", serializers.NewUUID())
			member.Set("project", project.Id)
			member.Set("member", user.Id)
			member.Set("workspace", scope.Workspace.Id)
			member.Set("role", scope.WorkspaceRole)
			member.Set("is_active", true)
			member.Set("created_by", user.Id)
			if err := h.App.Save(member); err != nil {
				return err
			}
		}

		if existing, _ := h.App.FindFirstRecordByFilter(
			"project_user_properties",
			"project = {:project} && user = {:user}",
			dbx.Params{"project": project.Id, "user": user.Id},
		); existing == nil {
			prop := core.NewRecord(propsColl)
			prop.Set("uuid", serializers.NewUUID())
			prop.Set("project", project.Id)
			prop.Set("user", user.Id)
			prop.Set("workspace", scope.Workspace.Id)
			prop.Set("created_by", user.Id)
			if err := h.App.Save(prop); err != nil {
				return err
			}
		}
	}

	return e.JSON(http.StatusCreated, map[string]any{"message": "Projects joined successfully"})
}

// workspaceInviteJSON mirrors Django's WorkSpaceMemberInviteSerializer (full
// form, including token + invite_link). It is duplicated rather than shared
// with the workspaces package to keep the two domains independent.
func workspaceInviteJSON(app core.App, r *core.Record) map[string]any {
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

// accountJSON mirrors Django's AccountSerializer (`fields = "__all__"` over the
// Account model, with `user` read-only).
func accountJSON(app core.App, r *core.Record) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":                       serializers.UUID(r),
		"user":                     serializers.RelationUUID(app, r, "user"),
		"provider_account_id":      serializers.Text(r, "provider_account_id"),
		"provider":                 serializers.Text(r, "provider"),
		"access_token":             serializers.Text(r, "access_token"),
		"access_token_expired_at":  serializers.NullableDateTime(r, "access_token_expired_at"),
		"refresh_token":            serializers.NullableText(r, "refresh_token"),
		"refresh_token_expired_at": serializers.NullableDateTime(r, "refresh_token_expired_at"),
		"last_connected_at":        serializers.DateTime(r, "last_connected_at"),
		"id_token":                 serializers.Text(r, "id_token"),
		"metadata":                 serializers.JSONOrDefault(r, "metadata", map[string]any{}),
		"created_at":               serializers.DateTime(r, "created_at"),
		"updated_at":               serializers.DateTime(r, "updated_at"),
	}
}

// isSecretProject reports whether a project is private (network 0). An unset
// network column means "never set", which defaults to public (2), so it is
// only secret when explicitly 0.
func isSecretProject(project *core.Record) bool {
	return project.Get("network") != nil && project.GetInt("network") == 0
}

// emailCodeKey namespaces the stored verification code by user and address,
// matching Django's `magic_email_update_<user>_<email>` cache key.
func emailCodeKey(userID, email string) string {
	return "magic_email_update_" + userID + "_" + email
}

// stringList coerces a JSON array to a slice of strings.
func stringList(raw any) []string {
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// asString renders a JSON string value, or "" for anything else.
func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// sixDigitCode returns a random code in [100000, 999999], matching Plane's
// `secrets.randbelow(900000) + 100000`.
func sixDigitCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		return "100000"
	}
	return strconv.Itoa(int(n.Int64()) + 100000)
}

// validEmail is a deliberately small format check (local@domain.tld), matching
// the spirit of Django's validate_email.
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
