package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/pocketbase/pocketbase/core"
)

// Gzip compresses eligible responses on the fly.
//
// Why a middleware instead of nginx: the deployment target is one Go binary
// plus three static directories (see docs/04-DEPLOYMENT.md §4.4). Without this
// the JS/CSS bundles shipped from the binary go out uncompressed, which is
// roughly 3-4x the bytes for Plane's ~1.5 MB web bundle.
//
// What is compressed:
//   - only when the client sent `Accept-Encoding: gzip` (q>0), and
//   - only text-ish payloads (HTML, JS, CSS, JSON, SVG, XML, WASM), and
//   - only when the body is at least gzipMinSize bytes.
//
// What is never touched:
//   - `Range` requests: the byte offsets would no longer line up with the
//     encoded stream, and static files here are already immutable.
//   - bodies that already carry a Content-Encoding (double compression).
//   - 1xx/204/304 responses (no body).
//   - `text/event-stream`: PocketBase's /api/realtime SSE must reach the
//     client unbuffered, and gzip would add a framing layer in between.
//   - already-compressed media (images, woff2, video, zip).
//
// The body is buffered up to gzipMinSize before the decision is made, so a
// handler that sets Content-Length and writes a small body is unaffected: the
// bytes are replayed verbatim with the original header intact.
//
// PLANE_GZIP=0 disables the middleware entirely (see router.Register).
func Gzip() func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if !acceptsGzip(e.Request) {
			return e.Next()
		}

		gw := &gzipWriter{inner: e.Response}
		e.Response = gw

		// Close is idempotent: this only does anything if the handler panicked
		// before the explicit Close below (a gzip stream without its trailer is
		// undecodable, so terminating it matters even on the panic path).
		defer func() { _ = gw.Close() }()

		err := e.Next()
		if err != nil {
			// tools/router.ErrorHandler writes the error envelope to the
			// *original* writer, so anything still buffered here would end up
			// in front of the JSON error. Drop it and let the router answer.
			gw.Discard()
			return err
		}

		return gw.Close()
	}
}

// gzipMinSize is the smallest body worth compressing. Below this the gzip
// header/trailer (18 bytes) plus the CPU cost buys nothing, and small JSON
// errors are the bulk of the responses on this API.
const gzipMinSize = 1024

// gzipWriter states.
const (
	gzipUndecided   = iota // buffering, no bytes sent yet
	gzipCompressing        // header sent with Content-Encoding: gzip
	gzipPassthrough        // header sent without compression
	gzipDone
)

// gzipWriter defers the compress/don't-compress decision until it has seen
// either gzipMinSize bytes or the end of the response.
//
// It intentionally does not implement Written()/Status(): tools/router's
// getWritten/getStatus walk the RWUnwrapper chain, so reporting through the
// wrapped *router.ResponseWriter keeps a single source of truth.
type gzipWriter struct {
	inner http.ResponseWriter
	buf   bytes.Buffer
	gz    *gzip.Writer

	state       int
	status      int
	wroteHeader bool
}

func (w *gzipWriter) Header() http.Header { return w.inner.Header() }

// Unwrap lets http.ResponseController and tools/router look through this
// wrapper (flush, status tracking).
func (w *gzipWriter) Unwrap() http.ResponseWriter { return w.inner }

func (w *gzipWriter) WriteHeader(status int) {
	if w.wroteHeader || w.status != 0 {
		return
	}
	w.status = status

	// Bodyless statuses must not be wrapped: the header goes out as-is.
	if status < 200 || status == http.StatusNoContent || status == http.StatusNotModified {
		w.state = gzipPassthrough
		w.flushHeader()
	}
}

func (w *gzipWriter) Write(p []byte) (int, error) {
	switch w.state {
	case gzipPassthrough:
		return w.inner.Write(p)
	case gzipCompressing:
		return w.gz.Write(p)
	case gzipDone:
		return w.inner.Write(p)
	}

	// A Content-Type the handler set explicitly can rule compression out
	// before we buffer anything. An empty one is decided later, from the
	// sniffed bytes, exactly like net/http would do.
	if !compressibleContentType(w.inner.Header().Get("Content-Type")) || w.wroteHeader {
		w.state = gzipPassthrough
		w.flushHeader()
		return w.inner.Write(p)
	}

	w.buf.Write(p)
	if w.buf.Len() >= gzipMinSize {
		if err := w.startCompressing(); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// startCompressing commits to gzip and flushes the buffered prefix.
func (w *gzipWriter) startCompressing() error {
	h := w.inner.Header()

	// Something upstream already encoded the body; gzipping again would
	// produce a stream the client cannot decode.
	if h.Get("Content-Encoding") != "" {
		w.state = gzipPassthrough
		w.flushHeader()
		_, err := w.inner.Write(w.buf.Bytes())
		w.buf.Reset()
		return err
	}

	if h.Get("Content-Type") == "" {
		ct := http.DetectContentType(w.buf.Bytes())
		if !compressibleContentType(ct) {
			w.state = gzipPassthrough
			w.flushHeader()
			_, err := w.inner.Write(w.buf.Bytes())
			w.buf.Reset()
			return err
		}
		h.Set("Content-Type", ct)
	}

	// The encoded body has a different length, and byte ranges no longer map
	// onto it.
	h.Del("Content-Length")
	h.Del("Accept-Ranges")
	h.Set("Content-Encoding", "gzip")
	addVary(h, "Accept-Encoding")

	w.state = gzipCompressing
	w.flushHeader()

	w.gz = gzipPool.Get().(*gzip.Writer)
	w.gz.Reset(w.inner)

	_, err := w.gz.Write(w.buf.Bytes())
	w.buf.Reset()
	return err
}

// Close finishes the response. Safe to call more than once.
func (w *gzipWriter) Close() error {
	switch w.state {
	case gzipCompressing:
		w.state = gzipDone
		err := w.gz.Close()
		gzipPool.Put(w.gz)
		w.gz = nil
		return err
	case gzipPassthrough, gzipDone:
		return nil
	}

	// Still undecided: the body was shorter than gzipMinSize, or the handler
	// wrote no body at all. Replay it untouched.
	w.state = gzipDone
	if w.status == 0 && w.buf.Len() == 0 {
		return nil
	}

	h := w.inner.Header()
	if h.Get("Content-Length") == "" && w.buf.Len() > 0 {
		h.Set("Content-Length", strconv.Itoa(w.buf.Len()))
	}
	w.flushHeader()
	_, err := w.inner.Write(w.buf.Bytes())
	return err
}

// Discard abandons the buffered body. Used when the handler returned an error
// and the router is about to write its own envelope.
func (w *gzipWriter) Discard() {
	if w.state == gzipCompressing {
		// The header is already on the wire; terminate the stream cleanly.
		_ = w.Close()
		return
	}
	w.buf.Reset()
	w.state = gzipDone
}

// Flush implements http.Flusher so streaming handlers keep working.
func (w *gzipWriter) Flush() {
	switch w.state {
	case gzipUndecided:
		if w.buf.Len() > 0 {
			_ = w.startCompressing()
		} else {
			// Nothing to compress yet and the header is about to be sent:
			// Content-Encoding can no longer be added afterwards.
			w.state = gzipPassthrough
			w.flushHeader()
		}
	case gzipCompressing:
		_ = w.gz.Flush()
	}
	_ = http.NewResponseController(w.inner).Flush()
}

func (w *gzipWriter) flushHeader() {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.inner.WriteHeader(w.status)
}

var gzipPool = sync.Pool{
	New: func() any {
		// Default compression: best trade-off for JS/CSS bundles. The writer
		// is pooled because each one holds ~600 KB of deflate buffers.
		return gzip.NewWriter(io.Discard)
	},
}

// acceptsGzip reports whether the request may be answered with a gzip body.
func acceptsGzip(r *http.Request) bool {
	// A Range request is served by http.ServeContent byte-for-byte; encoding
	// it would break the offsets the client asked for.
	if r.Header.Get("Range") != "" {
		return false
	}
	return acceptsEncoding(r.Header.Get("Accept-Encoding"), "gzip")
}

// acceptsEncoding parses an Accept-Encoding header and reports whether the
// given coding is acceptable (present, or covered by `*`, with q > 0).
func acceptsEncoding(header, coding string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		name, params, _ := strings.Cut(part, ";")
		name = strings.ToLower(strings.TrimSpace(name))
		if name != coding && name != "*" {
			continue
		}

		// Only `q=` matters here; `gzip;q=0` means "explicitly not wanted".
		if q, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			if v, err := strconv.ParseFloat(strings.TrimSpace(q), 64); err == nil && v <= 0 {
				continue
			}
		}
		return true
	}
	return false
}

// compressibleContentType reports whether a body of this media type is worth
// gzipping. An empty value means "not decided yet" and is treated as eligible,
// because the caller will sniff it before committing.
func compressibleContentType(ct string) bool {
	if ct == "" {
		return true
	}

	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	}

	// SSE is a stream: gzip would add a framing layer between the event and
	// the client, and proxies buffer it differently.
	if mt == "text/event-stream" {
		return false
	}
	if strings.HasPrefix(mt, "text/") {
		return true
	}

	switch mt {
	case "application/json",
		"application/ld+json",
		"application/manifest+json",
		"application/x-ndjson",
		"application/xml",
		"application/xhtml+xml",
		"application/rss+xml",
		"application/atom+xml",
		"application/javascript",
		"application/x-javascript",
		"application/ecmascript",
		"application/wasm",
		"image/svg+xml":
		return true
	}

	// Everything else (images, fonts, video, zip, pdf, octet-stream) is
	// either already compressed or not worth the CPU.
	return false
}

// addVary appends a value to Vary unless it is already listed, so a cache in
// front of this server keeps the encoded and identity variants apart.
func addVary(h http.Header, value string) {
	for _, line := range h.Values("Vary") {
		for _, token := range strings.Split(line, ",") {
			if strings.EqualFold(strings.TrimSpace(token), value) {
				return
			}
		}
	}
	h.Add("Vary", value)
}
