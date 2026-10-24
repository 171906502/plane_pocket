package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// limitEvent builds a request event for the rate-limit middleware. xff sets
// X-Forwarded-For (when non-empty) and remote sets RemoteAddr (when non-empty);
// httptest supplies its own RemoteAddr otherwise.
func limitEvent(t *testing.T, xff, remote string) (*core.RequestEvent, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/auth/sign-in/", nil)
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	if remote != "" {
		req.RemoteAddr = remote
	}
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{}
	e.Request = req
	e.Response = rec
	return e, rec
}

// hit runs the middleware once and returns the recorder. A nil error means the
// request was forwarded (or answered); the code tells which.
func hit(t *testing.T, mw func(*core.RequestEvent) error, xff, remote string) *httptest.ResponseRecorder {
	t.Helper()
	e, rec := limitEvent(t, xff, remote)
	if err := mw(e); err != nil {
		t.Fatalf("middleware returned error: %v", err)
	}
	return rec
}

// TestRateLimiterBlocksAfterBurst pins the core contract: the first `burst`
// requests pass, the (burst+1)th is answered 429 with a positive integer
// Retry-After header.
func TestRateLimiterBlocksAfterBurst(t *testing.T) {
	cases := []struct {
		name   string
		perMin int
		burst  int
	}{
		{"default 30/min burst 10", 30, 10},
		{"low rate burst 3", 6, 3},
		{"single token", 60, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rl := NewRateLimiter(tc.perMin, tc.burst)
			// Freeze the clock so refill cannot mask the burst boundary.
			rl.now = func() time.Time { return time.Unix(0, 0) }
			mw := rl.Middleware()

			for i := 0; i < tc.burst; i++ {
				if rec := hit(t, mw, "203.0.113.7", ""); rec.Code == http.StatusTooManyRequests {
					t.Fatalf("request %d/%d: want allowed, got 429", i+1, tc.burst)
				}
			}

			rec := hit(t, mw, "203.0.113.7", "")
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("request %d: want 429, got %d", tc.burst+1, rec.Code)
			}
			raw := rec.Header().Get("Retry-After")
			n, err := strconv.Atoi(raw)
			if err != nil {
				t.Fatalf("Retry-After = %q, want a positive integer", raw)
			}
			if n <= 0 {
				t.Fatalf("Retry-After = %d, want > 0", n)
			}
			if body := rec.Body.String(); body == "" || body[0] != '{' {
				t.Fatalf("429 body = %q, want a JSON object", body)
			}
		})
	}
}

// TestRateLimiterPerIPIsolation pins that exhausting one IP's bucket does not
// affect another IP.
func TestRateLimiterPerIPIsolation(t *testing.T) {
	rl := NewRateLimiter(30, 2)
	rl.now = func() time.Time { return time.Unix(0, 0) }
	mw := rl.Middleware()

	for i := 0; i < 2; i++ {
		if rec := hit(t, mw, "198.51.100.1", ""); rec.Code == http.StatusTooManyRequests {
			t.Fatalf("IP A request %d: want allowed, got 429", i+1)
		}
	}
	if rec := hit(t, mw, "198.51.100.1", ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("IP A over budget: want 429, got %d", rec.Code)
	}
	// IP B has its own bucket and must be unaffected.
	for i := 0; i < 2; i++ {
		if rec := hit(t, mw, "198.51.100.2", ""); rec.Code == http.StatusTooManyRequests {
			t.Fatalf("IP B request %d: want allowed, got 429", i+1)
		}
	}
}

// TestRateLimiterDisabledByEnv pins that PLANE_RATELIMIT_AUTH_PER_MIN=0 turns
// limiting off entirely.
func TestRateLimiterDisabledByEnv(t *testing.T) {
	t.Setenv("PLANE_RATELIMIT_AUTH_PER_MIN", "0")
	rl := NewAuthRateLimiter()
	mw := rl.Middleware()

	for i := 0; i < 500; i++ {
		if rec := hit(t, mw, "192.0.2.50", ""); rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d: limiting disabled, got 429", i+1)
		}
	}
}

// TestAuthRateLimiterEnvOverride pins the env parsing: the default applies when
// unset, a positive value overrides it, and 0 disables.
func TestAuthRateLimiterEnvOverride(t *testing.T) {
	cases := []struct {
		name        string
		env         string
		wantPerMin  float64
		wantDisable bool
	}{
		{"unset uses default", "", 30, false},
		{"override", "120", 120, false},
		{"zero disables", "0", 0, true},
		{"negative falls back to default", "-5", 30, false},
		{"garbage falls back to default", "abc", 30, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PLANE_RATELIMIT_AUTH_PER_MIN", tc.env)
			rl := NewAuthRateLimiter()
			if rl.disabled != tc.wantDisable {
				t.Fatalf("disabled = %v, want %v", rl.disabled, tc.wantDisable)
			}
			if rl.perMinute != tc.wantPerMin {
				t.Fatalf("perMinute = %v, want %v", rl.perMinute, tc.wantPerMin)
			}
		})
	}
}

// TestRateLimiterReapsIdleBuckets pins that the in-memory table cannot grow
// without bound: buckets idle past their refill window are swept.
func TestRateLimiterReapsIdleBuckets(t *testing.T) {
	rl := NewRateLimiter(60, 1) // refill 1/s, burst 1 -> idle TTL 1s
	now := time.Unix(0, 0)
	rl.now = func() time.Time { return now }
	mw := rl.Middleware()

	for _, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		hit(t, mw, ip, "")
	}
	if got := rl.size(); got != 3 {
		t.Fatalf("size before reap = %d, want 3", got)
	}

	now = now.Add(time.Hour)
	hit(t, mw, "10.0.0.4", "")

	if got := rl.size(); got != 1 {
		t.Fatalf("size after reap = %d, want 1 (only the fresh IP)", got)
	}
}

// TestClientIPPrefersForwardedFor pins the bucketing key: the first
// X-Forwarded-For segment wins, RemoteAddr is the fallback, and the ephemeral
// port is always stripped (see TestRateLimiterBucketsIgnoreEphemeralPort).
func TestClientIPPrefersForwardedFor(t *testing.T) {
	cases := []struct {
		name   string
		xff    string
		remote string
		want   string
	}{
		{"single forwarded", "203.0.113.9", "", "203.0.113.9"},
		{"first of a chain", "203.0.113.9, 70.41.3.18, 150.172.238.178", "", "203.0.113.9"},
		{"forwarded with port is stripped", "203.0.113.9:5555", "", "203.0.113.9"},
		{"fallback to remote strips port", "", "198.51.100.5:4321", "198.51.100.5"},
		{"bracketed ipv6 with port", "", "[2001:db8::1]:4321", "2001:0db8:0000:0000:0000:0000:0000:0001"},
		{"bare ipv6 without port", "", "2001:db8::1", "2001:0db8:0000:0000:0000:0000:0000:0001"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := limitEvent(t, tc.xff, tc.remote)
			if got := clientIP(e.Request); got != tc.want {
				t.Fatalf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRateLimiterBucketsIgnoreEphemeralPort is the regression test for a real
// defect: a client opens a new TCP connection per request, so RemoteAddr
// carries a fresh ephemeral port every time. Keying on the raw RemoteAddr made
// every connection its own bucket, so the limiter never fired over real HTTP
// even though the unit tests (which reused one request) passed. All requests
// below come from the same IP on different ports and must share one bucket.
func TestRateLimiterBucketsIgnoreEphemeralPort(t *testing.T) {
	const burst = 3
	rl := NewRateLimiter(30, burst)
	rl.now = func() time.Time { return time.Unix(0, 0) }
	mw := rl.Middleware()

	for i := 0; i < burst; i++ {
		remote := fmt.Sprintf("192.0.2.77:%d", 40000+i)
		if rec := hit(t, mw, "", remote); rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d (port %d): want allowed, got 429", i+1, 40000+i)
		}
	}
	rec := hit(t, mw, "", "192.0.2.77:40099")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("request %d from same IP on a new port: want 429, got %d", burst+1, rec.Code)
	}

	// A different client IP on its own new port must still get through.
	if rec := hit(t, mw, "", "192.0.2.88:40099"); rec.Code == http.StatusTooManyRequests {
		t.Fatalf("different IP: want allowed, got 429")
	}
}
