package issues

import (
	"net/http"
	"testing"
)

// TestDetailByIdentifier covers GET /api/workspaces/{slug}/work-items/{ref}/
// (IssueDetailIdentifierEndpoint): exact reference, case-insensitive
// identifier, unknown sequence → 404.
func TestDetailByIdentifier(t *testing.T) {
	f := newWave2Fixture(t)
	h := New(f.app)

	e, rec := f.event(t, http.MethodGet, "/", "", map[string]string{"ref": "P-1"})
	if err := h.DetailByIdentifier(e); err != nil {
		t.Fatalf("work-items ref: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("work-items ref status %d body %s", rec.Code, rec.Body.String())
	}
	if decodeObject(t, rec)["id"] != f.issueUUID() {
		t.Fatalf("work-items ref must resolve to the sequence-1 issue: %s", rec.Body.String())
	}

	// Case-insensitive identifier ("p-1" == "P-1").
	e, rec = f.event(t, http.MethodGet, "/", "", map[string]string{"ref": "p-1"})
	if err := h.DetailByIdentifier(e); err != nil {
		t.Fatalf("work-items lowercase: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("work-items lowercase status %d", rec.Code)
	}

	// Unknown sequence, unknown identifier, malformed reference → 404.
	for _, bad := range []string{"P-99", "ZZ-1", "P"} {
		e, rec = f.event(t, http.MethodGet, "/", "", map[string]string{"ref": bad})
		if err := h.DetailByIdentifier(e); err != nil {
			t.Fatalf("work-items %q: %v", bad, err)
		}
		if rec.Code != http.StatusNotFound {
			t.Fatalf("work-items %q status %d", bad, rec.Code)
		}
	}
}

// TestListV2 proves the v2 paginated list returns the same envelope as the
// plain list — both fixture issues, real content asserted.
func TestListV2(t *testing.T) {
	f := newWave2Fixture(t)
	h := New(f.app)

	e, rec := f.event(t, http.MethodGet, "/", "", nil)
	if err := h.ListV2(e); err != nil {
		t.Fatalf("v2 list: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("v2 list status %d", rec.Code)
	}
	out := decodeObject(t, rec)
	if out["total_count"].(float64) != 2 {
		t.Fatalf("v2 list total_count: %s", rec.Body.String())
	}
	results, _ := out["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("v2 list results: %s", rec.Body.String())
	}
	seen := map[string]bool{}
	for _, r := range results {
		seen[r.(map[string]any)["id"].(string)] = true
	}
	if !seen[f.issueUUID()] || !seen[f.relateUUID()] {
		t.Fatalf("v2 list must contain both fixture issues: %s", rec.Body.String())
	}
}
