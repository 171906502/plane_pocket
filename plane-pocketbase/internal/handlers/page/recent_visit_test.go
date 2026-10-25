package page

import (
	"net/http"
	"testing"

	"github.com/pocketbase/dbx"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// TestDetailRecordsPageVisit: a successful page detail GET upserts one
// user_recent_visits row with entity_name=page and the page's Plane UUID;
// an immediate revisit stays one row with an untouched visited_at, and a
// 404 (private page seen by a non-owner) records nothing. (tasklist L-7)
func TestDetailRecordsPageVisit(t *testing.T) {
	f := newPageFixture(t)
	h := New(f.app)
	pageUUID := f.publicPage.GetString("uuid")

	// 1. First successful fetch: exactly one row, wired to the page UUID.
	e, rec := f.event(t, f.member, http.MethodGet, "/pages/"+pageUUID+"/", "")
	e.Request.SetPathValue("page_id", pageUUID)
	if err := h.Detail(e); err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("Detail: status = %d (body %s)", rec.Code, rec.Body.String())
	}
	rows, err := f.app.FindRecordsByFilter(
		"user_recent_visits",
		"user = {:user} && entity_name = 'page'",
		"", 0, 0,
		dbx.Params{"user": f.member.Id},
	)
	if err != nil {
		t.Fatalf("visit rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("visit rows = %d, want 1", len(rows))
	}
	if got := rows[0].GetString("entity_identifier"); got != pageUUID {
		t.Fatalf("entity_identifier = %s, want page uuid %s", got, pageUUID)
	}
	if got := rows[0].GetString("project"); got != f.project.Id {
		t.Fatalf("project = %s, want %s", got, f.project.Id)
	}
	visitedAt := rows[0].GetDateTime("visited_at").String()

	// 2. Revisit inside the throttle window: still one row, visited_at moved
	// by neither a new row nor an update.
	e, rec = f.event(t, f.member, http.MethodGet, "/pages/"+pageUUID+"/", "")
	e.Request.SetPathValue("page_id", pageUUID)
	if err := h.Detail(e); err != nil {
		t.Fatalf("Detail(revisit): %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("Detail(revisit): status = %d", rec.Code)
	}
	rows, _ = f.app.FindRecordsByFilter(
		"user_recent_visits",
		"user = {:user} && entity_name = 'page'",
		"", 0, 0,
		dbx.Params{"user": f.member.Id},
	)
	if len(rows) != 1 {
		t.Fatalf("visit rows after revisit = %d, want 1", len(rows))
	}
	if after := rows[0].GetDateTime("visited_at").String(); after != visitedAt {
		t.Fatalf("revisit inside throttle moved visited_at: %s -> %s", visitedAt, after)
	}

	// 3. A fetch that 404s (private page, non-owner) records nothing: the
	// fixture starts clean for the owner-less private path.
	privateUUID := f.privatePage.GetString("uuid")
	e, rec = f.event(t, f.member, http.MethodGet, "/pages/"+privateUUID+"/", "")
	e.Request.SetPathValue("page_id", privateUUID)
	if err := h.Detail(e); err != nil {
		t.Fatalf("Detail(private): %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Detail(private): status = %d, want 404", rec.Code)
	}
	rows, _ = f.app.FindRecordsByFilter(
		"user_recent_visits",
		"user = {:user}",
		"", 0, 0,
		dbx.Params{"user": f.member.Id},
	)
	if len(rows) != 1 {
		t.Fatalf("visit rows after 404 = %d, want 1 (the 404 must not write)", len(rows))
	}

	// 4. The read side resolves entity_data through the same UUID the visit
	// recorded (serializer sanity on the written row).
	if got := serializers.UUID(f.publicPage); got != pageUUID {
		t.Fatalf("serializer UUID drift: %s vs %s", got, pageUUID)
	}
}
