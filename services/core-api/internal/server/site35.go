package server

// Phase 35 public site acquisition pages:
//
//	A1  /site/{slug}/airport/{IATA}       airport transfer SEO pages
//	A2  /site/{slug}/routes/{from}-to-{to} city-pair estimate pages
//	A3  POST /site/{slug}/estimate        anonymous instant estimate
//	A14 /site/{slug}/gift                 gift voucher storefront
//
// Honesty gates: an airport page 404s unless a tenant service area actually
// covers the airport; a route page 404s unless the tenant has an
// auto-quotable per-km service to price it with; the estimate endpoint
// prices from the live catalog only. Nothing here invents coverage.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// ------------------------------------------------------------- A1: airport pages

func (h *handlers) siteAirport(w http.ResponseWriter, r *http.Request) {
	t, services, _, err := h.siteData(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	ap := airportByIATA(strings.ToUpper(r.PathValue("iata")))
	if ap == nil {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	// Honesty gate: no coverage, no page.
	hits, err := h.svc.Geo.Contains(r.Context(), t.ID, ap.Lat, ap.Lng)
	if err != nil || len(hits) == 0 {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	locale, ok := requestLocale(r, t)
	if !ok {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	rawBase := h.siteBase(r, t.Slug)
	base := localeBase(rawBase, t, locale)
	p := h.basePage(r, t, locale, "/og.png")
	p.Title = ap.Name + " transfers — " + t.Name
	p.Description = fmt.Sprintf("Chauffeur transfers and VIP meet & greet at %s (%s), arranged end to end by %s.",
		ap.Name, ap.Code, t.Name)
	p.Canonical = fmt.Sprintf("%s/airport/%s", base, ap.Code)
	p.HrefLangs = h.hreflangCluster(t, rawBase, "/airport/"+ap.Code)
	p.Heading = ap.Name
	p.SubHead = "Airport transfers & VIP meet & greet"
	p.CTA, p.CTALabel = h.captureChannelURLFor(r, t, "seo", "organic", "airport-"+ap.Code), p.D.BookYourConcierge
	// Only airport-relevant, bookable services are listed.
	var relevant []model.CatalogService
	for _, s := range services {
		switch s.Vertical {
		case "AIRPORT_MEET_GREET", "CHAUFFEUR_POINT_TO_POINT", "HELICOPTER":
			relevant = append(relevant, s)
		}
	}
	p.Services = serviceCards(t, relevant, base, rawBase, locale,
		func(id string) string { return h.liveMediaURL(r.Context(), t.ID, rawBase, id) })
	p.FAQs = []siteFAQ{
		{Q: "Do you monitor my flight?",
			A: "Yes — share your flight number and pickup timing tracks the real arrival, not the schedule."},
		{Q: "Where do I meet my chauffeur?",
			A: "Inside the arrivals hall with a name board, or at a pre-agreed point — confirmed in your booking message."},
		{Q: "How do I book?",
			A: "One message. Your concierge confirms the quote, the chauffeur and the meeting point end to end."},
	}
	p.JSONLD = marshalLDGraph(
		map[string]any{
			"@context": "https://schema.org", "@type": "Service",
			"name":     ap.Name + " transfers — " + t.Name,
			"provider": map[string]any{"@type": "LocalBusiness", "name": t.Name},
			"areaServed": map[string]any{
				"@type": "Airport", "name": ap.Name, "iataCode": ap.Code,
			},
		},
		faqLD(p.FAQs),
		breadcrumbLD(base, [2]string{"Home", base + "/"}, [2]string{ap.Code, p.Canonical}),
	)
	renderSite(w, r, p)
}

// ------------------------------------------------------------- A2: route pages

func (h *handlers) siteRoute(w http.ResponseWriter, r *http.Request) {
	t, services, _, err := h.siteData(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	parts := strings.SplitN(r.PathValue("pair"), "-to-", 2)
	if len(parts) != 2 {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	from, to := cityBySlug(parts[0]), cityBySlug(parts[1])
	if from == nil || to == nil || from.Code == to.Code {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	// Honesty gate: the estimate is priced from the tenant's own catalog —
	// no auto-quotable per-km service, no page.
	var perKM *model.CatalogService
	for i := range services {
		s := &services[i]
		if s.Vertical == "CHAUFFEUR_POINT_TO_POINT" && s.PerKMMinor > 0 && s.AutoQuotable {
			perKM = s
			break
		}
	}
	if perKM == nil {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	km := domain.EstimateKM(from.Lat, from.Lng, to.Lat, to.Lng, t.PricingRules)
	total := perKM.BaseMinor + int64(float64(perKM.PerKMMinor)*km+0.5)
	currency := t.PricingRules.Currency
	if currency == "" {
		currency = "EUR"
	}
	locale, ok := requestLocale(r, t)
	if !ok {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	rawBase := h.siteBase(r, t.Slug)
	base := localeBase(rawBase, t, locale)
	p := h.basePage(r, t, locale, "/og.png")
	pair := fmt.Sprintf("%s to %s", from.Name, to.Name)
	p.Title = pair + " chauffeur — " + t.Name
	p.Description = fmt.Sprintf("%s by chauffeur with %s: %.0f km, from %s %.2f — priced from the live catalog.",
		pair, t.Name, km, currency, float64(total)/100)
	p.Canonical = fmt.Sprintf("%s/routes/%s-to-%s", base, from.Code, to.Code)
	p.Heading = pair
	p.SubHead = fmt.Sprintf("%.0f km by road · from %s %.2f", km, currency, float64(total)/100)
	p.CTA, p.CTALabel = h.captureChannelURLFor(r, t, "seo", "organic", "route-"+from.Code+"-"+to.Code),
		p.D.BookYourConcierge
	p.FAQs = []siteFAQ{
		{Q: "Is this price final?",
			A: "It's an indicative estimate from the live catalog rate. Your exact quote — vehicle class, timing, extras — confirms instantly in chat before you commit."},
		{Q: "Can I book a return or a multi-stop day?",
			A: "Yes — hourly hire and multi-stop itineraries are quoted in the same conversation."},
	}
	p.JSONLD = marshalLDGraph(
		map[string]any{
			"@context": "https://schema.org", "@type": "Service",
			"name":     pair + " chauffeur — " + t.Name,
			"provider": map[string]any{"@type": "LocalBusiness", "name": t.Name},
			"offers": map[string]any{
				"@type": "Offer", "priceCurrency": currency,
				"price":       fmt.Sprintf("%.2f", float64(total)/100),
				"description": "Indicative estimate from live catalog rates",
			},
		},
		faqLD(p.FAQs),
		breadcrumbLD(base, [2]string{"Home", base + "/"}, [2]string{pair, p.Canonical}),
	)
	renderSite(w, r, p)
}

// ------------------------------------------------------------- A3: anonymous estimate

type estimateInput struct {
	ServiceID  string  `json:"service_id"`
	PickupLat  float64 `json:"pickup_lat,omitempty"`
	PickupLng  float64 `json:"pickup_lng,omitempty"`
	DropoffLat float64 `json:"dropoff_lat,omitempty"`
	DropoffLng float64 `json:"dropoff_lng,omitempty"`
	Hours      int     `json:"hours,omitempty"`
}

// siteEstimate prices one service anonymously from the live catalog — the
// lead magnet behind route/airport pages and third-party widgets. No
// client record, no request row: a price lookup, nothing more. Per-km
// verticals need coordinates; hourly needs hours; base-only services price
// with neither.
func (h *handlers) siteEstimate(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.Store.Tenants().GetBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var in estimateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	services, err := h.svc.Store.Catalog().List(r.Context(), t.ID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var svc *model.CatalogService
	for i := range services {
		if services[i].ID == in.ServiceID {
			svc = &services[i]
			break
		}
	}
	if svc == nil || !svc.AutoQuotable {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	item := model.ServiceItem{
		Vertical: svc.Vertical, PickupLat: in.PickupLat, PickupLng: in.PickupLng,
		DropoffLat: in.DropoffLat, DropoffLng: in.DropoffLng, Hours: in.Hours,
	}
	lines := domain.PriceItem(*svc, item, t.PricingRules)
	var total int64
	for _, l := range lines {
		total += l.AmountMinor
	}
	currency := t.PricingRules.Currency
	if currency == "" {
		currency = "EUR"
	}
	h.svc.Events.Track(r.Context(), &model.AttributionEvent{
		TenantID: t.ID, Kind: "estimate_view", Channel: "seo",
		Campaign: h.activeCampaign(r, t),
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"lines": lines, "total_minor": total, "currency": currency,
		"indicative": true, // tax and final routing confirm in the real quote
	})
}

// ------------------------------------------------------------- A14: gift storefront

// siteGift lists the tenant's LIVE gift vouchers (promo campaigns flagged
// gift, active, unexpired). Purchasing is a concierge conversation — the
// CTA opens WhatsApp/capture with the voucher code pre-referenced, so no
// payment flow is implied that the tenant hasn't configured.
func (h *handlers) siteGift(w http.ResponseWriter, r *http.Request) {
	t, _, _, err := h.siteData(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	locale, ok := requestLocale(r, t)
	if !ok {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	promos, err := h.svc.Store.Promos().List(r.Context(), t.ID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	now := h.svc.Now()
	var gifts []model.PromoCampaign
	for _, pr := range promos {
		if !pr.Gift || !pr.Active {
			continue
		}
		if !pr.ExpiresAt.IsZero() && now.After(pr.ExpiresAt) {
			continue
		}
		gifts = append(gifts, pr)
	}
	if len(gifts) == 0 {
		httpx.WriteError(w, httpx.ErrNotFound) // no storefront without stock
		return
	}
	rawBase := h.siteBase(r, t.Slug)
	base := localeBase(rawBase, t, locale)
	p := h.basePage(r, t, locale, "/og.png")
	p.Title = "Gift the experience — " + t.Name
	p.Description = "Gift vouchers for " + t.Name + " — the concierge experience, given."
	p.Canonical = base + "/gift"
	p.Heading = "Gift the experience"
	p.SubHead = "A concierge gift — arranged end to end"
	p.CTA, p.CTALabel = h.captureChannelURLFor(r, t, "seo", "organic", "gift"), p.D.BookYourConcierge
	for _, g := range gifts {
		p.Sections = append(p.Sections, fmt.Sprintf("%s — %s %.2f of concierge credit (code %s)",
			g.Code, g.Currency, float64(g.CreditMinor)/100, g.Code))
	}
	p.JSONLD = marshalLD(breadcrumbLD(base, [2]string{"Home", base + "/"}, [2]string{"Gift", p.Canonical}))
	renderSite(w, r, p)
}
