package issues

import (
	"net/http"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// The Wave-3 method-gap handlers, per internal/handlers/issues/wave3.go. All
// content assertions seed rows first: a 200 with an empty array proves nothing
// (the exact trap documented in docs/07 §4 conclusion 4).

// TestSubIssues covers GET (list + state_distribution) and POST (re-parent +
// echo of the updated children) for …/issues/{issue_id}/sub-issues/.
func TestSubIssues(t *testing.T) {
	f := newWave2Fixture(t)
	h := New(f.app)

	// Seed: `related` becomes a child of `issue` via parent.
	f.related.Set("parent", f.issue.Id)
	if err := f.app.Save(f.related); err != nil {
		t.Fatalf("seed parent: %v", err)
	}

	e, rec := f.event(t, http.MethodGet, "/", "", map[string]string{"issue_id": f.issueUUID()})
	if err := h.SubIssues(e); err != nil {
		t.Fatalf("sub-issues GET: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("sub-issues GET status %d body %s", rec.Code, rec.Body.String())
	}
	out := decodeObject(t, rec)
	rows, _ := out["sub_issues"].([]any)
	if len(rows) != 1 {
		t.Fatalf("sub_issues: want 1 row, got %s", rec.Body.String())
	}
	if rows[0].(map[string]any)["id"] != f.relateUUID() {
		t.Fatalf("sub_issues[0].id: want %s got %s", f.relateUUID(), rec.Body.String())
	}
	dist, _ := out["state_distribution"].(map[string]any)
	if len(dist["unstarted"].([]any)) != 1 {
		t.Fatalf("state_distribution: want 1 unstarted id, got %s", rec.Body.String())
	}

	// POST re-parents a standalone issue under `issue`.
	standalone := mustSave(t, f.app, "issues", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-0000000000b3")
		r.Set("name", "Standalone")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
		r.Set("state", f.state.Id)
		r.Set("created_by", f.user.Id)
		r.Set("sequence_id", 2)
	})
	body := `{"sub_issue_ids": ["` + standalone.GetString("uuid") + `"]}`
	e, rec = f.event(t, http.MethodPost, "/", body, map[string]string{"issue_id": f.issueUUID()})
	if err := h.AddSubIssues(e); err != nil {
		t.Fatalf("sub-issues POST: %v", err)
	}
	// The handler re-parents a fresh record instance — re-read from the DB
	// before asserting (the local `standalone` is stale after the POST).
	if rec.Code != http.StatusOK {
		t.Fatalf("sub-issues POST status %d body %s", rec.Code, rec.Body.String())
	}
	standalone, err := f.app.FindRecordById("issues", standalone.Id)
	if err != nil {
		t.Fatalf("re-read standalone: %v", err)
	}
	if standalone.GetString("parent") != f.issue.Id {
		t.Fatalf("re-parent failed: parent=%q", standalone.GetString("parent"))
	}

	// Empty id list is a 400 with Django's message; unknown parent is a 404.
	e, rec = f.event(t, http.MethodPost, "/", `{}`, map[string]string{"issue_id": f.issueUUID()})
	if err := h.AddSubIssues(e); err != nil {
		t.Fatalf("sub-issues POST empty: %v", err)
	}
	if rec.Code != http.StatusBadRequest || decodeObject(t, rec)["error"] != "Sub Issue IDs are required" {
		t.Fatalf("empty ids: %d %s", rec.Code, rec.Body.String())
	}
	e, rec = f.event(t, http.MethodPost, "/", body, map[string]string{"issue_id": "00000000-0000-0000-0000-0000000000ff"})
	if err := h.AddSubIssues(e); err != nil {
		t.Fatalf("sub-issues POST missing parent: %v", err)
	}
	if rec.Code != http.StatusNotFound || decodeObject(t, rec)["error"] != "Parent issue not found" {
		t.Fatalf("missing parent: %d %s", rec.Code, rec.Body.String())
	}
}

// TestIssueLinks covers list/create/duplicate-rejection/patch/delete for
// …/issues/{issue_id}/issue-links/.
func TestIssueLinks(t *testing.T) {
	f := newWave2Fixture(t)
	h := New(f.app)

	e, rec := f.event(t, http.MethodPost, "/", `{"url": "example.com/doc", "title": "Doc"}`, map[string]string{"issue_id": f.issueUUID()})
	if err := h.CreateIssueLink(e); err != nil {
		t.Fatalf("create link: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("create link status %d body %s", rec.Code, rec.Body.String())
	}
	created := decodeObject(t, rec)
	if created["url"] != "http://example.com/doc" {
		t.Fatalf("url scheme prefix missing: %s", rec.Body.String())
	}

	// Same URL again → Django's duplicate message.
	e, rec = f.event(t, http.MethodPost, "/", `{"url": "example.com/doc"}`, map[string]string{"issue_id": f.issueUUID()})
	if err := h.CreateIssueLink(e); err != nil {
		t.Fatalf("duplicate link: %v", err)
	}
	if rec.Code != http.StatusBadRequest || decodeObject(t, rec)["error"] != "URL already exists for this Issue" {
		t.Fatalf("duplicate url: %d %s", rec.Code, rec.Body.String())
	}

	e, rec = f.event(t, http.MethodGet, "/", "", map[string]string{"issue_id": f.issueUUID()})
	if err := h.ListIssueLinks(e); err != nil {
		t.Fatalf("list links: %v", err)
	}
	rows := decodeArray(t, rec)
	if len(rows) != 1 {
		t.Fatalf("list links: want 1 row, got %s", rec.Body.String())
	}
	linkID, _ := rows[0].(map[string]any)["id"].(string)

	e, rec = f.event(t, http.MethodPatch, "/", `{"title": "Docs"}`, map[string]string{"issue_id": f.issueUUID(), "pk": linkID})
	if err := h.IssueLinkDetail(e); err != nil {
		t.Fatalf("patch link: %v", err)
	}
	if rec.Code != http.StatusOK || decodeObject(t, rec)["title"] != "Docs" {
		t.Fatalf("patch link: %d %s", rec.Code, rec.Body.String())
	}

	e, rec = f.event(t, http.MethodDelete, "/", "", map[string]string{"issue_id": f.issueUUID(), "pk": linkID})
	if err := h.IssueLinkDetail(e); err != nil {
		t.Fatalf("delete link: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete link: %d %s", rec.Code, rec.Body.String())
	}
}

// TestIssueArchive covers the single-issue archive round trip: archive rejects
// non-terminal states, archives a completed issue, retrieves it, then
// unarchives back to 204.
func TestIssueArchive(t *testing.T) {
	f := newWave2Fixture(t)
	h := New(f.app)

	// Non-terminal state → 400 with Django's message.
	e, rec := f.event(t, http.MethodPost, "/", "", map[string]string{"pk": f.issueUUID()})
	if err := h.ArchiveIssue(e); err != nil {
		t.Fatalf("archive non-terminal: %v", err)
	}
	if rec.Code != http.StatusBadRequest || decodeObject(t, rec)["error"] != "Can only archive completed or cancelled state group issue" {
		t.Fatalf("archive non-terminal: %d %s", rec.Code, rec.Body.String())
	}

	done := mustSave(t, f.app, "states", func(r *core.Record) {
		r.Set("name", "Done")
		r.Set("color", "#0f0")
		r.Set("group", "completed")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
	})
	f.issue.Set("state", done.Id)
	if err := f.app.Save(f.issue); err != nil {
		t.Fatalf("move to done: %v", err)
	}

	e, rec = f.event(t, http.MethodPost, "/", "", map[string]string{"pk": f.issueUUID()})
	if err := h.ArchiveIssue(e); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if rec.Code != http.StatusOK || decodeObject(t, rec)["archived_at"] == nil {
		t.Fatalf("archive: %d %s", rec.Code, rec.Body.String())
	}

	// GET detail only answers for archived rows; unarchive clears and 204s.
	e, rec = f.event(t, http.MethodGet, "/", "", map[string]string{"pk": f.issueUUID()})
	if err := h.ArchivedIssueDetail(e); err != nil {
		t.Fatalf("archived detail: %v", err)
	}
	if rec.Code != http.StatusOK || decodeObject(t, rec)["id"] != f.issueUUID() {
		t.Fatalf("archived detail: %d %s", rec.Code, rec.Body.String())
	}

	e, rec = f.event(t, http.MethodDelete, "/", "", map[string]string{"pk": f.issueUUID()})
	if err := h.UnarchiveIssue(e); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unarchive: %d %s", rec.Code, rec.Body.String())
	}
	e, rec = f.event(t, http.MethodGet, "/", "", map[string]string{"pk": f.issueUUID()})
	if err := h.ArchivedIssueDetail(e); err != nil {
		t.Fatalf("archived detail after unarchive: %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("archived detail after unarchive: %d %s", rec.Code, rec.Body.String())
	}
}

// TestListDetailExpansion proves …/issues-detail/ returns the same envelope as
// /issues/ plus the per-issue `issue_relation` array for declared relations.
func TestListDetailExpansion(t *testing.T) {
	f := newWave2Fixture(t)
	h := New(f.app)

	mustSave(t, f.app, "issue_relations", func(r *core.Record) {
		r.Set("issue", f.issue.Id)
		r.Set("related_issue", f.related.Id)
		r.Set("relation_type", "blocking")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
		r.Set("created_by", f.user.Id)
	})

	e, rec := f.event(t, http.MethodGet, "/", "", nil)
	if err := h.ListDetail(e); err != nil {
		t.Fatalf("issues-detail: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("issues-detail status %d body %s", rec.Code, rec.Body.String())
	}
	out := decodeObject(t, rec)
	results, _ := out["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("issues-detail results: want 2 (issue + related), got %s", rec.Body.String())
	}
	var first map[string]any
	for _, r := range results {
		if m := r.(map[string]any); m["id"] == f.issueUUID() {
			first = m
			break
		}
	}
	if first == nil {
		t.Fatalf("current issue missing from issues-detail: %s", rec.Body.String())
	}
	rels, _ := first["issue_relation"].([]any)
	if len(rels) != 1 {
		t.Fatalf("issue_relation: want 1 entry, got %s", rec.Body.String())
	}
	rel := rels[0].(map[string]any)
	if rel["id"] != f.relateUUID() || rel["relation_type"] != "blocking" {
		t.Fatalf("issue_relation entry: %s", rec.Body.String())
	}

	// Plain list stays relation-free.
	e, rec = f.event(t, http.MethodGet, "/", "", nil)
	if err := h.List(e); err != nil {
		t.Fatalf("list: %v", err)
	}
	results = decodeObject(t, rec)["results"].([]any)
	first = nil
	for _, r := range results {
		if m := r.(map[string]any); m["id"] == f.issueUUID() {
			first = m
			break
		}
	}
	if first == nil {
		t.Fatalf("current issue missing from list: %s", rec.Body.String())
	}
	if _, ok := first["issue_relation"]; ok {
		t.Fatalf("plain list must not carry issue_relation: %s", rec.Body.String())
	}
}
