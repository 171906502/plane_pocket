package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

// newEvent builds the minimal RequestEvent Check needs: a request and a
// response recorder. The handler only reads/writes those two fields.
func newEvent(app core.App) (*core.RequestEvent, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: app}
	e.Request = httptest.NewRequest(http.MethodGet, "/health", nil)
	e.Response = rec
	return e, rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return body
}

// TestCheckHealthy asserts the exact success shape: 200 with status/version/db
// and nothing else (no leaked internals).
func TestCheckHealthy(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	e, rec := newEvent(app)
	if err := New(app).Check(e); err != nil {
		t.Fatalf("Check returned error: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}

	body := decode(t, rec)
	if body["status"] != "ok" {
		t.Errorf(`status = %v, want "ok"`, body["status"])
	}
	if body["db"] != "ok" {
		t.Errorf(`db = %v, want "ok"`, body["db"])
	}
	version, ok := body["version"].(string)
	if !ok || version == "" {
		t.Errorf(`version = %v, want a non-empty string`, body["version"])
	}

	// Exactly these three fields — a probe must not expose internals.
	if len(body) != 3 {
		t.Errorf("body has %d fields (%v), want exactly 3", len(body), body)
	}
	for _, leaked := range []string{"path", "error", "detail", "dir", "dsn", "table"} {
		if _, present := body[leaked]; present {
			t.Errorf("body leaks %q: %v", leaked, body)
		}
	}
}

// TestCheckDatabaseUnavailable closes the app's database connections (the
// equivalent of "the data dir is gone") and asserts the probe degrades to 503
// instead of reporting healthy.
func TestCheckDatabaseUnavailable(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatalf("new test app: %v", err)
	}
	t.Cleanup(app.Cleanup)

	// Sanity: healthy before we break it.
	e, rec := newEvent(app)
	if err := New(app).Check(e); err != nil {
		t.Fatalf("precondition Check error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("precondition status = %d, want 200", rec.Code)
	}

	if err := app.ClearBootstrap(); err != nil {
		t.Fatalf("clear bootstrap: %v", err)
	}

	e, rec = newEvent(app)
	if err := New(app).Check(e); err != nil {
		t.Fatalf("Check returned error: %v", err)
	}

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %q)", rec.Code, rec.Body.String())
	}

	body := decode(t, rec)
	if body["status"] != "degraded" {
		t.Errorf(`status = %v, want "degraded"`, body["status"])
	}
	if body["db"] != "error" {
		t.Errorf(`db = %v, want "error"`, body["db"])
	}
}

// TestVersionNeverPanics guards the "must not panic; fall back to unknown"
// requirement.
func TestVersionNeverPanics(t *testing.T) {
	if v := Version(); v == "" {
		t.Fatal("Version() returned empty string; want non-empty (unknown fallback)")
	}
}
