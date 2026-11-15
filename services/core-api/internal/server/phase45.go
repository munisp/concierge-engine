package server

// Phase 45: client self-service consent center. The consent/ HMAC token
// is the credential (tenantctx exempts /v1/consent/ like /v1/unsub/), so
// the page works straight from an email footer on any device. Three
// public routes (view, set, receipt) plus one tenant-authed route for the
// console to mint the link for a client.

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase45(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/consent/{token}", h.consentCenter)
	mux.HandleFunc("POST /v1/consent/{token}/set", h.consentCenterSet)
	mux.HandleFunc("GET /v1/consent/{token}/receipt", h.consentReceipt)
	mux.HandleFunc("GET /v1/clients/{id}/consent-link", h.consentLink)
}

// consentLink mints the client's consent-center URL for the console
// (tenant-authed; the operator pastes it into a message or the client
// reaches it from any marketing email footer).
func (h *handlers) consentLink(w http.ResponseWriter, r *http.Request) {
	tenantID, id := tenantOf(r), r.PathValue("id")
	if _, err := h.svc.Store.Clients().Get(r.Context(), tenantID, id); err != nil {
		httpx.WriteError(w, err) // 404 — never leak existence cross-tenant
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"url": h.svc.PublicBase() + "/v1/consent/" + h.svc.Trips.ConsentTokenFor(tenantID, id),
	})
}

func (h *handlers) consentCenter(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	cc, err := h.svc.Clients.ConsentCenterByToken(r.Context(), token)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	name := html.EscapeString(cc.TenantName)
	var b strings.Builder
	if cc.Erased {
		b.WriteString(`<p>Your data has been erased. <strong>` + name + `</strong> holds only a pseudonymous tombstone and the consent evidence trail below — no further processing or contact is possible.</p>`)
	} else {
		b.WriteString(`<p><strong>` + name + `</strong> processes your data under the choices below. Changes take effect immediately and are recorded with a timestamp and this page as the source.</p>`)
	}
	for _, s := range cc.Scopes {
		state, action, next := "not granted", "Switch on", "true"
		if s.Granted {
			state, action, next = "granted", "Switch off", "false"
		}
		b.WriteString(`<div class="scope"><h2>` + html.EscapeString(s.Label) +
			` <span class="` + map[bool]string{true: "on", false: "off"}[s.Granted] + `">` + state + `</span></h2>`)
		if s.Blurb != "" {
			b.WriteString(`<p class="muted">` + html.EscapeString(s.Blurb) + `</p>`)
		}
		switch {
		case cc.Erased:
			// read-only tombstone view
		case s.Locked:
			b.WriteString(`<p class="muted">Locked — see above. To end the service relationship entirely, ask ` + name + ` to erase your data.</p>`)
		default:
			b.WriteString(`<form method="post" action="/v1/consent/` + html.EscapeString(token) + `/set">` +
				`<input type="hidden" name="scope" value="` + html.EscapeString(s.Scope) + `">` +
				`<input type="hidden" name="granted" value="` + next + `">` +
				`<button type="submit">` + action + `</button></form>`)
		}
		b.WriteString(`</div>`)
	}
	if len(cc.History) > 0 {
		b.WriteString(`<h2>Your consent record</h2><table><thead><tr><th>When</th><th>Scope</th><th>Change</th><th>Via</th></tr></thead><tbody>`)
		for _, e := range cc.History {
			change := "revoked"
			if e.Granted {
				change = "granted"
			}
			b.WriteString(`<tr><td>` + e.CreatedAt.Format("2006-01-02 15:04 UTC") + `</td><td>` +
				html.EscapeString(e.Scope) + `</td><td>` + change + `</td><td class="muted">` +
				html.EscapeString(e.Source) + `</td></tr>`)
		}
		b.WriteString(`</tbody></table>`)
	}
	b.WriteString(`<p><a href="/v1/consent/` + html.EscapeString(token) + `/receipt">Download consent receipt (JSON)</a></p>`)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Your privacy choices · %s</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>body{font-family:system-ui,sans-serif;max-width:34rem;margin:3rem auto;padding:0 1rem;color:#1a1a1a;line-height:1.5}
.scope{border:1px solid #ddd;border-radius:8px;padding:.8rem 1rem;margin:.8rem 0}
.scope h2{margin:.1rem 0 .3rem;font-size:1.05rem}.muted{color:#666;font-size:.92rem}
.on{color:#0a7a2f;font-weight:600}.off{color:#999;font-weight:600}
table{border-collapse:collapse;width:100%%;font-size:.88rem}td,th{padding:.3rem .5rem;border-bottom:1px solid #eee;text-align:left}
button{padding:.45rem .9rem;border-radius:6px;border:1px solid #999;background:#fff;cursor:pointer}</style>
</head><body><h1>Your privacy choices</h1>%s</body></html>`, name, b.String())
}

// consentCenterSet applies one toggle and redirects back to the page —
// plain HTML forms, no JS required, works on any device the link opens on.
func (h *handlers) consentCenterSet(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if err := r.ParseForm(); err != nil {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	granted := r.PostFormValue("granted") == "true"
	if _, err := h.svc.Clients.ConsentSetByToken(r.Context(), token, r.PostFormValue("scope"), granted); err != nil {
		httpx.WriteError(w, err)
		return
	}
	http.Redirect(w, r, "/v1/consent/"+token, http.StatusSeeOther)
}

// consentReceipt is the downloadable proof artifact — one JSON document
// with the controller, the current scopes and the full evidence trail.
func (h *handlers) consentReceipt(w http.ResponseWriter, r *http.Request) {
	rec, err := h.svc.Clients.ConsentReceiptByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="consent-receipt.json"`)
	_ = json.NewEncoder(w).Encode(rec)
}
