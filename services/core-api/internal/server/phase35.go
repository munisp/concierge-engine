package server

// Phase 35: customer acquisition & outreach — 30 innovations.
//
// Acquisition (API + public site):
//	A1  /site/{slug}/airport/{iata}      airport SEO landing pages
//	A2  /site/{slug}/routes/{from}-to-{to}  city-pair estimate pages
//	A3  POST /site/{slug}/estimate       anonymous instant estimate
//	A4  /v1/quotes/{id}/share + /quote/{token}  shareable quote links
//	A5  /v1/growth/referral-leaderboard  referral leaderboard
//	A6  /v1/marketing/links              channel link builder
//	A7  Referer auto-attribution (in server.go trackAttribution)
//	A8  first-booking credit (domain, booking confirm hook)
//	A9  /v1/capture/corporate            corporate self-serve signup
//	A10 social proof band (site landing, real counts)
//	A11 review deep links (track view carries review URL)
//	A12 campaign windows (CTA attribution follows the active window)
//	A13 embed widget theming (?theme=light&compact=1)
//	A14 /site/{slug}/gift                gift voucher storefront
//	A15 sitemap coverage for all of the above
//
// Outreach (API):
//	O1  /v1/outreach/broadcast           segment broadcasts
//	O2  /v1/outreach/segments/preview    segment engine preview
//	O3  drip sequences (domain sweep)
//	O4  template A/B (broadcast #a/#b arms)
//	O5  STOP/START keywords (domain intake)
//	O6  scheduled sends (send.scheduled_for + sweep)
//	O7  quiet hours (domain deferral)
//	O8-O11, O13, O14 lifecycle rules (domain followups)
//	O12 journey notifications (domain dispatch hook)
//	O15 /v1/outreach/campaigns/{id}/funnel

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (h *handlers) registerPhase35(mux *http.ServeMux) {
	// acquisition API
	mux.HandleFunc("POST /v1/quotes/{id}/share", h.shareQuote)
	mux.HandleFunc("GET /v1/growth/referral-leaderboard", h.referralLeaderboard)
	mux.HandleFunc("GET /v1/marketing/links", h.marketingLinks)
	mux.HandleFunc("POST /v1/capture/corporate", h.captureCorporate)
	// outreach API
	mux.HandleFunc("POST /v1/outreach/broadcast", h.broadcastCampaign)
	mux.HandleFunc("POST /v1/outreach/segments/preview", h.segmentPreview)
	mux.HandleFunc("GET /v1/outreach/campaigns/{id}/funnel", h.campaignFunnel)
	// public surfaces (non-/v1: no tenant header, capability/URL-scoped)
	mux.HandleFunc("GET /quote/{token}", h.quoteSharePage)
	mux.HandleFunc("POST /site/{slug}/estimate", h.siteEstimate)
}

// ------------------------------------------------------------- A4: quote share

func (h *handlers) shareQuote(w http.ResponseWriter, r *http.Request) {
	share, err := h.svc.Trips.Share(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, share)
}

// quoteSharePage renders the forwardable quote. Standalone minimal chrome
// (no site chrome — the page must render identically whoever opens it).
func (h *handlers) quoteSharePage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	// Resolve the tenant from the token first (the CTA needs the tenant id).
	tenantID, _, err := h.svc.Trips.ParseQuoteToken(token)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	t, terr := h.svc.Store.Tenants().Get(r.Context(), tenantID)
	if terr != nil {
		httpx.WriteError(w, terr)
		return
	}
	cta := h.captureChannelURLFor(r, t, "seo", "quote_share", "quote_share")
	v, err := h.svc.Trips.QuoteView(r.Context(), token, cta)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store") // priced offer — never cached
	var lines strings.Builder
	for _, l := range v.Quote.Lines {
		fmt.Fprintf(&lines, `<tr><td>%s</td><td style="text-align:right">%s %.2f</td></tr>`,
			template.HTMLEscapeString(l.Description), template.HTMLEscapeString(v.Quote.Currency),
			float64(l.AmountMinor)/100)
	}
	expiry := ""
	if v.Expired {
		expiry = `<p class="expired">This quote has expired — the conversation can re-quote in seconds.</p>`
	} else {
		expiry = `<p class="valid">Valid until ` + v.Quote.ExpiresAt.Format("2 Jan 2006 15:04 UTC") + `</p>`
	}
	wa := ""
	if v.WhatsApp != "" {
		wa = fmt.Sprintf(`<a class="cta wa" href="%s?text=%s">Confirm on WhatsApp</a>`,
			template.HTMLEscapeString(v.WhatsApp),
			template.HTMLEscapeString("I'd like to confirm my quote"))
	}
	itinerary := ""
	if v.Request != nil {
		for _, it := range v.Request.Items {
			route := template.HTMLEscapeString(it.PickupAddress)
			if it.DropoffAddress != "" {
				route += " → " + template.HTMLEscapeString(it.DropoffAddress)
			}
			itinerary += `<p class="leg">` + route + `</p>`
		}
	}
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Your quote — %s</title>
<style>body{font-family:system-ui,sans-serif;max-width:640px;margin:24px auto;padding:0 16px;color:#1a1a1a}
table{width:100%%;border-collapse:collapse;margin:16px 0}td{padding:8px 0;border-bottom:1px solid #eee}
.total td{font-weight:700;font-size:1.15em;border-bottom:none}
.cta{display:block;background:#0b5fff;color:#fff;text-decoration:none;padding:14px;border-radius:10px;text-align:center;font-weight:600;margin:10px 0}
.wa{background:#25d366}.valid{color:#0a7a3d}.expired{color:#b3261e}.leg{color:#555;margin:4px 0}
.brand{font-size:.9em;color:#666;letter-spacing:.06em;text-transform:uppercase}</style></head><body>
<p class="brand">%s</p><h1>Your quote</h1>%s%s
<table>%s<tr class="total"><td>Total</td><td style="text-align:right">%s %.2f</td></tr></table>
<p>Deposit to confirm: %s %.2f</p>%s
<a class="cta" href="%s">Book this quote</a>%s
</body></html>`,
		template.HTMLEscapeString(v.TenantName),
		template.HTMLEscapeString(v.TenantName), expiry, itinerary,
		lines.String(), template.HTMLEscapeString(v.Quote.Currency), float64(v.Quote.TotalMinor)/100,
		template.HTMLEscapeString(v.Quote.Currency), float64(v.Quote.DepositMinor)/100,
		"", template.HTMLEscapeString(v.CaptureURL), wa)
}

// ------------------------------------------------------------- A5: leaderboard

func (h *handlers) referralLeaderboard(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	ranks, err := h.svc.ReferralLeaderboard(r.Context(), tenantOf(r), limit)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"leaderboard": ranks})
}

// ------------------------------------------------------------- A6: link builder

// marketingLinks returns ready-to-paste capture links per channel, stamped
// with the active campaign window (A12) when one is running.
func (h *handlers) marketingLinks(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.Store.Tenants().Get(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	channels := []string{"seo", "qr", "widget", "social", "email", "ads", "partner"}
	links := map[string]string{}
	for _, ch := range channels {
		links[ch] = h.captureChannelURLFor(r, t, ch, ch, ch)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"capture_links":   links,
		"active_campaign": t.AcquisitionConfig.ActiveCampaign(h.svc.Now()),
		"capture_page":    h.svc.ConsoleBase() + "/capture?tenant=" + t.ID,
	})
}

// ------------------------------------------------------------- A9: corporate self-serve

type captureCorporateInput struct {
	CompanyName  string `json:"company_name"`
	BillingEmail string `json:"billing_email"`
	ContactName  string `json:"contact_name"`
	ContactPhone string `json:"contact_phone"`
	ContactEmail string `json:"contact_email"`
	Locale       string `json:"locale,omitempty"`
}

// captureCorporate is the B2B front door: one call creates the corporate
// account AND the booker contact linked to it, stamped with the
// corporate_selfserve channel so the funnel reads the motion separately.
func (h *handlers) captureCorporate(w http.ResponseWriter, r *http.Request) {
	var in captureCorporateInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if in.CompanyName == "" || (in.ContactPhone == "" && in.ContactEmail == "") {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	tenantID := tenantOf(r)
	t, err := h.svc.Store.Tenants().Get(r.Context(), tenantID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	acct, err := h.svc.Corporate.CreateAccount(r.Context(), &model.CorporateAccount{
		TenantID: tenantID, Name: in.CompanyName, BillingEmail: in.BillingEmail,
		Currency: t.PricingRules.Currency,
	})
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	res, err := h.svc.Clients.ResolveV2(r.Context(), tenantID, domain.ResolveInput{
		Phone: in.ContactPhone, Email: in.ContactEmail, Name: in.ContactName, Locale: in.Locale,
	})
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if res.Client.OrgID != acct.ID {
		res.Client.OrgID = acct.ID
		_ = h.svc.Store.Clients().Upsert(r.Context(), res.Client)
	}
	h.svc.Events.Track(r.Context(), &model.AttributionEvent{
		TenantID: tenantID, Kind: "corporate_signup", Channel: "corporate_selfserve",
		Campaign: h.activeCampaign(r, t), ClientID: res.Client.ID,
	})
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"account": acct, "contact": res.Client, "contact_is_new": res.IsNew,
	})
}

// ------------------------------------------------------------- O1/O2/O15: campaigns

func (h *handlers) broadcastCampaign(w http.ResponseWriter, r *http.Request) {
	var in domain.BroadcastInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	res, err := h.svc.Campaigns.Broadcast(r.Context(), tenantOf(r), in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// segmentPreview returns the audience size for a ruleset — the number an
// operator sees before pressing send is exactly the broadcast's audience.
func (h *handlers) segmentPreview(w http.ResponseWriter, r *http.Request) {
	var rules model.SegmentRules
	if err := httpx.Decode(r, &rules); err != nil {
		httpx.WriteError(w, err)
		return
	}
	clients, err := h.svc.SegmentClients(r.Context(), tenantOf(r), rules)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	type sample struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var s []sample
	for i, c := range clients {
		if i >= 5 {
			break
		}
		s = append(s, sample{ID: c.ID, Name: c.FullName})
	}
	if s == nil {
		s = []sample{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"count": len(clients), "sample": s})
}

func (h *handlers) campaignFunnel(w http.ResponseWriter, r *http.Request) {
	f, err := h.svc.Campaigns.Funnel(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, f)
}

// ------------------------------------------------------------- helpers

// activeCampaign resolves the tenant's currently-active campaign window
// name (A12), or "" when no window covers now.
func (h *handlers) activeCampaign(r *http.Request, t *model.Tenant) string {
	return t.AcquisitionConfig.ActiveCampaign(h.svc.Now())
}

// refererAttribution (A7) classifies an HTTP Referer into a channel and a
// campaign hint. Unknown or absent referers classify as "" (no attribution
// invented); own-site referers classify as "" (internal navigation is not
// acquisition). The classification table is conservative — a referer we
// don't recognize is "referral" with the bare host, never a guess.
func refererAttribution(referer string) (channel, campaign string) {
	if referer == "" {
		return "", ""
	}
	u, err := url.Parse(referer)
	if err != nil || u.Host == "" {
		return "", ""
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	switch {
	case strings.HasPrefix(host, "google."), strings.HasPrefix(host, "bing."),
		strings.HasPrefix(host, "duckduckgo."), strings.HasPrefix(host, "yahoo."):
		return "seo", "organic:" + strings.Split(host, ".")[0]
	case strings.HasPrefix(host, "instagram."), strings.HasPrefix(host, "facebook."),
		strings.HasPrefix(host, "fb."), strings.HasPrefix(host, "tiktok."),
		strings.HasPrefix(host, "linkedin."), host == "x.com", strings.HasPrefix(host, "twitter."):
		return "social", "referral:" + strings.Split(host, ".")[0]
	}
	return "referral", host
}

// captureChannelURLFor is captureChannelURL with campaign-window override
// (A12): during a window, utm_campaign carries the window name and the
// page/campaign context moves to utm_content, so window traffic groups
// cleanly in the funnel without losing page-level attribution.
func (h *handlers) captureChannelURLFor(r *http.Request, t *model.Tenant, channel, source, campaign string) string {
	if w := h.activeCampaign(r, t); w != "" {
		return fmt.Sprintf("%s/capture?tenant=%s&channel=%s&utm_source=%s&utm_medium=%s&utm_campaign=%s&utm_content=%s",
			h.svc.ConsoleBase(), t.ID, channel, source, channel, w, campaign)
	}
	return h.captureChannelURL(t.ID, channel, source, campaign)
}
