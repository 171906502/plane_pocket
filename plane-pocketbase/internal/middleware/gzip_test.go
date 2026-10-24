package middleware

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// payload is comfortably above gzipMinSize and compresses well, like a JS
// bundle or a long issue list.
var payload = []byte(strings.Repeat(`{"id":"issue_1","title":"compression"},`, 200))

// testGzipID mirrors the id router.Register binds the middleware under; a
// distinct id keeps the handler from colliding with the terminal one.
const testGzipID = "planeGzip"

// errBoom stands in for a handler that failed after writing part of a body.
var errBoom = errors.New("boom")

// runGzip pushes a response through the real middleware chain: the gzip
// handler is bound into a hook, followed by the terminal handler under test.
// This exercises the e.Response replacement the same way the router does.
func runGzip(t *testing.T, r *http.Request, handler func(*core.RequestEvent) error) *httptest.ResponseRecorder {
	t.Helper()

	h := &hook.Hook[*core.RequestEvent]{}
	h.Bind(&hook.Handler[*core.RequestEvent]{Id: testGzipID, Func: Gzip(), Priority: -100})

	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: nil}
	e.Request = r
	e.Response = rec

	if err := h.Trigger(e, handler); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	return rec
}

func gzipRequest(method, target string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.Header.Set("Accept-Encoding", "gzip, deflate")
	return r
}

// TestGzipCompressesLargeJSON pins the happy path: the client gets a gzip
// body that decodes to the original bytes, Content-Length is dropped and Vary
// is advertised so caches keep the variants apart.
func TestGzipCompressesLargeJSON(t *testing.T) {
	rec := runGzip(t, gzipRequest(http.MethodGet, "/api/issues"), func(e *core.RequestEvent) error {
		e.Response.Header().Set("Content-Type", "application/json")
		e.Response.Header().Set("Content-Length", "999999")
		_, err := e.Response.Write(payload)
		return err
	})

	if got := rec.Code; got != http.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "" {
		t.Errorf("Content-Length = %q, want removed (length changed)", got)
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Errorf("Vary = %q, want Accept-Encoding", got)
	}
	if rec.Body.Len() >= len(payload) {
		t.Errorf("body did not shrink: %d >= %d", rec.Body.Len(), len(payload))
	}

	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("round-trip mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}

// TestGzipSkipsSmallBodies pins the size threshold: a short response must be
// replayed verbatim, and Content-Length must be filled in so net/http does not
// fall back to chunked encoding for every error envelope.
func TestGzipSkipsSmallBodies(t *testing.T) {
	body := []byte(`{"detail":"Not found."}`)
	rec := runGzip(t, gzipRequest(http.MethodGet, "/api/issues/x"), func(e *core.RequestEvent) error {
		e.Response.Header().Set("Content-Type", "application/json")
		e.Response.WriteHeader(http.StatusNotFound)
		_, err := e.Response.Write(body)
		return err
	})

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want none", got)
	}
	if got := rec.Body.String(); got != string(body) {
		t.Errorf("body = %q, want %q", got, body)
	}
	if got, want := rec.Header().Get("Content-Length"), strconv.Itoa(len(body)); got != want {
		t.Errorf("Content-Length = %q, want %s", got, want)
	}
}

// TestGzipSkipsNonCompressibleTypes pins the media-type filter: a PNG served
// from the static bundle must not be re-encoded.
func TestGzipSkipsNonCompressibleTypes(t *testing.T) {
	rec := runGzip(t, gzipRequest(http.MethodGet, "/assets/logo.png"), func(e *core.RequestEvent) error {
		e.Response.Header().Set("Content-Type", "image/png")
		_, err := e.Response.Write(bytes.Repeat([]byte{0x89}, len(payload)))
		return err
	})

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want none", got)
	}
	if rec.Body.Len() != len(payload) {
		t.Errorf("body length = %d, want %d", rec.Body.Len(), len(payload))
	}
}

// TestGzipSniffsContentType covers handlers that write without setting a
// Content-Type: the decision has to come from the bytes themselves.
func TestGzipSniffsContentType(t *testing.T) {
	rec := runGzip(t, gzipRequest(http.MethodGet, "/api/raw"), func(e *core.RequestEvent) error {
		_, err := e.Response.Write(payload)
		return err
	})

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("Content-Type = %q, want a sniffed text/plain", got)
	}
}

// TestGzipLeavesBodylessStatusesAlone pins 204/304: no body, no encoding, and
// the status must survive the wrapper.
func TestGzipLeavesBodylessStatusesAlone(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotModified} {
		rec := runGzip(t, gzipRequest(http.MethodGet, "/api/thing"), func(e *core.RequestEvent) error {
			e.Response.WriteHeader(status)
			return nil
		})

		if rec.Code != status {
			t.Errorf("status = %d, want %d", rec.Code, status)
		}
		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("status %d: Content-Encoding = %q, want none", status, got)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("status %d: body = %q, want empty", status, rec.Body.String())
		}
	}
}

// TestGzipHonoursRangeAndAcceptEncoding pins the two request-side guards.
func TestGzipHonoursRangeAndAcceptEncoding(t *testing.T) {
	handler := func(e *core.RequestEvent) error {
		e.Response.Header().Set("Content-Type", "application/json")
		_, err := e.Response.Write(payload)
		return err
	}

	withRange := gzipRequest(http.MethodGet, "/assets/app.js")
	withRange.Header.Set("Range", "bytes=0-99")
	if rec := runGzip(t, withRange, handler); rec.Header().Get("Content-Encoding") != "" {
		t.Error("Range request was compressed")
	}

	noGzip := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	if rec := runGzip(t, noGzip, handler); rec.Header().Get("Content-Encoding") != "" {
		t.Error("request without Accept-Encoding was compressed")
	}

	zeroQ := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	zeroQ.Header.Set("Accept-Encoding", "gzip;q=0, deflate")
	if rec := runGzip(t, zeroQ, handler); rec.Header().Get("Content-Encoding") != "" {
		t.Error("gzip;q=0 request was compressed")
	}

	star := httptest.NewRequest(http.MethodGet, "/api/issues", nil)
	star.Header.Set("Accept-Encoding", "*")
	if rec := runGzip(t, star, handler); rec.Header().Get("Content-Encoding") != "gzip" {
		t.Error("Accept-Encoding: * was not treated as gzip-ok")
	}
}

// TestGzipDropsBufferedBodyOnHandlerError pins the error path: the router
// writes its own envelope to the original writer, so a partially buffered
// success body must not leak in front of it.
func TestGzipDropsBufferedBodyOnHandlerError(t *testing.T) {
	h := &hook.Hook[*core.RequestEvent]{}
	h.Bind(&hook.Handler[*core.RequestEvent]{Id: testGzipID, Func: Gzip(), Priority: -100})

	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: nil}
	e.Request = gzipRequest(http.MethodGet, "/api/issues")
	e.Response = rec

	failing := func(e *core.RequestEvent) error {
		e.Response.Header().Set("Content-Type", "application/json")
		_, _ = e.Response.Write([]byte(`{"partial":true}`))
		return errBoom
	}
	if err := h.Trigger(e, failing); err != errBoom {
		t.Fatalf("err = %v, want errBoom", err)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("buffered body leaked: %q", rec.Body.String())
	}
}

// TestAcceptsEncoding table-tests the header parser directly, including the
// shapes browsers and curl actually send.
func TestAcceptsEncoding(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{"", false},
		{"gzip", true},
		{"deflate, gzip", true},
		{"gzip, deflate, br", true},
		{"br;q=1.0, gzip;q=0.8", true},
		{"gzip;q=0", false},
		{"gzip;q=0.0", false},
		{"GZIP", true},
		{"identity", false},
		{"*", true},
		{"*;q=0", false},
		{"deflate, *;q=0.1", true},
	}
	for _, c := range cases {
		if got := acceptsEncoding(c.header, "gzip"); got != c.want {
			t.Errorf("acceptsEncoding(%q) = %v, want %v", c.header, got, c.want)
		}
	}
}

// TestCompressibleContentType pins the media-type policy, including the SSE
// exclusion that keeps /api/realtime unbuffered.
func TestCompressibleContentType(t *testing.T) {
	yes := []string{
		"text/html; charset=utf-8",
		"text/css",
		"text/javascript",
		"application/javascript",
		"application/json",
		"application/manifest+json",
		"image/svg+xml",
		"application/wasm",
		"application/xml",
		"",
	}
	no := []string{
		"text/event-stream",
		"image/png",
		"image/webp",
		"font/woff2",
		"video/mp4",
		"application/zip",
		"application/pdf",
		"application/octet-stream",
	}
	for _, ct := range yes {
		if !compressibleContentType(ct) {
			t.Errorf("compressibleContentType(%q) = false, want true", ct)
		}
	}
	for _, ct := range no {
		if compressibleContentType(ct) {
			t.Errorf("compressibleContentType(%q) = true, want false", ct)
		}
	}
}
