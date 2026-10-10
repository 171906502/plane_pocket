package issues

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/plane-pocketbase/plane-pocketbase/internal/middleware"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

// wave2Fixture is a fully-seeded project graph shared by the Wave-2 handler
// tests. Every table the Wave-2 endpoints read starts EMPTY in the shipped
// data.db, so these tests must create rows before asserting anything — a 200
// with an empty array proves nothing.
type wave2Fixture struct {
	app       *tests.TestApp
	user      *core.Record
	other     *core.Record
	workspace *core.Record
	project   *core.Record
	state     *core.Record
	issue     *core.Record
	related   *core.Record
	scope     *middleware.Scope
}

func newWave2Fixture(t *testing.T) *wave2Fixture {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	f := &wave2Fixture{app: app}
	f.user = mustSave(t, app, "users", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-0000000000a1")
		r.Set("username", "me")
		r.Set("email", "me@example.com")
		r.Set("password", "1234567890")
		r.Set("display_name", "Me")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	f.other = mustSave(t, app, "users", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-0000000000a2")
		r.Set("username", "other")
		r.Set("email", "other@example.com")
		r.Set("password", "1234567890")
		r.Set("display_name", "Other")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	f.workspace = mustSave(t, app, "workspaces", func(r *core.Record) {
		r.Set("name", "WS")
		r.Set("slug", "ws")
		r.Set("owner", f.user.Id)
	})
	f.project = mustSave(t, app, "projects", func(r *core.Record) {
		r.Set("name", "P")
		r.Set("identifier", "P")
		r.Set("workspace", f.workspace.Id)
	})
	member := mustSave(t, app, "project_members", func(r *core.Record) {
		r.Set("project", f.project.Id)
		r.Set("member", f.user.Id)
		r.Set("workspace", f.workspace.Id)
		r.Set("is_active", true)
		r.Set("role", 15)
	})
	f.state = mustSave(t, app, "states", func(r *core.Record) {
		r.Set("name", "Todo")
		r.Set("color", "#fff")
		r.Set("group", "unstarted")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
	})

	newIssue := func(uuid, name string) *core.Record {
		return mustSave(t, app, "issues", func(r *core.Record) {
			r.Set("uuid", uuid)
			r.Set("name", name)
			r.Set("project", f.project.Id)
			r.Set("workspace", f.workspace.Id)
			r.Set("state", f.state.Id)
			r.Set("created_by", f.user.Id)
			r.Set("sequence_id", 1)
		})
	}
	f.issue = newIssue("00000000-0000-0000-0000-0000000000b1", "Current")
	f.related = newIssue("00000000-0000-0000-0000-0000000000b2", "Related")

	f.scope = &middleware.Scope{
		Workspace:       f.workspace,
		Project:         f.project,
		WorkspaceMember: member,
		ProjectMember:   member,
		WorkspaceRole:   20,
		ProjectRole:     15,
	}
	return f
}

// event builds a RequestEvent with the resolved scope, an authenticated user
// and the given path parameters. `scopeKey` is middleware's unexported
// contextKey("plane.scope") rendered as a string.
func (f *wave2Fixture) event(t *testing.T, method, target, body string, pathValues map[string]string) (*core.RequestEvent, *httptest.ResponseRecorder) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range pathValues {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: f.app}
	e.Request = req
	e.Response = rec
	e.Auth = f.user
	e.Set("plane.scope", f.scope)
	return e, rec
}

func (f *wave2Fixture) issueUUID() string  { return f.issue.GetString("uuid") }
func (f *wave2Fixture) relateUUID() string { return f.related.GetString("uuid") }

func decodeObject(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode object %q: %v", rec.Body.String(), err)
	}
	return out
}

func decodeArray(t *testing.T, rec *httptest.ResponseRecorder) []any {
	t.Helper()
	var out []any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode array %q: %v", rec.Body.String(), err)
	}
	return out
}

// TestHistoryReturnsSeededContent proves the history endpoint returns the
// activity and comment rows it is supposed to, in all three projections.
func TestHistoryReturnsSeededContent(t *testing.T) {
	f := newWave2Fixture(t)
	h := New(f.app)

	mustSave(t, f.app, "issue_activities", func(r *core.Record) {
		r.Set("issue", f.issue.Id)
		r.Set("verb", "updated")
		r.Set("field", "state")
		r.Set("old_value", "Todo")
		r.Set("new_value", "In Progress")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
		r.Set("actor", f.user.Id)
		r.Set("created_by", f.user.Id)
	})
	mustSave(t, f.app, "issue_comments", func(r *core.Record) {
		r.Set("issue", f.issue.Id)
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
		r.Set("actor", f.user.Id)
		r.Set("comment_html", "<p>hello</p>")
		r.Set("comment_stripped", "hello")
		r.Set("created_by", f.user.Id)
	})

	e, rec := f.event(t, http.MethodGet, "/", "", map[string]string{"issue_id": f.issueUUID()})
	if err := h.History(e); err != nil {
		t.Fatalf("history: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("history status %d body %s", rec.Code, rec.Body.String())
	}
	all := decodeArray(t, rec)
	if len(all) != 2 {
		t.Fatalf("merged history: want 2 rows (1 activity + 1 comment), got %d: %s", len(all), rec.Body.String())
	}

	e, rec = f.event(t, http.MethodGet, "/?activity_type=issue-property", "", map[string]string{"issue_id": f.issueUUID()})
	if err := h.History(e); err != nil {
		t.Fatalf("history issue-property: %v", err)
	}
	props := decodeArray(t, rec)
	if len(props) != 1 || props[0].(map[string]any)["field"] != "state" {
		t.Fatalf("issue-property history: want 1 state activity, got %s", rec.Body.String())
	}

	e, rec = f.event(t, http.MethodGet, "/?activity_type=issue-comment", "", map[string]string{"issue_id": f.issueUUID()})
	if err := h.History(e); err != nil {
		t.Fatalf("history issue-comment: %v", err)
	}
	comments := decodeArray(t, rec)
	if len(comments) != 1 || comments[0].(map[string]any)["comment_stripped"] != "hello" {
		t.Fatalf("issue-comment history: want 1 comment, got %s", rec.Body.String())
	}
	if comments[0].(map[string]any)["is_member"] != true {
		t.Fatalf("comment is_member must be true for an active member: %s", rec.Body.String())
	}
}

// TestCommentLifecycle covers list/create/patch/delete and asserts the list is
// non-empty after the create.
func TestCommentLifecycle(t *testing.T) {
	f := newWave2Fixture(t)
	h := New(f.app)

	mustSave(t, f.app, "issue_comments", func(r *core.Record) {
		r.Set("issue", f.issue.Id)
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
		r.Set("actor", f.user.Id)
		r.Set("comment_html", "<p>seeded</p>")
		r.Set("comment_stripped", "seeded")
		r.Set("created_by", f.user.Id)
	})

	e, rec := f.event(t, http.MethodGet, "/", "", map[string]string{"issue_id": f.issueUUID()})
	if err := h.ListComments(e); err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if got := decodeArray(t, rec); len(got) != 1 {
		t.Fatalf("seeded comments: want 1, got %d: %s", len(got), rec.Body.String())
	}

	e, rec = f.event(t, http.MethodPost, "/", `{"comment_html":"<p>new</p>"}`, map[string]string{"issue_id": f.issueUUID()})
	if err := h.CreateComment(e); err != nil {
		t.Fatalf("create comment: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment status %d: %s", rec.Code, rec.Body.String())
	}
	created := decodeObject(t, rec)
	if created["comment_stripped"] != "new" {
		t.Fatalf("created comment_stripped: %v", created["comment_stripped"])
	}
	commentID, _ := created["id"].(string)

	e, rec = f.event(t, http.MethodGet, "/", "", map[string]string{"issue_id": f.issueUUID()})
	_ = h.ListComments(e)
	if got := decodeArray(t, rec); len(got) != 2 {
		t.Fatalf("comments after create: want 2, got %d: %s", len(got), rec.Body.String())
	}

	e, rec = f.event(t, http.MethodPatch, "/", `{"comment_html":"<p>edited</p>"}`, map[string]string{"issue_id": f.issueUUID(), "pk": commentID})
	if err := h.UpdateComment(e); err != nil {
		t.Fatalf("patch comment: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("patch comment status %d: %s", rec.Code, rec.Body.String())
	}
	if decodeObject(t, rec)["comment_stripped"] != "edited" {
		t.Fatalf("patched comment_stripped: %s", rec.Body.String())
	}

	e, rec = f.event(t, http.MethodDelete, "/", "", map[string]string{"issue_id": f.issueUUID(), "pk": commentID})
	if err := h.DeleteComment(e); err != nil {
		t.Fatalf("delete comment: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete comment status %d", rec.Code)
	}
}

// TestSubscribeLifecycle covers the status/subscribe/unsubscribe trio.
func TestSubscribeLifecycle(t *testing.T) {
	f := newWave2Fixture(t)
	h := New(f.app)
	pv := map[string]string{"issue_id": f.issueUUID()}

	e, rec := f.event(t, http.MethodGet, "/", "", pv)
	if err := h.SubscriptionStatus(e); err != nil {
		t.Fatalf("subscription status: %v", err)
	}
	if decodeObject(t, rec)["subscribed"] != false {
		t.Fatalf("expected not subscribed: %s", rec.Body.String())
	}

	e, rec = f.event(t, http.MethodPost, "/", "", pv)
	if err := h.Subscribe(e); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("subscribe status %d: %s", rec.Code, rec.Body.String())
	}
	if decodeObject(t, rec)["subscriber"] != f.user.GetString("uuid") {
		t.Fatalf("subscribe subscriber: %s", rec.Body.String())
	}

	e, rec = f.event(t, http.MethodGet, "/", "", pv)
	_ = h.SubscriptionStatus(e)
	if decodeObject(t, rec)["subscribed"] != true {
		t.Fatalf("expected subscribed after POST: %s", rec.Body.String())
	}

	e, rec = f.event(t, http.MethodDelete, "/", "", pv)
	if err := h.Unsubscribe(e); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unsubscribe status %d", rec.Code)
	}
}

// TestReactionLifecycle covers list/create/delete for issue reactions.
func TestReactionLifecycle(t *testing.T) {
	f := newWave2Fixture(t)
	h := New(f.app)
	pv := map[string]string{"issue_id": f.issueUUID()}

	mustSave(t, f.app, "issue_reactions", func(r *core.Record) {
		r.Set("issue", f.issue.Id)
		r.Set("actor", f.other.Id)
		r.Set("reaction", "👍")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
	})

	e, rec := f.event(t, http.MethodGet, "/", "", pv)
	if err := h.ListReactions(e); err != nil {
		t.Fatalf("list reactions: %v", err)
	}
	if got := decodeArray(t, rec); len(got) != 1 {
		t.Fatalf("seeded reactions: want 1, got %d: %s", len(got), rec.Body.String())
	}

	e, rec = f.event(t, http.MethodPost, "/", `{"reaction":"🎉"}`, pv)
	if err := h.CreateReaction(e); err != nil {
		t.Fatalf("create reaction: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("create reaction status %d: %s", rec.Code, rec.Body.String())
	}

	e, rec = f.event(t, http.MethodDelete, "/", "", map[string]string{"issue_id": f.issueUUID(), "reaction": "🎉"})
	if err := h.DeleteReaction(e); err != nil {
		t.Fatalf("delete reaction: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete reaction status %d", rec.Code)
	}
}

// TestRelationLifecycle covers list/create/remove-relation.
func TestRelationLifecycle(t *testing.T) {
	f := newWave2Fixture(t)
	h := New(f.app)
	pv := map[string]string{"issue_id": f.issueUUID()}

	e, rec := f.event(t, http.MethodPost, "/", `{"relation_type":"relates_to","issues":["`+f.relateUUID()+`"]}`,
		map[string]string{"issue_id": f.issueUUID()})
	if err := h.CreateRelations(e); err != nil {
		t.Fatalf("create relations: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("create relations status %d: %s", rec.Code, rec.Body.String())
	}
	created := decodeArray(t, rec)
	if len(created) != 1 || created[0].(map[string]any)["id"] != f.relateUUID() {
		t.Fatalf("create relations body: %s", rec.Body.String())
	}

	e, rec = f.event(t, http.MethodGet, "/", "", pv)
	if err := h.ListRelations(e); err != nil {
		t.Fatalf("list relations: %v", err)
	}
	grouped := decodeObject(t, rec)
	relatesTo, _ := grouped["relates_to"].([]any)
	if len(relatesTo) != 1 {
		t.Fatalf("relates_to: want 1, got %s", rec.Body.String())
	}

	e, rec = f.event(t, http.MethodPost, "/", `{"related_issue":"`+f.relateUUID()+`"}`,
		map[string]string{"issue_id": f.issueUUID()})
	if err := h.RemoveRelation(e); err != nil {
		t.Fatalf("remove relation: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remove relation status %d: %s", rec.Code, rec.Body.String())
	}

	e, rec = f.event(t, http.MethodGet, "/", "", pv)
	_ = h.ListRelations(e)
	grouped = decodeObject(t, rec)
	if relatesTo, _ := grouped["relates_to"].([]any); len(relatesTo) != 0 {
		t.Fatalf("relates_to after remove: want 0, got %s", rec.Body.String())
	}
}

// TestCommentReactionLifecycle covers the comment-reaction sub-resource.
func TestCommentReactionLifecycle(t *testing.T) {
	f := newWave2Fixture(t)
	h := New(f.app)
	comment := mustSave(t, f.app, "issue_comments", func(r *core.Record) {
		r.Set("issue", f.issue.Id)
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
		r.Set("actor", f.user.Id)
		r.Set("comment_html", "<p>hi</p>")
		r.Set("created_by", f.user.Id)
	})
	cid := comment.GetString("uuid")
	pv := map[string]string{"comment_id": cid}

	mustSave(t, f.app, "comment_reactions", func(r *core.Record) {
		r.Set("comment", comment.Id)
		r.Set("actor", f.other.Id)
		r.Set("reaction", "🔥")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
	})

	e, rec := f.event(t, http.MethodGet, "/", "", pv)
	if err := h.ListCommentReactions(e); err != nil {
		t.Fatalf("list comment reactions: %v", err)
	}
	if got := decodeArray(t, rec); len(got) != 1 {
		t.Fatalf("seeded comment reactions: want 1, got %d: %s", len(got), rec.Body.String())
	}

	e, rec = f.event(t, http.MethodPost, "/", `{"reaction":"❤️"}`, pv)
	if err := h.CreateCommentReaction(e); err != nil {
		t.Fatalf("create comment reaction: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment reaction status %d: %s", rec.Code, rec.Body.String())
	}

	e, rec = f.event(t, http.MethodDelete, "/", "", map[string]string{"comment_id": cid, "reaction": "❤️"})
	if err := h.DeleteCommentReaction(e); err != nil {
		t.Fatalf("delete comment reaction: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete comment reaction status %d", rec.Code)
	}
}

// TestDescriptionVersions proves both the list envelope and the detail row
// return the seeded version.
func TestDescriptionVersions(t *testing.T) {
	f := newWave2Fixture(t)
	h := New(f.app)
	version := mustSave(t, f.app, "issue_description_versions", func(r *core.Record) {
		r.Set("issue", f.issue.Id)
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
		r.Set("owned_by", f.user.Id)
		r.Set("last_saved_at", "2024-01-01 00:00:00.000Z")
		r.Set("description_html", "<p>v1</p>")
		r.Set("description_stripped", "v1")
	})

	e, rec := f.event(t, http.MethodGet, "/", "", map[string]string{"work_item_id": f.issueUUID()})
	if err := h.DescriptionVersions(e); err != nil {
		t.Fatalf("description versions: %v", err)
	}
	env := decodeObject(t, rec)
	results, _ := env["results"].([]any)
	if len(results) != 1 || env["total_results"].(float64) != 1 {
		t.Fatalf("description versions envelope: %s", rec.Body.String())
	}
	if _, ok := env["next_cursor"]; !ok {
		t.Fatalf("envelope missing next_cursor: %s", rec.Body.String())
	}

	e, rec = f.event(t, http.MethodGet, "/", "", map[string]string{"work_item_id": f.issueUUID(), "pk": version.GetString("uuid")})
	if err := h.DescriptionVersionDetail(e); err != nil {
		t.Fatalf("description version detail: %v", err)
	}
	if decodeObject(t, rec)["description_html"] != "<p>v1</p>" {
		t.Fatalf("detail body: %s", rec.Body.String())
	}
}

// TestBulkUpdateDates covers the POST bulk date update.
func TestBulkUpdateDates(t *testing.T) {
	f := newWave2Fixture(t)
	h := New(f.app)

	e, rec := f.event(t, http.MethodPost, "/",
		`{"updates":[{"id":"`+f.issueUUID()+`","start_date":"2024-02-01","target_date":"2024-03-01"}]}`, nil)
	if err := h.BulkUpdateDates(e); err != nil {
		t.Fatalf("bulk update dates: %v", err)
	}
	if rec.Code != http.StatusOK || decodeObject(t, rec)["message"] != "Issues updated successfully" {
		t.Fatalf("bulk update dates: %d %s", rec.Code, rec.Body.String())
	}
	reloaded, _ := f.app.FindRecordById("issues", f.issue.Id)
	if got := reloaded.GetString("start_date"); !strings.HasPrefix(got, "2024-02-01") {
		t.Fatalf("start_date not persisted: %q", got)
	}
}

// TestUserProperties covers issue-display-properties (GET/PATCH/POST) and
// epics-user-properties (GET/PATCH), including the get-or-create defaults.
func TestUserProperties(t *testing.T) {
	f := newWave2Fixture(t)
	h := New(f.app)

	e, rec := f.event(t, http.MethodGet, "/", "", nil)
	if err := h.DisplayProperties(e); err != nil {
		t.Fatalf("display properties GET: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("display properties status %d: %s", rec.Code, rec.Body.String())
	}
	props := decodeObject(t, rec)
	displayProps, _ := props["display_properties"].(map[string]any)
	if displayProps["assignee"] != true {
		t.Fatalf("default display_properties missing assignee: %s", rec.Body.String())
	}

	e, rec = f.event(t, http.MethodPost, "/", `{"properties":{"assignee":false,"key":true}}`, nil)
	if err := h.UpdateDisplayProperties(e); err != nil {
		t.Fatalf("display properties POST: %v", err)
	}
	updated, _ := decodeObject(t, rec)["display_properties"].(map[string]any)
	if updated["assignee"] != false {
		t.Fatalf("display_properties not updated: %s", rec.Body.String())
	}

	e, rec = f.event(t, http.MethodPatch, "/", `{"display_filters":{"layout":"kanban"}}`, nil)
	if err := h.UpdateDisplayProperties(e); err != nil {
		t.Fatalf("display properties PATCH: %v", err)
	}
	if df, _ := decodeObject(t, rec)["display_filters"].(map[string]any); df["layout"] != "kanban" {
		t.Fatalf("display_filters not updated: %s", rec.Body.String())
	}

	e, rec = f.event(t, http.MethodGet, "/", "", nil)
	if err := h.EpicUserProperties(e); err != nil {
		t.Fatalf("epic user properties GET: %v", err)
	}
	if rec.Code != http.StatusOK || decodeObject(t, rec)["rich_filters"] == nil {
		t.Fatalf("epic user properties: %d %s", rec.Code, rec.Body.String())
	}
}
