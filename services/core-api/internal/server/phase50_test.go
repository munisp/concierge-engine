package server_test

// Phase 50: security headers + tiered rate limiting middleware tests.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/server"
	"concierge-engine/services/core-api/internal/store"
)

func TestSecurityHeaders50(t *testing.T) {
	env := newEnv(t)

	resp, err := http.Get(env.server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	checks := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "SAMEORIGIN",
		"Referrer-Policy":           "strict-origin-when-cross-origin",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	}
	for k, want := range checks {
		if got := resp.Header.Get(k); got != want {
			t.Fatalf("%s = %q, want %q", k, got, want)
		}
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'self'") {
		t.Fatalf("default responses must deny foreign framing, CSP %q", csp)
	}

	// The embed widget is designed to be iframed onto tenant sites — framing
	// headers must NOT be sent there.
	resp, err = http.Get(env.server.URL + "/embed/taylor")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("embed widget: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "" {
		t.Fatalf("embed widget must not carry X-Frame-Options, got %q", got)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); strings.Contains(csp, "frame-ancestors") {
		t.Fatalf("embed widget CSP must allow external framing, got %q", csp)
	}
}

func TestRateLimitTiers50(t *testing.T) {
	svc := domain.NewServices(store.NewMemory(), domain.Deps{})
	srv := httptest.NewServer(server.New(svc, server.WithRateLimits(2, 2, 100, 100)))
	t.Cleanup(srv.Close)

	get := func(path, tenantID string) (int, http.Header) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if tenantID != "" {
			req.Header.Set("X-Tenant-ID", tenantID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode, resp.Header
	}

	// One public token goes to tenant onboarding (platform admin is
	// tenant-exempt, so it draws from the anonymous IP bucket).
	if code, _ := get("/v1/platform/tenants", ""); code == http.StatusTooManyRequests {
		t.Fatal("first public request must pass")
	}

	// Tenant-scoped traffic uses its own, generous bucket — unaffected by
	// the anonymous budget.
	for i := 0; i < 5; i++ {
		if code, _ := get("/v1/tenant", "some-tenant"); code == http.StatusTooManyRequests {
			t.Fatalf("tenant bucket must not trip at request %d", i)
		}
	}

	// Exhaust the anonymous bucket (1 used above, 1 remaining).
	if code, _ := get("/v1/tenant", ""); code == http.StatusTooManyRequests {
		t.Fatal("second public request must pass (burst 2)")
	}
	code, hdr := get("/v1/tenant", "")
	if code != http.StatusTooManyRequests {
		t.Fatalf("third public request must 429, got %d", code)
	}
	if hdr.Get("Retry-After") == "" {
		t.Fatal("429 must carry Retry-After")
	}

	// Buckets are independent: the tenant lane still flows after the
	// anonymous lane is exhausted.
	if code, _ := get("/v1/tenant", "some-tenant"); code == http.StatusTooManyRequests {
		t.Fatal("tenant bucket must be independent of the anonymous bucket")
	}

	// /healthz is exempt — load-balancer probes must never 429.
	for i := 0; i < 10; i++ {
		if code, _ := get("/healthz", ""); code != http.StatusOK {
			t.Fatalf("healthz exempt: got %d", code)
		}
	}
}

func TestRateLimitEnvelope50(t *testing.T) {
	svc := domain.NewServices(store.NewMemory(), domain.Deps{})
	srv := httptest.NewServer(server.New(svc, server.WithRateLimits(1, 1, 100, 100)))
	t.Cleanup(srv.Close)

	get := func() (int, map[string]string) {
		resp, err := http.Get(srv.URL + "/v1/tenant")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&env)
		return resp.StatusCode, map[string]string{"code": env.Error.Code}
	}
	get() // drain the single token
	code, body := get()
	if code != http.StatusTooManyRequests || body["code"] != "rate_limited" {
		t.Fatalf("expected 429 rate_limited envelope, got %d %v", code, body)
	}
}
