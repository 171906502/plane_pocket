package middleware

import (
	"math"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"github.com/plane-pocketbase/plane-pocketbase/internal/serializers"
)

// The credential-verifying endpoints under /auth/* are the only online
// brute-force surface: without a limit an attacker can try unlimited passwords
// (or magic codes) against a known account. Everything else under /auth/
// (sign-up, sign-out, get-csrf-token, auth-check, email-check) is either not a
// secret guess or part of the normal flow, so it is deliberately left
// unlimited.
//
// Limiting is a per-client-IP token bucket: a bucket holds up to `burst`
// tokens and refills at `perMinute / 60` tokens per second. Each request costs
// one token. The default budget is 30 requests/minute with a burst of 10.
// PLANE_RATELIMIT_AUTH_PER_MIN overrides the per-minute rate; 0 disables
// limiting entirely (for tests and troubleshooting).
const (
	defaultAuthRatePerMinute = 30
	defaultAuthRateBurst     = 10

	// retryAfterMessage is the 429 body. DRF's throttling classes answer with
	// {"detail": "Request was throttled. Expected available in N seconds."};
	// this backend keeps the same {"detail": …} envelope via serializers.
	retryAfterMessage = "Too many requests. Please try again later."
)

// tokenBucket is one client's bucket. It is only ever touched while holding
// RateLimiter.mu.
type tokenBucket struct {
	tokens float64
	last   time.Time
}

// RateLimiter is an in-memory, per-key token bucket limiter.
//
// The bucket table is reaped: a bucket that has been idle long enough to have
// fully refilled is indistinguishable from an absent one, so it is deleted on
// the next sweep. Without this the table would grow without bound on a
// long-running server (one entry per IP ever seen).
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket

	perMinute float64
	burst     float64
	disabled  bool

	// now is injectable so tests can freeze or advance the clock.
	now func() time.Time

	lastReap time.Time
}

// NewRateLimiter builds a limiter allowing perMinute requests per minute with
// the given burst. perMinute <= 0 disables limiting.
func NewRateLimiter(perMinute, burst int) *RateLimiter {
	if burst < 1 {
		burst = 1
	}
	return &RateLimiter{
		buckets:   make(map[string]*tokenBucket),
		perMinute: float64(perMinute),
		burst:     float64(burst),
		disabled:  perMinute <= 0,
		now:       time.Now,
	}
}

// NewAuthRateLimiter builds the limiter for the credential endpoints, honouring
// PLANE_RATELIMIT_AUTH_PER_MIN. A missing, unparseable or negative value keeps
// the default; 0 disables limiting.
func NewAuthRateLimiter() *RateLimiter {
	perMinute := defaultAuthRatePerMinute
	if raw := strings.TrimSpace(os.Getenv("PLANE_RATELIMIT_AUTH_PER_MIN")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
			perMinute = v
		}
	}
	return NewRateLimiter(perMinute, defaultAuthRateBurst)
}

// Middleware returns the HTTP middleware that enforces the limit. On the
// over-limit branch it answers 429 with a Retry-After header and the standard
// {"detail": …} body; otherwise it forwards to the next handler untouched.
func (r *RateLimiter) Middleware() func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		allowed, retryAfter := r.allow(clientIP(e.Request))
		if allowed {
			return e.Next()
		}
		e.Response.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		return serializers.WriteError(e, http.StatusTooManyRequests, serializers.Detail(retryAfterMessage))
	}
}

// allow decides whether a request from key may proceed, and how many seconds
// the caller should wait when it may not.
func (r *RateLimiter) allow(key string) (bool, int) {
	if r.disabled {
		return true, 0
	}

	now := r.now()

	r.mu.Lock()
	defer r.mu.Unlock()

	r.reapLocked(now)

	b := r.buckets[key]
	if b == nil {
		b = &tokenBucket{tokens: r.burst, last: now}
		r.buckets[key] = b
	}

	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(r.burst, b.tokens+elapsed*r.ratePerSecond())
		b.last = now
	}

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}

	// Seconds until one whole token is available again.
	retryAfter := int(math.Ceil((1 - b.tokens) / r.ratePerSecond()))
	if retryAfter < 1 {
		retryAfter = 1
	}
	return false, retryAfter
}

// ratePerSecond is the bucket refill rate.
func (r *RateLimiter) ratePerSecond() float64 {
	return r.perMinute / 60.0
}

// idleTTL is how long a bucket must sit untouched before it is safe to drop:
// after this long it has refilled to `burst` and is equivalent to a new bucket.
func (r *RateLimiter) idleTTL() time.Duration {
	return time.Duration(r.burst / r.ratePerSecond() * float64(time.Second))
}

// reapLocked deletes buckets that have been idle for longer than idleTTL. It
// sweeps at most once per idleTTL so the hot path stays cheap, and must be
// called with r.mu held.
func (r *RateLimiter) reapLocked(now time.Time) {
	ttl := r.idleTTL()
	if !r.lastReap.IsZero() && now.Sub(r.lastReap) < ttl {
		return
	}
	r.lastReap = now
	for key, b := range r.buckets {
		if now.Sub(b.last) > ttl {
			delete(r.buckets, key)
		}
	}
}

// size reports the current number of tracked buckets. It exists for tests that
// pin the reaping behaviour.
func (r *RateLimiter) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.buckets)
}

// clientIP returns the key a request is bucketed by.
//
// SECURITY: this trusts the reverse-proxy header X-Forwarded-For, taking its
// first segment. That is only safe when the process sits behind a proxy that
// *overwrites* the header; on a directly-exposed deployment the header is
// client-controlled, so an attacker can spoof it to dodge the limiter or to
// pin the limit on another address. RemoteAddr is the fallback when the header
// is absent.
//
// The port is stripped before keying. RemoteAddr is "IP:port" and the port is
// an ephemeral, per-connection value, so bucketing on the raw string would
// give every new TCP connection its own bucket and the limit would never
// accumulate — the limiter would be silently inert against any client that
// opens a fresh connection per request (which is exactly what an attacker
// does). This mirrors the router's own Event.RemoteIP().
func clientIP(req *http.Request) string {
	if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		if ip := strings.TrimSpace(first); ip != "" {
			return normalizeIP(ip)
		}
	}
	return normalizeIP(req.RemoteAddr)
}

// normalizeIP reduces "host:port" (or a bracketed IPv6 literal with a port) to
// a bare, canonical address so the same client always maps to the same bucket.
// A value that carries no port and no parseable address is returned unchanged.
func normalizeIP(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return hostport
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.StringExpanded()
	}
	return host
}
