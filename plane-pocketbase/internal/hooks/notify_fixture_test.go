package hooks_test

// Shared fixture for the L-4 notification tests. Same recipe as wiring_test.go
// (tests.NewTestApp + router.Register + app.Save); the unread-count helper
// mirrors internal/handlers/notification/notification.go's UnreadCount filter
// VERBATIM so a green assertion here means a green
// GET …/users/notifications/unread/ for that user.

import (
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/plane-pocketbase/plane-pocketbase/internal/router"
	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

type notifyFixture struct {
	t       *testing.T
	app     core.App
	a       *core.Record // the actor (creates/assigns/comments)
	b       *core.Record // the usual receiver
	c       *core.Record // a second receiver
	ws      *core.Record
	project *core.Record
	state   *core.Record
	seq     int
}

func newNotifyFixture(t *testing.T) *notifyFixture {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	f := &notifyFixture{t: t, app: app}
	f.a = f.user("actor-a", "Actor A")
	f.b = f.user("actor-b", "Actor B")
	f.c = f.user("actor-c", "Actor C")
	f.ws = f.save("workspaces", func(r *core.Record) {
		r.Set("name", "WS")
		r.Set("slug", "ws")
		r.Set("owner", f.a.Id)
	})
	f.project = f.save("projects", func(r *core.Record) {
		r.Set("name", "P")
		r.Set("identifier", "P")
		r.Set("workspace", f.ws.Id)
	})
	f.state = f.save("states", func(r *core.Record) {
		r.Set("name", "Todo")
		r.Set("color", "#fff")
		r.Set("group", "unstarted")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
	})
	return f
}

// save creates one record the way the handlers do (NewRecord + Save), which is
// also the path the hooks observe.
func (f *notifyFixture) save(collection string, set func(*core.Record)) *core.Record {
	t := f.t
	c, err := f.app.FindCollectionByNameOrId(collection)
	if err != nil {
		t.Fatalf("collection %s: %v", collection, err)
	}
	r := core.NewRecord(c)
	r.Set("uuid", serializers.NewUUID())
	set(r)
	if err := f.app.Save(r); err != nil {
		t.Fatalf("save %s: %v", collection, err)
	}
	return r
}

// user creates a Plane user row.
func (f *notifyFixture) user(username, displayName string) *core.Record {
	return f.save("users", func(r *core.Record) {
		r.Set("username", username)
		r.Set("email", username+"@example.com")
		r.Set("display_name", displayName)
		r.Set("password", "1234567890")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
}

// newIssue creates one issue owned by `creator`.
func (f *notifyFixture) newIssue(creator *core.Record) *core.Record {
	f.seq++
	return f.save("issues", func(r *core.Record) {
		r.Set("name", "issue")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("state", f.state.Id)
		r.Set("created_by", creator.Id)
		r.Set("sequence_id", f.seq)
	})
}

// register binds the production wiring (what main.go runs). Call it AFTER the
// fixture graph exists and BEFORE the records under test.
func (f *notifyFixture) register() {
	router.Register(f.app)
}

// notifications returns the receiver's live notification rows, newest first.
func (f *notifyFixture) notifications(receiver string) []*core.Record {
	rows, err := f.app.FindRecordsByFilter(
		"notifications",
		"receiver = {:receiver} && deleted_at = ''",
		"-created_at", 0, 0,
		dbx.Params{"receiver": receiver},
	)
	if err != nil {
		f.t.Fatalf("find notifications: %v", err)
	}
	return rows
}

// unread mirrors notification.go's UnreadCount filter verbatim: the row counts
// only when read_at, archived_at, snoozed_till and deleted_at are all unset.
func (f *notifyFixture) unread(receiver string) (total, mention int) {
	base := "workspace = {:workspace} && receiver = {:receiver} && read_at = '' && archived_at = '' && snoozed_till = '' && deleted_at = ''"
	params := dbx.Params{"workspace": f.ws.Id, "receiver": receiver}
	others, err := f.app.FindRecordsByFilter("notifications", base+" && sender !~ 'mentioned'", "", 0, 0, params)
	if err != nil {
		f.t.Fatalf("unread others: %v", err)
	}
	mentions, err := f.app.FindRecordsByFilter("notifications", base+" && sender ~ 'mentioned'", "", 0, 0, params)
	if err != nil {
		f.t.Fatalf("unread mentions: %v", err)
	}
	return len(others), len(mentions)
}

// mentionRows returns the live issue_mentions rows of an issue.
func (f *notifyFixture) mentionRows(issue *core.Record) []*core.Record {
	rows, err := f.app.FindRecordsByFilter(
		"issue_mentions",
		"issue = {:issue} && deleted_at = ''",
		"", 0, 0,
		dbx.Params{"issue": issue.Id},
	)
	if err != nil {
		f.t.Fatalf("find issue_mentions: %v", err)
	}
	return rows
}

// comment saves one comment by `actor` on `issue`.
func (f *notifyFixture) comment(actor, issue *core.Record, html string) *core.Record {
	return f.save("issue_comments", func(r *core.Record) {
		r.Set("comment_html", html)
		r.Set("comment_stripped", "hello")
		r.Set("issue", issue.Id)
		r.Set("actor", actor.Id)
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
	})
}

// subscribe adds a live issue_subscribers row.
func (f *notifyFixture) subscribe(user, issue *core.Record) {
	f.save("issue_subscribers", func(r *core.Record) {
		r.Set("issue", issue.Id)
		r.Set("subscriber", user.Id)
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("created_by", user.Id)
	})
}

// assign saves one issue_assignees row the way the handlers do.
func (f *notifyFixture) assign(actor, assignee, issue *core.Record) {
	f.save("issue_assignees", func(r *core.Record) {
		r.Set("issue", issue.Id)
		r.Set("assignee", assignee.Id)
		r.Set("project", f.project.Id)
		r.Set("workspace", f.ws.Id)
		r.Set("created_by", actor.Id)
	})
}
