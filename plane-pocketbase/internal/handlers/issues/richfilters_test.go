package issues

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// TestListRichFilters exercises the `?filters=<json>` expression the web
// client sends on every issue board (computedFilteredParams serialises the
// whole TWorkItemFilterExpression tree). Before the rich-filter evaluator this
// parameter was silently ignored and boards showed unfiltered results.
func TestListRichFilters(t *testing.T) {
	f := newWave2Fixture(t)
	h := &Handler{App: f.app}

	urgent := mustSave(t, f.app, "issues", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-0000000000c1")
		r.Set("name", "Urgent")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
		r.Set("state", f.state.Id)
		r.Set("created_by", f.user.Id)
		r.Set("sequence_id", 10)
		r.Set("priority", "urgent")
		r.Set("target_date", "2026-03-15 00:00:00.000Z")
	})
	high := mustSave(t, f.app, "issues", func(r *core.Record) {
		r.Set("uuid", "00000000-0000-0000-0000-0000000000c2")
		r.Set("name", "High")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
		r.Set("state", f.state.Id)
		r.Set("created_by", f.other.Id)
		r.Set("sequence_id", 11)
		r.Set("priority", "high")
		r.Set("target_date", "2026-04-20 00:00:00.000Z")
	})

	assignee := mustSave(t, f.app, "issue_assignees", func(r *core.Record) {
		r.Set("issue", urgent.Id)
		r.Set("assignee", f.other.Id)
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
	})
	_ = assignee
	label := mustSave(t, f.app, "labels", func(r *core.Record) {
		r.Set("name", "Bug")
		r.Set("color", "#f00")
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
	})
	mustSave(t, f.app, "issue_labels", func(r *core.Record) {
		r.Set("issue", high.Id)
		r.Set("label", label.Id)
		r.Set("project", f.project.Id)
		r.Set("workspace", f.workspace.Id)
	})

	issueUUID := func(rec *core.Record) string { return rec.GetString("uuid") }

	fetch := func(t *testing.T, rawQuery string) map[string]bool {
		t.Helper()
		e, rec := f.event(t, http.MethodGet, "/?"+rawQuery, "", nil)
		if err := h.List(e); err != nil {
			t.Fatalf("list %s: %v", rawQuery, err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("list %s status %d body %s", rawQuery, rec.Code, rec.Body.String())
		}
		out := decodeObject(t, rec)
		results, _ := out["results"].([]any)
		got := map[string]bool{}
		for _, item := range results {
			m, _ := item.(map[string]any)
			got[m["id"].(string)] = true
		}
		return got
	}
	has := func(m map[string]bool, rec *core.Record) bool { return m[issueUUID(rec)] }

	// priority__in with comma-joined values.
	got := fetch(t, fmt.Sprintf("filters=%s", urlEncode(`{"priority__in":"urgent,high"}`)))
	if len(got) != 2 || !has(got, urgent) || !has(got, high) {
		t.Fatalf("priority__in: want 2, got %v", got)
	}

	// Flat single-condition map.
	got = fetch(t, fmt.Sprintf("filters=%s", urlEncode(fmt.Sprintf(`{"assignee_id__exact":"%s"}`, f.other.GetString("uuid")))))
	if len(got) != 1 || !has(got, urgent) {
		t.Fatalf("assignee_id__exact: want only Urgent, got %v", got)
	}

	// AND group over two conditions (assignee + label → disjoint sets → empty).
	got = fetch(t, fmt.Sprintf("filters=%s", urlEncode(fmt.Sprintf(
		`{"and":[{"assignee_id__exact":"%s"},{"label_id__in":"%s"}]}`,
		f.other.GetString("uuid"), label.GetString("uuid")))))
	if len(got) != 0 {
		t.Fatalf("and-group disjoint: want 0, got %v", got)
	}

	// AND group over intersecting conditions.
	got = fetch(t, fmt.Sprintf("filters=%s", urlEncode(fmt.Sprintf(
		`{"and":[{"priority__in":"high"},{"label_id__in":"%s"}]}`,
		label.GetString("uuid")))))
	if len(got) != 1 || !has(got, high) {
		t.Fatalf("and-group intersect: want only High, got %v", got)
	}

	// state_group__in.
	got = fetch(t, fmt.Sprintf("filters=%s", urlEncode(`{"state_group__in":"unstarted"}`)))
	if len(got) != 4 {
		t.Fatalf("state_group__in: want 4 (fixture 2 + seeded 2), got %v", got)
	}

	// Date range over target_date.
	got = fetch(t, fmt.Sprintf("filters=%s", urlEncode(`{"target_date__range":"2026-03-01,2026-03-31"}`)))
	if len(got) != 1 || !has(got, urgent) {
		t.Fatalf("target_date__range: want only Urgent, got %v", got)
	}

	// created_by_id via the bridge-free path.
	got = fetch(t, fmt.Sprintf("filters=%s", urlEncode(fmt.Sprintf(`{"created_by_id__in":"%s"}`, f.user.GetString("uuid")))))
	if len(got) != 3 || !has(got, urgent) || !has(got, f.issue) {
		t.Fatalf("created_by_id__in: want 3, got %v", got)
	}

	// Invalid JSON is ignored, not an error.
	got = fetch(t, "filters=%7Bbroken")
	if len(got) != 4 {
		t.Fatalf("invalid filters ignored: want 4, got %v", got)
	}
}

// urlEncode is a minimal query-value escaper (the fixtures' payloads contain
// only characters Go's url.QueryEscape would leave readable, but keep the
// explicit escaping for the JSON braces).
func urlEncode(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~', c == '"', c == '{', c == '}',
			c == ',', c == ':':
			out = append(out, c)
		case c == ' ':
			out = append(out, '+')
		default:
			out = append(out, '%')
			const hex = "0123456789ABCDEF"
			out = append(out, hex[c>>4], hex[c&0x0f])
		}
	}
	return string(out)
}
