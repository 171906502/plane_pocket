package page

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

// pageFixture seeds the role ladder ProjectPagePermission distinguishes:
//
//	owner        project Member, owns both pages
//	member       project Member, owns nothing
//	admin        project Admin, owns nothing
//	guest        project Guest
//	wsAdminGuest workspace Admin whose *project* role is Guest — the caller
//	             @allow_permission's bypass admits and ProjectPagePermission
//	             does not
//
// `privatePage` is owned by `owner` with access=1; `publicPage` is owned by
// `owner` with access=0.
type pageFixture struct {
	app          *tests.TestApp
	owner        *core.Record
	member       *core.Record
	admin        *core.Record
	guest        *core.Record
	wsAdminGuest *core.Record
	ws           *core.Record
	project      *core.Record
	privatePage  *core.Record
	publicPage   *core.Record
}

func newPageFixture(t *testing.T) *pageFixture {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	f := &pageFixture{app: app}
	newUser := func(uuid, name string) *core.Record {
		return mustSave(t, app, "users", func(r *core.Record) {
			r.Set("uuid", uuid)
			r.Set("username", name)
			r.Set("email", name+"@example.com")
			r.Set("password", "1234567890")
			r.Set("last_active", "2024-01-01 00:00:00.000Z")
		})
	}
	f.owner = newUser("00000000-0000-0000-0000-0000000000a1", "owner")
	f.member = newUser("00000000-0000-0000-0000-0000000000a2", "member")
	f.admin = newUser("00000000-0000-0000-0000-0000000000a3", "admin")
	f.guest = newUser("00000000-0000-0000-0000-0000000000a4", "guest")
	f.wsAdminGuest = newUser("00000000-0000-0000-0000-0000000000a5", "wsadmin")

	f.ws = mustSave(t, app, "workspaces", func(r *core.Record) {
		r.Set("name", "WS")
		r.Set("slug", "ws")
		r.Set("owner", f.owner.Id)
	})
	f.project = mustSave(t, app, "projects", func(r *core.Record) {
		r.Set("name", "P")
		r.Set("identifier", "P")
		r.Set("workspace", f.ws.Id)
	})
	membership := func(user *core.Record, wsRole, pjRole int) {
		mustSave(t, app, "workspace_members", func(r *core.Record) {
			r.Set("workspace", f.ws.Id)
			r.Set("member", user.Id)
			r.Set("is_active", true)
			r.Set("role", wsRole)
		})
		mustSave(t, app, "project_members", func(r *core.Record) {
			r.Set("project", f.project.Id)
			r.Set("member", user.Id)
			r.Set("workspace", f.ws.Id)
			r.Set("is_active", true)
			r.Set("role", pjRole)
		})
	}
	membership(f.owner, middleware.RoleMember, middleware.RoleMember)
	membership(f.member, middleware.RoleMember, middleware.RoleMember)
	membership(f.admin, middleware.RoleAdmin, middleware.RoleAdmin)
	membership(f.guest, middleware.RoleGuest, middleware.RoleGuest)
	membership(f.wsAdminGuest, middleware.RoleAdmin, middleware.RoleGuest)

	newPage := func(uuid, name string, access int) *core.Record {
		p := mustSave(t, app, "pages", func(r *core.Record) {
			r.Set("uuid", uuid)
			r.Set("workspace", f.ws.Id)
			r.Set("name", name)
			r.Set("owned_by", f.owner.Id)
			r.Set("created_by", f.owner.Id)
			r.Set("access", access)
			r.Set("description_json", map[string]any{})
			r.Set("description_html", "<p>"+name+"</p>")
			r.Set("sort_order", 65535)
		})
		mustSave(t, app, "project_pages", func(r *core.Record) {
			r.Set("workspace", f.ws.Id)
			r.Set("project", f.project.Id)
			r.Set("page", p.Id)
			r.Set("created_by", f.owner.Id)
		})
		return p
	}
	f.privatePage = newPage("00000000-0000-0000-0000-0000000000b1", "private", 1)
	f.publicPage = newPage("00000000-0000-0000-0000-0000000000b2", "public", 0)
	return f
}

// scope mirrors what ResolveWorkspace+ResolveProject produce: both membership
// rows are loaded and ProjectRole keeps the raw project-member role (the
// workspace-Admin bypass lives in the predicates, never in resolution).
func (f *pageFixture) scope(user *core.Record) *middleware.Scope {
	wsMember, _ := f.app.FindFirstRecordByFilter(
		"workspace_members",
		"workspace = {:w} && member = {:m} && is_active = true",
		map[string]any{"w": f.ws.Id, "m": user.Id},
	)
	pjMember, _ := f.app.FindFirstRecordByFilter(
		"project_members",
		"project = {:p} && member = {:m} && is_active = true",
		map[string]any{"p": f.project.Id, "m": user.Id},
	)
	wsRole, pjRole := 0, 0
	if wsMember != nil {
		wsRole = wsMember.GetInt("role")
	}
	if pjMember != nil {
		pjRole = pjMember.GetInt("role")
	}
	return &middleware.Scope{
		Workspace:       f.ws,
		Project:         f.project,
		WorkspaceMember: wsMember,
		ProjectMember:   pjMember,
		WorkspaceRole:   wsRole,
		ProjectRole:     pjRole,
	}
}

func (f *pageFixture) event(t *testing.T, user *core.Record, method, target, body string) (*core.RequestEvent, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: f.app}
	e.Request = req
	e.Response = rec
	e.Auth = user
	e.Set("plane.scope", f.scope(user))
	return e, rec
}

func mustSave(t *testing.T, app core.App, collection string, set func(*core.Record)) *core.Record {
	t.Helper()
	c, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		t.Fatalf("collection %s: %v", collection, err)
	}
	r := core.NewRecord(c)
	r.Set("uuid", serializers.NewUUID())
	set(r)
	if err := app.Save(r); err != nil {
		t.Fatalf("save %s: %v", collection, err)
	}
	return r
}

// TestDetailPrivatePageIsInvisible pins PageViewSet.get_queryset's
// `Q(owned_by=caller) | Q(access=0)` rule: a private page must behave as absent
// for every caller but its owner, whatever their project role. Before the fix a
// plain project Member could read another user's private page.
func TestDetailPrivatePageIsInvisible(t *testing.T) {
	f := newPageFixture(t)
	h := New(f.app)
	id := f.privatePage.GetString("uuid")

	cases := []struct {
		name string
		user *core.Record
		want int
	}{
		{"owner may read", f.owner, http.StatusOK},
		{"plain member refused", f.member, http.StatusNotFound},
		{"project admin refused", f.admin, http.StatusNotFound},
		{"guest refused", f.guest, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, rec := f.event(t, tc.user, http.MethodGet, "/pages/"+id+"/", "")
			e.Request.SetPathValue("page_id", id)
			if err := h.Detail(e); err != nil {
				t.Fatalf("Detail: %v", err)
			}
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusOK && !strings.Contains(rec.Body.String(), "private") {
				t.Fatalf("owner body does not carry the page: %s", rec.Body.String())
			}
		})
	}
}

// TestDetailPublicPageGuestRule pins the guest half: a Guest without
// guest_view_all_features sees only their own pages (Django answers 400 with
// this exact message), while a Member sees a public page.
func TestDetailPublicPageGuestRule(t *testing.T) {
	f := newPageFixture(t)
	h := New(f.app)
	id := f.publicPage.GetString("uuid")

	e, rec := f.event(t, f.member, http.MethodGet, "/pages/"+id+"/", "")
	e.Request.SetPathValue("page_id", id)
	if err := h.Detail(e); err != nil {
		t.Fatalf("Detail(member): %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("member public page: status = %d, body %s", rec.Code, rec.Body.String())
	}

	e, rec = f.event(t, f.guest, http.MethodGet, "/pages/"+id+"/", "")
	e.Request.SetPathValue("page_id", id)
	if err := h.Detail(e); err != nil {
		t.Fatalf("Detail(guest): %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("guest public page: status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not allowed to view this page") {
		t.Fatalf("guest body = %s", rec.Body.String())
	}
}

// TestLockGuards pins ProjectPagePermission on the lock/unlock pair: the owner
// passes; otherwise POST needs ADMIN/MEMBER and DELETE needs ADMIN. Before the
// fix neither half was guarded at all, so a Guest could lock any page.
func TestLockGuards(t *testing.T) {
	f := newPageFixture(t)
	h := New(f.app)
	id := f.publicPage.GetString("uuid")

	lock := func(t *testing.T, user *core.Record) int {
		t.Helper()
		e, rec := f.event(t, user, http.MethodPost, "/pages/"+id+"/lock/", "")
		e.Request.SetPathValue("page_id", id)
		if err := h.Lock(e); err != nil {
			t.Fatalf("Lock: %v", err)
		}
		return rec.Code
	}

	if got := lock(t, f.guest); got != http.StatusForbidden {
		t.Fatalf("guest lock: status = %d, want 403", got)
	}
	if got := lock(t, f.member); got != http.StatusNoContent {
		t.Fatalf("member lock: status = %d, want 204", got)
	}
	reloaded, _ := f.app.FindRecordById("pages", f.publicPage.Id)
	if !reloaded.GetBool("is_locked") {
		t.Fatalf("member lock did not persist is_locked")
	}
	// Owner (a plain project Member here) is admitted by the owned_by shortcut.
	if got := lock(t, f.owner); got != http.StatusNoContent {
		t.Fatalf("owner lock: status = %d, want 204", got)
	}

	unlock := func(t *testing.T, user *core.Record) int {
		t.Helper()
		e, rec := f.event(t, user, http.MethodDelete, "/pages/"+id+"/lock/", "")
		e.Request.SetPathValue("page_id", id)
		if err := h.Unlock(e); err != nil {
			t.Fatalf("Unlock: %v", err)
		}
		return rec.Code
	}
	if got := unlock(t, f.member); got != http.StatusForbidden {
		t.Fatalf("member unlock: status = %d, want 403 (DELETE is ADMIN-only)", got)
	}
	if got := unlock(t, f.admin); got != http.StatusNoContent {
		t.Fatalf("admin unlock: status = %d, want 204", got)
	}
}

// TestDescriptionPatchGuard pins ProjectPagePermission on the description PATCH:
// ADMIN/MEMBER for a non-owner. Before the fix a Guest could rewrite the
// collaborative document of any page in the project.
func TestDescriptionPatchGuard(t *testing.T) {
	f := newPageFixture(t)
	h := New(f.app)
	id := f.publicPage.GetString("uuid")

	patch := func(t *testing.T, user *core.Record) int {
		t.Helper()
		e, rec := f.event(t, user, http.MethodPatch, "/pages/"+id+"/description/", `{"description_html":"<p>hijacked</p>"}`)
		e.Request.SetPathValue("page_id", id)
		if err := h.DescriptionPatch(e); err != nil {
			t.Fatalf("DescriptionPatch: %v", err)
		}
		return rec.Code
	}

	if got := patch(t, f.guest); got != http.StatusForbidden {
		t.Fatalf("guest patch: status = %d, want 403", got)
	}
	reloaded, _ := f.app.FindRecordById("pages", f.publicPage.Id)
	if strings.Contains(reloaded.GetString("description_html"), "hijacked") {
		t.Fatalf("guest mutation leaked through: %s", reloaded.GetString("description_html"))
	}
	if got := patch(t, f.member); got != http.StatusOK {
		t.Fatalf("member patch: status = %d, want 200", got)
	}
	reloaded, _ = f.app.FindRecordById("pages", f.publicPage.Id)
	if !strings.Contains(reloaded.GetString("description_html"), "hijacked") {
		t.Fatalf("member patch did not persist: %s", reloaded.GetString("description_html"))
	}
}

// TestArchiveAllowsMember pins the corrected floor: PageViewSet.archive is a
// POST, so ProjectPagePermission asks ADMIN/MEMBER of a non-owner. The handler
// used to require ADMIN, which refused a plain project Member.
func TestArchiveAllowsMember(t *testing.T) {
	f := newPageFixture(t)
	h := New(f.app)
	id := f.publicPage.GetString("uuid")

	e, rec := f.event(t, f.member, http.MethodPost, "/pages/"+id+"/archive/", "")
	e.Request.SetPathValue("page_id", id)
	if err := h.Archive(e); err != nil {
		t.Fatalf("Archive(member): %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("member archive: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	reloaded, _ := f.app.FindRecordById("pages", f.publicPage.Id)
	if reloaded.GetString("archived_at") == "" {
		t.Fatalf("archive did not persist archived_at")
	}

	// A Guest is still refused (POST floor is MEMBER, and they are not owner).
	e, rec = f.event(t, f.guest, http.MethodPost, "/pages/"+id+"/archive/", "")
	e.Request.SetPathValue("page_id", id)
	if err := h.Archive(e); err != nil {
		t.Fatalf("Archive(guest): %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("guest archive: status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestFavoriteWorkspaceAdminBypass pins that the favourites pair carries
// allow_permission's bypass: PageFavoriteViewSet is decorated with
// @allow_permission([ADMIN, MEMBER]), so a workspace Admin holding a Guest
// project role is admitted, while a plain Guest is not.
func TestFavoriteWorkspaceAdminBypass(t *testing.T) {
	f := newPageFixture(t)
	h := New(f.app)
	id := f.publicPage.GetString("uuid")

	fav := func(t *testing.T, user *core.Record) int {
		t.Helper()
		e, rec := f.event(t, user, http.MethodPost, "/favorite-pages/"+id+"/", "")
		e.Request.SetPathValue("page_id", id)
		if err := h.FavoriteCreate(e); err != nil {
			t.Fatalf("FavoriteCreate: %v", err)
		}
		return rec.Code
	}

	if got := fav(t, f.guest); got != http.StatusForbidden {
		t.Fatalf("guest favourite: status = %d, want 403", got)
	}
	if got := fav(t, f.wsAdminGuest); got != http.StatusNoContent {
		t.Fatalf("ws-admin/guest-project favourite: status = %d, want 204", got)
	}
	rows, _ := f.app.FindRecordsByFilter(
		"user_favorites",
		"user = {:u} && entity_type = 'page' && entity_identifier = {:p}",
		"", 0, 0,
		map[string]any{"u": f.wsAdminGuest.Id, "p": id},
	)
	if len(rows) != 1 {
		t.Fatalf("ws-admin favourite rows = %d, want 1", len(rows))
	}
}

// `description_binary` is a PocketBase *file* field: the column holds a filename
// and the bytes live in protected storage. The editors send base64 of a yjs
// state update, so the handler has to write the file — assigning the payload to
// the field directly (the previous behaviour) stored base64 as a filename and
// failed validation, which left `GET …/description/` permanently empty.
func TestDescriptionBinaryRoundTrip(t *testing.T) {
	f := newPageFixture(t)
	h := New(f.app)

	raw := []byte{0x00, 0x01, 0x02, 0xfe, 0xff}
	encoded := base64.StdEncoding.EncodeToString(raw)

	if err := h.setDescriptionBinary(f.publicPage, encoded); err != nil {
		t.Fatalf("setDescriptionBinary: %v", err)
	}
	if err := f.app.Save(f.publicPage); err != nil {
		t.Fatalf("save: %v", err)
	}
	if stored := f.publicPage.GetString("description_binary"); strings.Contains(stored, "=") || stored == encoded {
		t.Fatalf("column holds the payload, not a filename: %q", stored)
	}
	if got := h.readBinary(f.publicPage); !bytes.Equal(got, raw) {
		t.Fatalf("readBinary = %v, want %v", got, raw)
	}

	// Echoing the stored filename back (what a PATCH without a new snapshot
	// does) must not clear the file.
	filename := f.publicPage.GetString("description_binary")
	if err := h.setDescriptionBinary(f.publicPage, filename); err != nil {
		t.Fatalf("setDescriptionBinary(filename): %v", err)
	}
	if err := f.app.Save(f.publicPage); err != nil {
		t.Fatalf("save after echo: %v", err)
	}
	if got := h.readBinary(f.publicPage); !bytes.Equal(got, raw) {
		t.Fatalf("readBinary after echo = %v, want %v", got, raw)
	}
}
