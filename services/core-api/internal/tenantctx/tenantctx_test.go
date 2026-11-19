package tenantctx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(FromContext(r.Context())))
}

func TestMiddlewareTenantHeader(t *testing.T) {
	h := Middleware(http.HandlerFunc(okHandler))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/clients/x", nil)
	req.Header.Set("X-Tenant-ID", "t-1")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "t-1" {
		t.Fatalf("header auth failed: %d %q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/clients/x", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing tenant must be 403, got %d", rec.Code)
	}
}

func TestMiddlewareApiKeyAuthAndScopes(t *testing.T) {
	SetKeyAuthenticator(func(_ context.Context, header string) (string, []string, string, error) {
		switch header {
		case "Bearer cek_readonly":
			return "t-key", []string{"read"}, "", nil
		case "Bearer cek_writer":
			return "t-2", []string{"write"}, "", nil
		case "Bearer cek_partner":
			return "t-2", []string{"partner"}, "partner-1", nil
		}
		return "", nil, "", errors.New("nope")
	})
	defer SetKeyAuthenticator(nil)

	h := Middleware(http.HandlerFunc(okHandler))

	// read-scoped key: GET allowed, POST rejected.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/catalog", nil)
	req.Header.Set("Authorization", "Bearer cek_readonly")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "t-key" {
		t.Fatalf("read GET must pass and resolve tenant, got %d %q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/catalog", nil)
	req.Header.Set("Authorization", "Bearer cek_readonly")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("read key mutating must be 403, got %d", rec.Code)
	}

	// write key implies read and permits mutations; tenant resolves from key.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/catalog", nil)
	req.Header.Set("Authorization", "Bearer cek_writer")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "t-2" {
		t.Fatalf("write key must pass and resolve tenant, got %d %q", rec.Code, rec.Body.String())
	}

	// webhook admin needs webhooks:manage.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/webhooks", nil)
	req.Header.Set("Authorization", "Bearer cek_writer")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("webhook admin without webhooks:manage must be 403, got %d", rec.Code)
	}

	// bogus key rejected.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/catalog", nil)
	req.Header.Set("Authorization", "Bearer cek_bogus")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bogus key must be 403, got %d", rec.Code)
	}

	// platform routes stay open (no tenant required).
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/platform/tenants", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("platform route must not require tenant, got %d", rec.Code)
	}
}

// Phase 56: partner-scoped keys reach only /v1/partner/* — the tenant API
// rejects them, and the partner surface rejects non-partner credentials.
func TestMiddlewarePartnerScopedKey(t *testing.T) {
	SetKeyAuthenticator(func(_ context.Context, header string) (string, []string, string, error) {
		if header == "Bearer cek_partner" {
			return "t-2", []string{"partner"}, "partner-1", nil
		}
		if header == "Bearer cek_writer" {
			return "t-2", []string{"write"}, "", nil
		}
		return "", nil, "", errors.New("nope")
	})
	defer SetKeyAuthenticator(nil)

	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(PartnerFromContext(r.Context())))
	}))

	// Partner key on the tenant API: forbidden even for reads.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/catalog", nil)
	req.Header.Set("Authorization", "Bearer cek_partner")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("partner key on tenant API must be 403, got %d", rec.Code)
	}

	// Partner key on /v1/partner/*: passes, partner id in context.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/partner/assignments", nil)
	req.Header.Set("Authorization", "Bearer cek_partner")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "partner-1" {
		t.Fatalf("partner key on partner surface: %d %q", rec.Code, rec.Body.String())
	}

	// Tenant-wide key on the partner surface authenticates but carries no
	// partner binding (the handlers reject it with 403).
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/partner/assignments", nil)
	req.Header.Set("Authorization", "Bearer cek_writer")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "" {
		t.Fatalf("tenant key must not carry partner binding: %d %q", rec.Code, rec.Body.String())
	}
}
