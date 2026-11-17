package server

// Phase 50: security hardening wave — response security headers and tiered
// in-memory rate limiting at the edge of the monolith.
//
// Threat model (2025–26 attack wave): L7 floods against public surfaces
// (site, lead capture, estimates), LLM-cost abuse against agent turns, and
// credential stuffing against console/API auth. Two token buckets:
//   - anonymous requests (no tenant resolved): per-source-IP, strictest
//   - tenant-scoped requests: per-tenant, generous
// /healthz is exempt (load-balancer probes). Limits are env-tunable
// (CE_RATE_*) and can be disabled outright for load tests.
//
// Honest limit: buckets are in-process — multi-replica deployments need a
// CDN/WAF (Cloudflare) or Redis-backed limiter for fleet-wide ceilings.
// This middleware still protects each replica from being individually
// overwhelmed, and it is the canonical hook for a shared backend later.

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"concierge-engine/services/core-api/internal/httpx"
)

// Options tunes server.New. Zero values fall back to the defaults below.
type Options struct {
	RatePublicRPS   float64
	RatePublicBurst int
	RateTenantRPS   float64
	RateTenantBurst int
	RateLimitOff    bool
}

type Option func(*Options)

// WithRateLimits overrides both tiers (requests/sec + burst).
func WithRateLimits(publicRPS float64, publicBurst int, tenantRPS float64, tenantBurst int) Option {
	return func(o *Options) {
		o.RatePublicRPS, o.RatePublicBurst = publicRPS, publicBurst
		o.RateTenantRPS, o.RateTenantBurst = tenantRPS, tenantBurst
	}
}

// WithRateLimitDisabled turns the limiter off (load tests, local dev).
func WithRateLimitDisabled() Option {
	return func(o *Options) { o.RateLimitOff = true }
}

const (
	defaultPublicRPS   = 30
	defaultPublicBurst = 60
	defaultTenantRPS   = 200
	defaultTenantBurst = 400
)

func (o *Options) withDefaults() Options {
	out := *o
	if out.RatePublicRPS <= 0 {
		out.RatePublicRPS = defaultPublicRPS
	}
	if out.RatePublicBurst <= 0 {
		out.RatePublicBurst = defaultPublicBurst
	}
	if out.RateTenantRPS <= 0 {
		out.RateTenantRPS = defaultTenantRPS
	}
	if out.RateTenantBurst <= 0 {
		out.RateTenantBurst = defaultTenantBurst
	}
	return out
}

// ------------------------------------------------------------- security headers

// securityHeaders sets the baseline browser-security headers on every
// response. Handlers may still override a header (the media endpoint sets a
// stricter CSP). The /embed/ booking widget is DESIGNED to be iframed onto
// tenant websites, so framing headers are skipped there — everything else
// is frame-denied to kill clickjacking.
func securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; img-src 'self' data:; " +
		"style-src 'self' 'unsafe-inline'; script-src 'self'; " +
		"connect-src 'self'; frame-ancestors 'self'; base-uri 'self'"
	const cspEmbeddable = "default-src 'self'; img-src 'self' data:; " +
		"style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.Set("Permissions-Policy", "camera=(), microphone=(self), geolocation=(self)")
		if strings.HasPrefix(r.URL.Path, "/embed/") {
			h.Set("Content-Security-Policy", cspEmbeddable)
		} else {
			h.Set("X-Frame-Options", "SAMEORIGIN")
			h.Set("Content-Security-Policy", csp)
		}
		next.ServeHTTP(w, r)
	})
}

// ------------------------------------------------------------- rate limiting

type rateBucket struct {
	lim  *rate.Limiter
	last time.Time
}

type rateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*rateBucket
	lastSweep time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{buckets: map[string]*rateBucket{}, lastSweep: time.Now()}
}

// allow takes a token for key, creating the bucket on first use. Sweeps
// stale buckets lazily (no background goroutine — server tests spin up
// hundreds of instances).
func (rl *rateLimiter) allow(key string, rps float64, burst int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &rateBucket{lim: rate.NewLimiter(rate.Limit(rps), burst)}
		rl.buckets[key] = b
	}
	b.last = now
	if len(rl.buckets) > 1024 && now.Sub(rl.lastSweep) > time.Minute {
		for k, v := range rl.buckets {
			if now.Sub(v.last) > 10*time.Minute {
				delete(rl.buckets, k)
			}
		}
		rl.lastSweep = now
	}
	return b.lim.Allow()
}

// clientIP prefers the first X-Forwarded-For hop (set by the ingress),
// falling back to the peer address. Spoofable when no trusted proxy strips
// it — a known, documented limit of an in-process limiter; the CDN/WAF
// layer owns the authoritative client identity in production.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first, _, _ := strings.Cut(xff, ","); strings.TrimSpace(first) != "" {
			return strings.TrimSpace(first)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimit throttles anonymous traffic per source IP and identified
// traffic per tenant. It runs OUTSIDE tenantctx (auth-rejected junk must
// burn budget too), so it derives the bucket from the raw headers:
// X-Tenant-ID, else a hash of the Authorization bearer, else the source IP.
// Identified traffic also draws from a per-IP ceiling at tenant rates, so
// rotating fake tenant headers cannot mint unlimited buckets.
// 429s carry Retry-After so well-behaved clients back off.
func rateLimit(next http.Handler, opts Options) http.Handler {
	if opts.RateLimitOff {
		return next
	}
	rl := newRateLimiter()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		tenantID := r.Header.Get("X-Tenant-ID")
		if tenantID == "" {
			if auth := r.Header.Get("Authorization"); auth != "" {
				sum := sha256.Sum256([]byte(auth))
				tenantID = "key:" + hex.EncodeToString(sum[:8])
			}
		}
		var limited bool
		if tenantID != "" {
			limited = !rl.allow("t:"+tenantID, opts.RateTenantRPS, opts.RateTenantBurst) ||
				!rl.allow("ceiling:"+clientIP(r), opts.RateTenantRPS, opts.RateTenantBurst)
		} else {
			limited = !rl.allow("ip:"+clientIP(r), opts.RatePublicRPS, opts.RatePublicBurst)
		}
		if limited {
			w.Header().Set("Retry-After", "1")
			var env httpx.ErrorEnvelope
			env.Error.Code = "rate_limited"
			env.Error.Message = "too many requests — back off and retry"
			httpx.WriteJSON(w, http.StatusTooManyRequests, env)
			return
		}
		next.ServeHTTP(w, r)
	})
}
