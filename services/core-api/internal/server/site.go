package server

// Phase 26: public acquisition site (the SEO surface). Server-rendered per
// tenant at /site/{slug}/ — the console SPA is client-rendered and
// invisible to crawlers, so indexable marketing pages live here instead.
//
// Pages: landing, per-service booking pages, per-area programmatic pages
// (generated from the tenant's real catalog × real service areas — data,
// not thin content), sitemap.xml, robots.txt. Every CTA lands on the
// capture flow with channel=seo stamped, so organic arrivals attribute
// correctly through first-touch inheritance and the acquisition fee
// applies when they convert.
//
// Not tenant-middleware scoped: /site/ is public marketing content by
// design (tenant name, catalog, service areas). No client data is ever
// rendered here.

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (h *handlers) registerSite(mux *http.ServeMux) {
	mux.HandleFunc("GET /site/{slug}/", h.siteLanding)
	mux.HandleFunc("GET /site/{slug}/book/{serviceID}", h.siteService)
	mux.HandleFunc("GET /site/{slug}/areas/{areaID}", h.siteArea)
	mux.HandleFunc("GET /site/{slug}/links", h.siteLinks)
	mux.HandleFunc("GET /site/{slug}/sitemap.xml", h.siteSitemap)
	mux.HandleFunc("GET /site/{slug}/robots.txt", h.siteRobots)
	// Phase 27: embeddable booking widget — the brownfield play. Tenants
	// with an existing website iframe this page (or paste the snippet the
	// onboarding wizard shows); bookings arrive stamped channel=widget.
	mux.HandleFunc("GET /embed/{slug}", h.embedWidget)
}

type siteServiceView struct {
	Name       string
	PriceLabel string
	URL        string
	Vertical   string
}

type siteAreaView struct {
	Name string
	URL  string
}

type sitePage struct {
	TenantName  string
	Description string
	Canonical   string
	OGImage     string
	JSONLD      template.JS // json.Marshal output (HTML-escaped by encoding/json)
	Heading     string
	SubHead     string
	Services    []siteServiceView
	Areas       []siteAreaView
	CTA         string
	CTALabel    string
	// Phase 27: consented review aggregate — only publish-consented
	// reviews, so the stars shown are exactly the evidence held.
	ReviewCount int
	ReviewAvg   string // e.g. "4.8"
}

var siteTmpl = template.Must(template.New("site").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.TenantName}} — {{.SubHead}}</title>
<meta name="description" content="{{.Description}}">
<link rel="canonical" href="{{.Canonical}}">
<meta property="og:type" content="website">
<meta property="og:title" content="{{.TenantName}} — {{.SubHead}}">
<meta property="og:description" content="{{.Description}}">
<meta property="og:url" content="{{.Canonical}}">
<meta name="twitter:card" content="summary">
<script type="application/ld+json">{{.JSONLD}}</script>
<style>
body{font-family:system-ui,-apple-system,sans-serif;margin:0;color:#1a1a1a;background:#fafafa}
header{background:#0f1b2d;color:#fff;padding:48px 24px;text-align:center}
header h1{margin:0 0 8px;font-size:2rem}
header p{margin:0;opacity:.85}
main{max-width:760px;margin:0 auto;padding:32px 20px}
h2{font-size:1.2rem;border-bottom:2px solid #e5e5e5;padding-bottom:6px}
ul{list-style:none;padding:0}
li{padding:10px 0;border-bottom:1px solid #eee}
a{color:#0b5fff;text-decoration:none}
.cta{display:inline-block;background:#0b5fff;color:#fff;padding:14px 28px;border-radius:8px;font-weight:600;margin:24px 0}
.cta:hover{background:#0946c0;color:#fff}
.price{color:#555;font-size:.95rem}
footer{text-align:center;color:#888;font-size:.85rem;padding:24px}
</style>
</head>
<body>
<header><h1>{{.Heading}}</h1><p>{{.Description}}</p>{{if .ReviewCount}}<p>★ {{.ReviewAvg}} · {{.ReviewCount}} client reviews</p>{{end}}</header>
<main>
{{if .Services}}<h2>Services</h2><ul>{{range .Services}}
<li><a href="{{.URL}}">{{.Name}}</a>{{if .PriceLabel}} <span class="price">— from {{.PriceLabel}}</span>{{end}}</li>
{{end}}</ul>{{end}}
{{if .Areas}}<h2>Where we operate</h2><ul>{{range .Areas}}
<li><a href="{{.URL}}">{{.Name}}</a></li>
{{end}}</ul>{{end}}
<a class="cta" href="{{.CTA}}">{{.CTALabel}}</a>
</main>
<footer>{{.TenantName}} — concierge, arranged end to end.</footer>
</body>
</html>`))

func (h *handlers) siteBase(slug string) string {
	return h.svc.PublicBase() + "/site/" + slug
}

// siteData loads the tenant + active catalog + service areas for a slug.
func (h *handlers) siteData(r *http.Request) (*model.Tenant, []model.CatalogService, []model.ServiceArea, error) {
	t, err := h.svc.Store.Tenants().GetBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		return nil, nil, nil, err
	}
	services, err := h.svc.Store.Catalog().List(r.Context(), t.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	active := services[:0]
	for _, s := range services {
		if s.Active {
			active = append(active, s)
		}
	}
	areas, err := h.svc.Store.Geo().Areas(r.Context(), t.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	return t, active, areas, nil
}

func priceLabel(s model.CatalogService) string {
	if s.BaseMinor <= 0 {
		return ""
	}
	return fmt.Sprintf("%s %.2f", s.Currency, float64(s.BaseMinor)/100)
}

// captureURL builds the CTA into the booking funnel with seo attribution
// stamped. ConsoleBase empty = same-origin (console served beside the API).
func (h *handlers) captureURL(tenantID, campaign string) string {
	return h.captureChannelURL(tenantID, "seo", "organic", campaign)
}

// captureChannelURL is the channel-parameterized variant (Phase 27): qr,
// widget and other surfaces stamp their own channel so first-touch
// attribution — and the acquisition fee — classify correctly.
func (h *handlers) captureChannelURL(tenantID, channel, source, campaign string) string {
	return fmt.Sprintf("%s/capture?tenant=%s&channel=%s&utm_source=%s&utm_medium=%s&utm_campaign=%s",
		h.svc.ConsoleBase(), tenantID, channel, source, channel, campaign)
}

func brandDescription(t *model.Tenant) string {
	if v, ok := t.Brand["tagline"].(string); ok && v != "" {
		return v
	}
	if v, ok := t.Brand["description"].(string); ok && v != "" {
		return v
	}
	return "VIP concierge — travel, chauffeur and lifestyle arranged end to end."
}

func (h *handlers) siteLanding(w http.ResponseWriter, r *http.Request) {
	t, services, areas, err := h.siteData(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	base := h.siteBase(t.Slug)
	p := sitePage{
		TenantName: t.Name, Description: brandDescription(t),
		Canonical: base + "/", Heading: t.Name,
		SubHead: "VIP concierge",
		CTA:     h.captureURL(t.ID, "landing"), CTALabel: "Book your concierge",
	}
	if sum, err := h.svc.ReviewSummary(r.Context(), t.ID); err == nil && sum.Count > 0 {
		p.ReviewCount = sum.Count
		p.ReviewAvg = fmt.Sprintf("%.1f", sum.Average)
	}
	for _, s := range services {
		p.Services = append(p.Services, siteServiceView{
			Name: s.Name, PriceLabel: priceLabel(s), Vertical: s.Vertical,
			URL: fmt.Sprintf("%s/book/%s", base, s.ID),
		})
	}
	for _, a := range areas {
		p.Areas = append(p.Areas, siteAreaView{Name: a.Name, URL: fmt.Sprintf("%s/areas/%s", base, a.ID)})
	}
	p.JSONLD = marshalLD(h.landingLD(r, t, p))
	renderSite(w, p)
}

// landingLD builds the LocalBusiness JSON-LD, attaching the consented
// review aggregate when one exists — aggregateRating is what earns star
// rich snippets in search results.
func (h *handlers) landingLD(r *http.Request, t *model.Tenant, p sitePage) map[string]any {
	ld := map[string]any{
		"@context": "https://schema.org", "@type": "LocalBusiness",
		"name": t.Name, "description": p.Description, "url": p.Canonical,
	}
	if sum, err := h.svc.ReviewSummary(r.Context(), t.ID); err == nil && sum.Count > 0 {
		ld["aggregateRating"] = map[string]any{
			"@type":       "AggregateRating",
			"ratingValue": fmt.Sprintf("%.1f", sum.Average),
			"reviewCount": sum.Count,
		}
	}
	return ld
}

func (h *handlers) siteService(w http.ResponseWriter, r *http.Request) {
	t, services, _, err := h.siteData(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var svc *model.CatalogService
	for i := range services {
		if services[i].ID == r.PathValue("serviceID") {
			svc = &services[i]
			break
		}
	}
	if svc == nil {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	base := h.siteBase(t.Slug)
	desc := fmt.Sprintf("%s by %s — %s, arranged end to end with a dedicated concierge.",
		svc.Name, t.Name, strings.ToLower(strings.ReplaceAll(svc.Vertical, "_", " ")))
	p := sitePage{
		TenantName: t.Name, Description: desc,
		Canonical: fmt.Sprintf("%s/book/%s", base, svc.ID),
		Heading:   svc.Name, SubHead: svc.Name,
		CTA: h.captureURL(t.ID, "svc-"+svc.ID), CTALabel: "Book " + svc.Name,
		Services: []siteServiceView{{
			Name: svc.Name, PriceLabel: priceLabel(*svc),
			URL: fmt.Sprintf("%s/book/%s", base, svc.ID),
		}},
	}
	ld := map[string]any{
		"@context": "https://schema.org", "@type": "Service",
		"name": svc.Name, "provider": map[string]any{"@type": "LocalBusiness", "name": t.Name},
		"description": desc,
	}
	if svc.BaseMinor > 0 {
		ld["offers"] = map[string]any{"@type": "Offer", "price": fmt.Sprintf("%.2f", float64(svc.BaseMinor)/100),
			"priceCurrency": svc.Currency}
	}
	p.JSONLD = marshalLD(ld)
	renderSite(w, p)
}

func (h *handlers) siteArea(w http.ResponseWriter, r *http.Request) {
	t, services, areas, err := h.siteData(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var area *model.ServiceArea
	for i := range areas {
		if areas[i].ID == r.PathValue("areaID") {
			area = &areas[i]
			break
		}
	}
	if area == nil {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	base := h.siteBase(t.Slug)
	desc := fmt.Sprintf("%s in %s — %s", t.Name, area.Name, brandDescription(t))
	p := sitePage{
		TenantName: t.Name, Description: desc,
		Canonical: fmt.Sprintf("%s/areas/%s", base, area.ID),
		Heading:   fmt.Sprintf("%s in %s", t.Name, area.Name),
		SubHead:   "concierge in " + area.Name,
		CTA:       h.captureURL(t.ID, "area-"+area.ID), CTALabel: "Book in " + area.Name,
	}
	for _, s := range services {
		p.Services = append(p.Services, siteServiceView{
			Name: s.Name, PriceLabel: priceLabel(s),
			URL: fmt.Sprintf("%s/book/%s", base, s.ID),
		})
	}
	p.JSONLD = marshalLD(map[string]any{
		"@context": "https://schema.org", "@type": "Service",
		"name":       fmt.Sprintf("%s — %s", t.Name, area.Name),
		"provider":   map[string]any{"@type": "LocalBusiness", "name": t.Name},
		"areaServed": map[string]any{"@type": "Place", "name": area.Name},
	})
	renderSite(w, p)
}

func (h *handlers) siteSitemap(w http.ResponseWriter, r *http.Request) {
	t, services, areas, err := h.siteData(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	base := h.siteBase(t.Slug)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	writeURL := func(loc string) {
		fmt.Fprintf(&b, "<url><loc>%s</loc></url>\n", template.HTMLEscapeString(loc))
	}
	writeURL(base + "/")
	writeURL(base + "/links")
	for _, s := range services {
		writeURL(fmt.Sprintf("%s/book/%s", base, s.ID))
	}
	for _, a := range areas {
		writeURL(fmt.Sprintf("%s/areas/%s", base, a.ID))
	}
	b.WriteString("</urlset>\n")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

func (h *handlers) siteRobots(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.Store.Tenants().GetBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "User-agent: *\nAllow: /\nSitemap: %s/sitemap.xml\n", h.siteBase(t.Slug))
}

// ---------------------------------------------------------- link-in-bio (Phase 27, innovation #2)

// siteLinks is the mobile-first "link in bio" page for Instagram/TikTok/
// WhatsApp bios: every booking CTA on one thumb-reach page. CTAs stamp
// channel=social so bio traffic attributes separately from organic SEO.
func (h *handlers) siteLinks(w http.ResponseWriter, r *http.Request) {
	t, services, _, err := h.siteData(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	base := h.siteBase(t.Slug)
	p := sitePage{
		TenantName: t.Name, Description: brandDescription(t),
		Canonical: base + "/links", Heading: t.Name, SubHead: "book your concierge",
		CTA: h.captureChannelURL(t.ID, "social", "bio", "links"), CTALabel: "Book now",
	}
	for _, s := range services {
		p.Services = append(p.Services, siteServiceView{
			Name: s.Name, PriceLabel: priceLabel(s),
			URL: fmt.Sprintf("%s/book/%s", base, s.ID),
		})
	}
	p.JSONLD = marshalLD(map[string]any{
		"@context": "https://schema.org", "@type": "ItemList",
		"name": t.Name + " — booking links",
	})
	renderSite(w, p)
}

// ---------------------------------------------------------- embeddable widget (Phase 27, innovation #8)

// embedWidget is the iframe-able booking entry point for tenants who keep
// their existing website. Stripped chrome (no header/nav), target=_top so
// the CTA takes over the parent's tab, attribution channel=widget.
func (h *handlers) embedWidget(w http.ResponseWriter, r *http.Request) {
	t, services, _, err := h.siteData(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	cta := h.captureChannelURL(t.ID, "widget", "embed", "widget-"+t.Slug)
	var items strings.Builder
	for _, s := range services {
		fmt.Fprintf(&items, `<li><a target="_top" href="%s">Book %s%s</a></li>`,
			template.HTMLEscapeString(cta), template.HTMLEscapeString(s.Name),
			template.HTMLEscapeString(priceLabel(s)))
	}
	if items.Len() == 0 {
		// No catalog yet — the widget still books (the concierge conversation
		// starts and the catalog is quoted live).
		fmt.Fprintf(&items, `<li><a target="_top" href="%s">Book your concierge</a></li>`,
			template.HTMLEscapeString(cta))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>body{font-family:system-ui,sans-serif;margin:0;padding:12px;background:#fff}
a{display:block;background:#0b5fff;color:#fff;text-decoration:none;padding:12px;border-radius:8px;margin:8px 0;text-align:center;font-weight:600}
a:hover{background:#0946c0}</style></head><body>%s</body></html>`, items.String())
}

// marshalLD renders a JSON-LD block. encoding/json escapes <, >, & as
// unicode escapes, so the output is safe inside a <script> tag.
func marshalLD(v map[string]any) template.JS {
	b, _ := json.Marshal(v)
	return template.JS(b) //nolint:gosec // see above — json output is script-safe
}

func renderSite(w http.ResponseWriter, p sitePage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = siteTmpl.Execute(w, p)
}
