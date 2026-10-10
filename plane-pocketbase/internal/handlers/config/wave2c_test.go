package config_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/config"
	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/users"
	"github.com/plane-pocketbase/plane-pocketbase/internal/handlers/workspaces"
	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

// registerAgentC mirrors the exact Agent C route wiring from
// internal/router/{users,workspace,config}.go (method + path + middleware).
//
// It is reproduced here rather than calling router.Register so this test stays
// decoupled from the other agents' domains, which are edited concurrently in
// the shared registrar table: a transient compile/registration problem in a
// foreign domain would otherwise fail *this* test. The route strings are
// identical to the ones in the router files (which TestRoutePatternsAreAnchored
// guards for `{$}` anchoring); the full registrar table is exercised separately
// by booting the server.
func registerAgentC(e *core.ServeEvent, app core.App) {
	h := workspaces.New(app)
	u := users.New(app)
	c := config.New(app)

	e.Router.GET("/api/users/me/workspaces/{$}", u.MeWorkspaces)
	e.Router.GET("/api/users/me/accounts/{$}", u.Accounts)
	e.Router.GET("/api/users/me/instance-admin/{$}", u.InstanceAdmin)
	e.Router.POST("/api/users/me/email/generate-code/{$}", u.GenerateEmailCode)
	e.Router.PATCH("/api/users/me/email/{$}", u.UpdateEmail)
	e.Router.GET("/api/users/me/workspaces/invitations/{$}", u.MyWorkspaceInvitations)
	e.Router.POST("/api/users/me/workspaces/invitations/{$}", u.JoinWorkspaces)
	e.Router.GET("/api/users/last-visited-workspace/{$}", u.LastVisitedWorkspace)

	meWorkspace := e.Router.Group("/api/users/me/workspaces/{slug}").
		BindFunc(middleware.ResolveWorkspace(app))
	meWorkspace.POST("/projects/invitations/{$}", u.ProjectInvitations)

	ws := e.Router.Group("/api/workspaces/{slug}").BindFunc(middleware.ResolveWorkspace(app))
	ws.GET("/invitations/{$}", h.Invitations).BindFunc(middleware.RequireWorkspaceRole(middleware.RoleMember))
	ws.POST("/invitations/{$}", h.Invitations).BindFunc(middleware.RequireWorkspaceRole(middleware.RoleMember))
	ws.PATCH("/invitations/{ik}/{$}", h.InvitationDetail).BindFunc(middleware.RequireWorkspaceRole(middleware.RoleMember))
	ws.DELETE("/invitations/{ik}/{$}", h.InvitationDetail).BindFunc(middleware.RequireWorkspaceRole(middleware.RoleMember))
	ws.POST("/members/leave/{$}", h.LeaveWorkspace)

	e.Router.GET("/api/workspaces/{slug}/invitations/{ik}/join/{$}", h.InvitationJoin)
	e.Router.POST("/api/workspaces/{slug}/invitations/{ik}/join/{$}", h.InvitationJoin)

	proj := ws.Group("/projects/{project_id}").BindFunc(middleware.ResolveProject(app))
	proj.POST("/members/leave/{$}", h.LeaveProject)

	e.Router.GET("/api/configs/{$}", c.Configs)
	e.Router.GET("/api/release-notes/{$}", c.ReleaseNotes)
}

// TestAgentCWave2 drives every Agent C (user/workspace/config) endpoint through
// the real handlers over a real ServeMux and asserts CONTENT, not just status
// codes. The target tables are empty in a fresh install, so data is created
// first (by direct seeding and by exercising the POST endpoints) and then read
// back — a 200 with an empty array would fail every assertion below.
func TestAgentCWave2(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	defer app.Cleanup()

	owner := saveUser(t, app, "00000000-0000-0000-0000-0000000000a1", "owner@example.com")
	invitee := saveUser(t, app, "00000000-0000-0000-0000-0000000000a2", "invitee@example.com")
	newbie := saveUser(t, app, "00000000-0000-0000-0000-0000000000a3", "newbie@example.com")
	joiner := saveUser(t, app, "00000000-0000-0000-0000-0000000000a4", "joiner@example.com")

	ws := save(t, app, "workspaces", func(r *core.Record) {
		r.Set("name", "HRMS")
		r.Set("slug", "hrms")
		r.Set("owner", owner.Id)
	})
	mustSaveMembership(t, app, ws.Id, owner.Id, 20)
	mustSaveMembership(t, app, ws.Id, joiner.Id, 15)

	project := save(t, app, "projects", func(r *core.Record) {
		r.Set("name", "Proj")
		r.Set("identifier", "P")
		r.Set("workspace", ws.Id)
		r.Set("network", 2)
	})
	save(t, app, "project_members", func(r *core.Record) {
		r.Set("project", project.Id)
		r.Set("member", owner.Id)
		r.Set("workspace", ws.Id)
		r.Set("role", 20)
		r.Set("is_active", true)
	})

	save(t, app, "profiles", func(r *core.Record) {
		r.Set("user", owner.Id)
		r.Set("last_workspace_id", ws.GetString("uuid"))
	})
	save(t, app, "accounts", func(r *core.Record) {
		r.Set("user", owner.Id)
		r.Set("provider", "google")
		r.Set("provider_account_id", "google-123")
		r.Set("access_token", "at")
		r.Set("last_connected_at", serializers.Now())
	})
	save(t, app, "changelogs", func(r *core.Record) {
		r.Set("title", "Wave 2 release")
		r.Set("version", "1.4.2")
		r.Set("description", "notes")
	})

	inviteA := save(t, app, "workspace_member_invites", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-00000000b001")
		r.Set("workspace", ws.Id)
		r.Set("email", "invitee@example.com")
		r.Set("token", "tok-abc")
		r.Set("role", 15)
	})
	save(t, app, "workspace_member_invites", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-00000000b002")
		r.Set("workspace", ws.Id)
		r.Set("email", "newbie@example.com")
		r.Set("token", "tok-def")
		r.Set("role", 15)
	})
	inviteC := save(t, app, "workspace_member_invites", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-00000000b003")
		r.Set("workspace", ws.Id)
		r.Set("email", "patchme@example.com")
		r.Set("token", "tok-ghi")
		r.Set("role", 5)
	})

	// Build the router directly (no OnServe trigger) so PocketBase's bundled UI
	// extension routes are never registered — see registerAgentC's note.
	baseRouter, err := apis.NewRouter(app)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	serve := &core.ServeEvent{App: app, Router: baseRouter}
	registerAgentC(serve, app)
	mux, err := baseRouter.BuildMux()
	if err != nil {
		t.Fatalf("build mux: %v", err)
	}

	ownerToken := authToken(t, owner)
	inviteeToken := authToken(t, invitee)
	newbieToken := authToken(t, newbie)
	joinerToken := authToken(t, joiner)

	run(t, mux, tc{
		name: "accounts list is non-empty", method: http.MethodGet, url: "/api/users/me/accounts/",
		token: ownerToken, wantStatus: http.StatusOK,
		want: []string{`"provider":"google"`, `"provider_account_id":"google-123"`},
	})
	run(t, mux, tc{
		name: "instance-admin status", method: http.MethodGet, url: "/api/users/me/instance-admin/",
		token: ownerToken, wantStatus: http.StatusOK, want: []string{`"is_instance_admin":false`},
	})
	run(t, mux, tc{
		name: "email generate-code", method: http.MethodPost, url: "/api/users/me/email/generate-code/",
		token: ownerToken, body: `{"email":"owner-new@example.com"}`, wantStatus: http.StatusOK,
		want: []string{`"message":"Verification code sent to email"`},
	})
	if _, ok := app.Store().GetOk("magic_email_update_" + owner.Id + "_owner-new@example.com"); !ok {
		t.Errorf("expected the verification code to be stored")
	}
	run(t, mux, tc{
		name: "email update rejects bad code", method: http.MethodPatch, url: "/api/users/me/email/",
		token: ownerToken, body: `{"email":"owner-new@example.com","code":"1"}`, wantStatus: http.StatusBadRequest,
		want: []string{`"error":"Invalid verification code"`},
	})
	run(t, mux, tc{
		name: "last-visited-workspace", method: http.MethodGet, url: "/api/users/last-visited-workspace/",
		token: ownerToken, wantStatus: http.StatusOK,
		want: []string{`"workspace_details"`, `"slug":"hrms"`, `"project_details"`, `"identifier":"P"`},
	})
	run(t, mux, tc{
		name: "workspace invitations list is non-empty", method: http.MethodGet, url: "/api/workspaces/hrms/invitations/",
		token: ownerToken, wantStatus: http.StatusOK,
		want: []string{`"total_count":3`, `"invitee@example.com"`, `"invite_link"`},
	})
	run(t, mux, tc{
		name: "invitation join GET is public and omits the token", method: http.MethodGet,
		url:        "/api/workspaces/hrms/invitations/" + inviteA.GetString("uuid") + "/join/",
		wantStatus: http.StatusOK, want: []string{`"invitee@example.com"`, `"role":15`}, notWant: []string{`"token"`},
	})
	run(t, mux, tc{
		name: "invitation join POST accepts", method: http.MethodPost,
		url:   "/api/workspaces/hrms/invitations/" + inviteA.GetString("uuid") + "/join/",
		token: inviteeToken, body: `{"token":"tok-abc","accepted":true}`, wantStatus: http.StatusOK,
		want: []string{`"message":"Workspace Invitation Accepted"`},
	})
	run(t, mux, tc{
		name: "invitee now sees the workspace", method: http.MethodGet, url: "/api/users/me/workspaces/",
		token: inviteeToken, wantStatus: http.StatusOK, want: []string{`"slug":"hrms"`},
	})
	run(t, mux, tc{
		name: "workspace invitation create rejects an existing member", method: http.MethodPost,
		url: "/api/workspaces/hrms/invitations/", token: ownerToken,
		body: `{"emails":[{"email":"owner@example.com","role":15}]}`, wantStatus: http.StatusBadRequest,
		want: []string{`"Some users are already member of workspace"`},
	})
	run(t, mux, tc{
		name: "workspace invitation create", method: http.MethodPost, url: "/api/workspaces/hrms/invitations/",
		token: ownerToken, body: `{"emails":[{"email":"extra@example.com","role":15}]}`, wantStatus: http.StatusOK,
		want: []string{`"message":"Emails sent successfully"`},
	})
	run(t, mux, tc{
		name: "invitation patch role", method: http.MethodPatch,
		url:   "/api/workspaces/hrms/invitations/" + inviteC.GetString("uuid") + "/",
		token: ownerToken, body: `{"role":20}`, wantStatus: http.StatusOK,
		want: []string{`"role":20`, `"patchme@example.com"`},
	})
	run(t, mux, tc{
		name: "invitation delete", method: http.MethodDelete,
		url:   "/api/workspaces/hrms/invitations/" + inviteC.GetString("uuid") + "/",
		token: ownerToken, wantStatus: http.StatusNoContent,
	})
	run(t, mux, tc{
		name: "my workspace invitations lists the invite", method: http.MethodGet,
		url: "/api/users/me/workspaces/invitations/", token: newbieToken, wantStatus: http.StatusOK,
		want: []string{`"newbie@example.com"`, `"total_count":1`},
	})
	run(t, mux, tc{
		name: "join workspaces accepts the batch", method: http.MethodPost,
		url: "/api/users/me/workspaces/invitations/", token: newbieToken,
		body: `{"invitations":["00000000-0000-0000-0000-00000000b002"]}`, wantStatus: http.StatusNoContent,
	})
	run(t, mux, tc{
		name: "newbie is now a member", method: http.MethodGet, url: "/api/users/me/workspaces/",
		token: newbieToken, wantStatus: http.StatusOK, want: []string{`"slug":"hrms"`},
	})
	run(t, mux, tc{
		name: "join project", method: http.MethodPost,
		url: "/api/users/me/workspaces/hrms/projects/invitations/", token: joinerToken,
		body: `{"project_ids":["` + project.GetString("uuid") + `"]}`, wantStatus: http.StatusCreated,
		want: []string{`"message":"Projects joined successfully"`},
	})
	run(t, mux, tc{
		name: "leave project (member)", method: http.MethodPost,
		url:   "/api/workspaces/hrms/projects/" + project.GetString("uuid") + "/members/leave/",
		token: joinerToken, wantStatus: http.StatusNoContent,
	})
	run(t, mux, tc{
		name: "leave project blocked for sole admin", method: http.MethodPost,
		url:   "/api/workspaces/hrms/projects/" + project.GetString("uuid") + "/members/leave/",
		token: ownerToken, wantStatus: http.StatusBadRequest, want: []string{`"error"`},
	})
	run(t, mux, tc{
		name: "leave workspace blocked for sole admin", method: http.MethodPost,
		url: "/api/workspaces/hrms/members/leave/", token: ownerToken, wantStatus: http.StatusBadRequest,
		want: []string{`"error"`},
	})
	run(t, mux, tc{
		name: "leave workspace (member)", method: http.MethodPost,
		url: "/api/workspaces/hrms/members/leave/", token: joinerToken, wantStatus: http.StatusNoContent,
	})
	run(t, mux, tc{
		name: "configs", method: http.MethodGet, url: "/api/configs/", wantStatus: http.StatusOK,
		want: []string{`"enable_signup"`, `"is_self_managed":true`},
	})
	run(t, mux, tc{
		name: "release-notes", method: http.MethodGet, url: "/api/release-notes/", wantStatus: http.StatusOK,
		want: []string{`"Wave 2 release"`, `"version":"1.4.2"`},
	})
}

// tc is one HTTP case.
type tc struct {
	name       string
	method     string
	url        string
	token      string
	body       string
	wantStatus int
	want       []string
	notWant    []string
}

func run(t *testing.T, mux http.Handler, c tc) {
	t.Helper()
	t.Run(c.name, func(t *testing.T) {
		var body *strings.Reader
		if c.body != "" {
			body = strings.NewReader(c.body)
		} else {
			body = strings.NewReader("")
		}
		req := httptest.NewRequest(c.method, c.url, body)
		req.Header.Set("Content-Type", "application/json")
		if c.token != "" {
			req.Header.Set("Authorization", c.token)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != c.wantStatus {
			t.Fatalf("status: want %d, got %d (body: %s)", c.wantStatus, rec.Code, rec.Body.String())
		}
		out := rec.Body.String()
		for _, want := range c.want {
			if !strings.Contains(out, want) {
				t.Errorf("body missing %q\nbody: %s", want, out)
			}
		}
		for _, notWant := range c.notWant {
			if strings.Contains(out, notWant) {
				t.Errorf("body unexpectedly contains %q\nbody: %s", notWant, out)
			}
		}
	})
}

func saveUser(t *testing.T, app core.App, uuid, email string) *core.Record {
	t.Helper()
	return save(t, app, "users", func(r *core.Record) {
		r.Set("uuid", uuid)
		r.Set("username", strings.SplitN(email, "@", 2)[0])
		r.Set("email", email)
		r.Set("password", "1234567890")
		r.Set("is_active", true)
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
}

func save(t *testing.T, app core.App, collection string, set func(*core.Record)) *core.Record {
	t.Helper()
	c, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		t.Fatalf("collection %s: %v", collection, err)
	}
	r := core.NewRecord(c)
	if c.Fields.GetByName("uuid") != nil && r.GetString("uuid") == "" {
		r.Set("uuid", serializers.NewUUID())
	}
	set(r)
	if err := app.Save(r); err != nil {
		t.Fatalf("save %s: %v", collection, err)
	}
	return r
}

func mustSaveMembership(t *testing.T, app core.App, workspaceID, memberID string, role int) *core.Record {
	t.Helper()
	return save(t, app, "workspace_members", func(r *core.Record) {
		r.Set("workspace", workspaceID)
		r.Set("member", memberID)
		r.Set("role", role)
		r.Set("is_active", true)
	})
}

func authToken(t *testing.T, user *core.Record) string {
	t.Helper()
	token, err := user.NewAuthToken()
	if err != nil {
		t.Fatalf("auth token: %v", err)
	}
	return token
}
