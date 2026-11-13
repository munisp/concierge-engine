// Package tenantctx resolves the tenant for every request (Master Spec §13:
// every service tenant-scoped by enforced data filters; RLS verified in tests).
// In production APISIX validates the OIDC JWT and injects X-Tenant-ID; in dev
// the header is trusted directly.
package tenantctx

import (
	"context"
	"net/http"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
)

type ctxKey struct{}

func FromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey{}).(string)
	return v
}

func WithTenant(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, tenantID)
}

// Middleware enforces presence of a tenant on all /v1/ routes except
// platform-admin, health endpoints, chauffeur trip links (token-authed: the
// HMAC-signed token carries the tenant), and the Stripe webhook (the tenant
// rides in the HMAC-signed event metadata, never the caller's headers).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") &&
			!strings.HasPrefix(r.URL.Path, "/v1/platform/") &&
			!strings.HasPrefix(r.URL.Path, "/v1/trips/") &&
			r.URL.Path != "/v1/payments/stripe/webhook" {
			id := r.Header.Get("X-Tenant-ID")
			if id == "" {
				httpx.WriteError(w, httpx.ErrForbidden)
				return
			}
			r = r.WithContext(WithTenant(r.Context(), id))
		}
		next.ServeHTTP(w, r)
	})
}
