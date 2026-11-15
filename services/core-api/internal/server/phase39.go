package server

import (
	"fmt"
	"html"
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

// Phase 39: email channel — the public one-click unsubscribe endpoint.
// The link rides in every marketing email footer (mandated server-side)
// and as the List-Unsubscribe header target; the HMAC token is the
// credential, so no tenant header is required (tenantctx exempts
// /v1/unsub/ like /v1/track/).
func (h *handlers) registerPhase39(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/unsub/{token}", h.emailUnsubscribe)
}

func (h *handlers) emailUnsubscribe(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Outreach.UnsubscribeByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	name := html.EscapeString(res.TenantName)
	var body string
	if res.Already {
		body = fmt.Sprintf("<p>Marketing emails from <strong>%s</strong> were already switched off for this address — nothing changed.</p>", name)
	} else {
		body = fmt.Sprintf("<p>You are unsubscribed. <strong>%s</strong> will no longer send marketing emails to this address.</p><p>Service messages about active bookings are unaffected.</p>", name)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Unsubscribe · %s</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>body{font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem;color:#1a1a1a;line-height:1.5}</style>
</head><body><h1>Unsubscribe</h1>%s</body></html>`, name, body)
}
