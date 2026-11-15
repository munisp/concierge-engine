package server

import (
	"fmt"
	"html"
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

// Phase 43: post-sunset re-engagement — the public one-tap re-subscribe
// endpoint. The link rides in the footer of service-lane emails to clients
// whose marketing consent is off; the HMAC token is the credential, so no
// tenant header is required (tenantctx exempts /v1/reopt/ like /v1/unsub/).
func (h *handlers) registerPhase43(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/reopt/{token}", h.marketingReopt)
}

func (h *handlers) marketingReopt(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Outreach.ReoptInByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	name := html.EscapeString(res.TenantName)
	var body string
	if res.Already {
		body = fmt.Sprintf("<p>Marketing messages from <strong>%s</strong> were already switched on for you — nothing changed.</p>", name)
	} else {
		body = fmt.Sprintf("<p>You are back on the list. <strong>%s</strong> can now send you offers and updates.</p><p>Every marketing message carries a one-click unsubscribe link — change your mind any time.</p>", name)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Subscribe · %s</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>body{font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem;color:#1a1a1a;line-height:1.5}</style>
</head><body><h1>Subscribe</h1>%s</body></html>`, name, body)
}
