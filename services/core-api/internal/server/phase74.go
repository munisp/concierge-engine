package server

// Phase 74: partner self-service payment links. Three routes:
//
//	GET  /v1/pay/{token}           — public pay page (tenantctx exempts
//	                                 /v1/pay/: the pay/ HMAC token IS
//	                                 the credential). Quotes the derived
//	                                 outstanding balance; "Pay now" only
//	                                 appears when a live rail can
//	                                 actually take the money.
//	POST /v1/pay/{token}/checkout  — mints the hosted checkout on the
//	                                 tenant's live rail (Stripe /
//	                                 Paystack by currency) and 303s to
//	                                 it. No live rail → 409, never a
//	                                 fake checkout.
//	GET  /v1/folios/{id}/pay-link  — operator: mint the pay link.

import (
	"fmt"
	"html"
	"net/http"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase74(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/pay/{token}", h.payPage)
	mux.HandleFunc("POST /v1/pay/{token}/checkout", h.payCheckout)
	mux.HandleFunc("GET /v1/folios/{id}/pay-link", h.folioPayLink)
}

func (h *handlers) folioPayLink(w http.ResponseWriter, r *http.Request) {
	tenantID, id := tenantOf(r), r.PathValue("id")
	if _, err := h.svc.Store.Folios().Get(r.Context(), tenantID, id); err != nil {
		httpx.WriteError(w, err) // 404 — never leak existence cross-tenant
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"url": h.svc.Folios.PayURL(tenantID, id),
	})
}

func (h *handlers) payCheckout(w http.ResponseWriter, r *http.Request) {
	url, err := h.svc.Folios.StartCheckout(r.Context(), r.PathValue("token"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}

func (h *handlers) payPage(w http.ResponseWriter, r *http.Request) {
	v, err := h.svc.Folios.PayByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString(`<div class="head"><div><h1>Pay statement</h1><p class="muted">` +
		esc(v.Period) + ` · ` + esc(v.TenantName) + `</p></div></div>`)
	b.WriteString(`<table class="meta"><tbody>` +
		`<tr><td>To</td><td>` + esc(v.PartnerName) + `</td></tr>` +
		`<tr><td>Statement</td><td>` + money71(v.TotalMinor) + ` ` + esc(v.Currency) + `</td></tr>`)
	if v.CreditedMinor > 0 {
		b.WriteString(`<tr><td>Less credits</td><td>−` + money71(v.CreditedMinor) + ` ` + esc(v.Currency) + `</td></tr>`)
	}
	if v.PaidMinor > 0 {
		b.WriteString(`<tr><td>Paid so far</td><td>` + money71(v.PaidMinor) + ` ` + esc(v.Currency) + `</td></tr>`)
	}
	if v.DueAt != nil {
		b.WriteString(`<tr><td>Due</td><td>` + v.DueAt.Format("2006-01-02") +
			fmt.Sprintf(` (terms %d days)`, v.TermsDays) + `</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)
	switch v.State {
	case "SETTLED":
		b.WriteString(`<div class="banner paid">PAID — this statement is settled. Thank you.</div>`)
	case "OPEN":
		b.WriteString(`<div class="banner">This statement is still accruing — payment opens when it is issued.</div>`)
	default: // ISSUED
		b.WriteString(`<div class="duebox"><span>Amount due</span><strong>` +
			money71(v.OutstandingMinor) + ` ` + esc(v.Currency) + `</strong></div>`)
		if v.OverdueDays > 0 {
			b.WriteString(`<p class="overdue">` + fmt.Sprintf("%d days past due", v.OverdueDays) + `</p>`)
		}
		if v.RailLive {
			b.WriteString(`<form method="post" action="` + esc(r.URL.Path) + `/checkout">` +
				`<button type="submit">Pay ` + money71(v.OutstandingMinor) + ` ` + esc(v.Currency) +
				` now</button></form>` +
				`<p class="muted">Secure hosted checkout by ` + esc(v.Rail) +
				` — card details never touch ` + esc(v.TenantName) + `'s servers.</p>`)
		} else {
			// Honest degrade — no fake checkout, ever.
			b.WriteString(`<div class="banner">Online card payment is not enabled for ` +
				esc(v.TenantName) + ` yet.</div>` +
				`<p>Pay by bank transfer quoting reference <strong>` + esc(v.FolioID) +
				`</strong> — ` + esc(v.TenantName) + ` will reconcile it against this statement.</p>`)
		}
	}
	b.WriteString(`<p class="muted foot">` + esc(v.TenantName) + ` · statement ` + esc(v.FolioID) + `</p>`)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Pay statement %s · %s</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>body{font-family:system-ui,sans-serif;max-width:34rem;margin:2.5rem auto;padding:0 1rem;color:#1a1a1a;line-height:1.45}
.head{border-bottom:2px solid #1a1a1a;padding-bottom:.6rem}h1{margin:0;font-size:1.5rem}
.muted{color:#666;font-size:.88rem}
table{border-collapse:collapse;width:100%%;font-size:.92rem;margin-top:1rem}td{padding:.32rem .5rem;border-bottom:1px solid #e5e5e5;text-align:left}
.meta td:first-child{color:#666;width:8rem}
.duebox{display:flex;justify-content:space-between;align-items:center;border:1.5px solid #1a1a1a;border-radius:8px;padding:.9rem 1.1rem;margin:1.2rem 0}
.duebox strong{font-size:1.4rem}
.overdue{color:#b3261e;font-weight:600}
button{font-size:1.05rem;font-weight:700;padding:.8rem 1.4rem;border:none;border-radius:8px;background:#1a1a1a;color:#fff;cursor:pointer;width:100%%}
.banner{border:1.5px solid #999;border-radius:8px;padding:.8rem 1rem;margin:1.2rem 0;font-weight:600}
.banner.paid{color:#0a7a2f;border-color:#0a7a2f}
.foot{margin-top:2rem}</style>
</head><body>%s</body></html>`, esc(v.Period), esc(v.TenantName), b.String())
}
