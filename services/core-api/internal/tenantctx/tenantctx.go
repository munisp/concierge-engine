// Package tenantctx resolves the tenant for every request (Master Spec §13:
// every service tenant-scoped by enforced data filters; RLS verified in tests).
// In production APISIX validates the OIDC JWT and injects X-Tenant-ID; in dev
// the header is trusted directly. Phase 3 adds the public-API path: a
// "Bearer cek_..." API key resolves tenant + scopes server-side.
package tenantctx

import (
	"context"
	"net/http"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
)

type ctxKey struct{}
type ctxScopesKey struct{}
type ctxPartnerKey struct{}

func FromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey{}).(string)
	return v
}

func WithTenant(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, tenantID)
}

// ScopesFromContext returns the API-key scopes, nil when the request was
// tenant-header authed (internal/OIDC path — full access).
func ScopesFromContext(ctx context.Context) []string {
	v, _ := ctx.Value(ctxScopesKey{}).([]string)
	return v
}

// PartnerFromContext returns the partner a partner-scoped API key is bound
// to (Phase 56), "" for tenant-header and tenant-wide key requests.
func PartnerFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxPartnerKey{}).(string)
	return v
}

// KeyAuthenticator resolves a "Bearer cek_..." header to
// (tenantID, scopes, partnerID). partnerID is non-empty only for
// partner-scoped keys (Phase 56). Set once at startup by the server
// package (avoids an import cycle).
type KeyAuthenticator func(ctx context.Context, authHeader string) (tenantID string, scopes []string, partnerID string, err error)

var keyAuth KeyAuthenticator

func SetKeyAuthenticator(a KeyAuthenticator) { keyAuth = a }

// tenantScoped reports whether the path requires a resolved tenant.
// /v1/trips/, /v1/track/, /v1/portal/, /v1/unsub/, /v1/reopt/ and
// /v1/consent/ are token-authenticated (the HMAC link IS the credential —
// chauffeur, passenger, partner and email recipient never carry a tenant
// header).
func tenantScoped(path string) bool {
	return strings.HasPrefix(path, "/v1/") &&
		!strings.HasPrefix(path, "/v1/platform/") &&
		!strings.HasPrefix(path, "/v1/trips/") &&
		!strings.HasPrefix(path, "/v1/track/") &&
		!strings.HasPrefix(path, "/v1/portal/") &&
		!strings.HasPrefix(path, "/v1/unsub/") &&
		!strings.HasPrefix(path, "/v1/reopt/") &&
		!strings.HasPrefix(path, "/v1/consent/") &&
		path != "/v1/payments/stripe/webhook"
}

// scopeOK enforces API-key scopes: reads need "read" ("write" implies it),
// mutations need "write", webhook administration needs "webhooks:manage".
func scopeOK(r *http.Request, scopes []string) bool {
	has := scopeListHas
	if strings.HasPrefix(r.URL.Path, "/v1/webhooks") && r.Method != http.MethodGet {
		return has(scopes, "webhooks:manage")
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return has(scopes, "read")
	}
	return has(scopes, "write")
}

// scopeListHas reports whether scopes cover need ("write" implies "read").
func scopeListHas(scopes []string, need string) bool {
	for _, s := range scopes {
		if s == need || (need == "read" && s == "write") {
			return true
		}
	}
	return false
}

// Middleware enforces presence of a tenant on all /v1/ routes except
// platform-admin, health endpoints, chauffeur trip links (token-authed: the
// HMAC-signed token carries the tenant), and the Stripe webhook (the tenant
// rides in the HMAC-signed event metadata, never the caller's headers).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tenantScoped(r.URL.Path) {
			id := r.Header.Get("X-Tenant-ID")
			if id == "" && keyAuth != nil {
				// Public-API path: resolve "Bearer cek_..." to tenant + scopes.
				if tid, scopes, partnerID, err := keyAuth(r.Context(), r.Header.Get("Authorization")); err == nil {
					if partnerID != "" {
						// Phase 56: partner-scoped keys reach ONLY the
						// /v1/partner/* surface — never the tenant API.
						if !strings.HasPrefix(r.URL.Path, "/v1/partner/") || !scopeListHas(scopes, "partner") {
							httpx.WriteError(w, httpx.ErrForbidden)
							return
						}
					} else if !scopeOK(r, scopes) {
						httpx.WriteError(w, httpx.ErrForbidden)
						return
					}
					ctx := context.WithValue(WithTenant(r.Context(), tid), ctxScopesKey{}, scopes)
					ctx = context.WithValue(ctx, ctxPartnerKey{}, partnerID)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}
			if id == "" {
				httpx.WriteError(w, httpx.ErrForbidden)
				return
			}
			r = r.WithContext(WithTenant(r.Context(), id))
		}
		next.ServeHTTP(w, r)
	})
}
