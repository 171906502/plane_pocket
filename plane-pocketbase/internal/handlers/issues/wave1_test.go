package issues

import (
	"net/url"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"

	_ "github.com/plane-pocketbase/plane-pocketbase/pb_migrations"
)

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

// TestUserIssuesFilterSemantics seeds a minimal graph and asserts the
// user-issues filter (wave1.go) selects exactly the issues the target user is
// assigned to, created, or subscribed to — and nothing else. It also covers the
// Assigned tab's extra `assignees=<uuid>` narrowing, which is the leg the
// profile page actually calls.
func TestUserIssuesFilterSemantics(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	defer app.Cleanup()

	target := mustSave(t, app, "users", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-000000000001")
		r.Set("username", "target")
		r.Set("email", "target@example.com")
		r.Set("password", "1234567890")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	other := mustSave(t, app, "users", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-000000000002")
		r.Set("username", "other")
		r.Set("email", "other@example.com")
		r.Set("password", "1234567890")
		r.Set("last_active", "2024-01-01 00:00:00.000Z")
	})
	ws := mustSave(t, app, "workspaces", func(r *core.Record) {
		r.Set("name", "WS")
		r.Set("slug", "ws")
		r.Set("owner", target.Id)
	})
	project := mustSave(t, app, "projects", func(r *core.Record) {
		r.Set("name", "P")
		r.Set("identifier", "P")
		r.Set("workspace", ws.Id)
	})
	mustSave(t, app, "project_members", func(r *core.Record) {
		r.Set("project", project.Id)
		r.Set("member", target.Id)
		r.Set("workspace", ws.Id)
		r.Set("is_active", true)
		r.Set("role", 15)
	})
	state := mustSave(t, app, "states", func(r *core.Record) {
		r.Set("name", "Todo")
		r.Set("color", "#fff")
		r.Set("group", "unstarted")
		r.Set("project", project.Id)
		r.Set("workspace", ws.Id)
	})

	newIssue := func(name string, createdBy *core.Record) *core.Record {
		return mustSave(t, app, "issues", func(r *core.Record) {
			r.Set("name", name)
			r.Set("project", project.Id)
			r.Set("workspace", ws.Id)
			r.Set("state", state.Id)
			r.Set("created_by", createdBy.Id)
			r.Set("sequence_id", 1)
		})
	}

	assigned := newIssue("assigned", other)
	newIssue("created", target)
	subscribed := newIssue("subscribed", other)
	newIssue("unrelated", other)

	mustSave(t, app, "issue_assignees", func(r *core.Record) {
		r.Set("issue", assigned.Id)
		r.Set("assignee", target.Id)
		r.Set("project", project.Id)
		r.Set("workspace", ws.Id)
	})
	mustSave(t, app, "issue_subscribers", func(r *core.Record) {
		r.Set("issue", subscribed.Id)
		r.Set("subscriber", target.Id)
		r.Set("project", project.Id)
		r.Set("workspace", ws.Id)
	})

	h := New(app)
	filter, params := h.userIssuesFilter(url.Values{"sub_issue": {"false"}}, target, []string{project.Id})
	records, err := app.FindRecordsByFilter("issues", filter, "-created_at", 100, 0, params)
	if err != nil {
		t.Fatalf("filter rejected: %v", err)
	}
	got := map[string]bool{}
	for _, r := range records {
		got[r.GetString("name")] = true
	}
	for _, want := range []string{"assigned", "created", "subscribed"} {
		if !got[want] {
			t.Errorf("expected %q to match, got %v", want, got)
		}
	}
	if got["unrelated"] {
		t.Errorf("unrelated issue must not match, got %v", got)
	}

	// The Assigned tab additionally sends `assignees=<target uuid>`, which
	// narrows to the live issue_assignees leg only.
	targetUUID := target.GetString("uuid")
	filter, params = h.userIssuesFilter(url.Values{
		"sub_issue": {"false"},
		"assignees": {targetUUID},
	}, target, []string{project.Id})
	records, err = app.FindRecordsByFilter("issues", filter, "-created_at", 100, 0, params)
	if err != nil {
		t.Fatalf("assignees filter rejected: %v", err)
	}
	if len(records) != 1 || records[0].GetString("name") != "assigned" {
		names := []string{}
		for _, r := range records {
			names = append(names, r.GetString("name"))
		}
		t.Errorf("assignees filter: want [assigned], got %v", names)
	}
}
