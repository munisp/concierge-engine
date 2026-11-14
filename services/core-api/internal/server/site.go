package server

// Phase 26: public acquisition site (the SEO surface). Server-rendered per
// tenant at /site/{slug}/ — the console SPA is client-rendered and
// invisible to crawlers, so indexable marketing pages live here instead.
//
// Phase 28: optimization wave. Custom domains (host-based tenant
// resolution so authority compounds on the tenant's own domain),
// data-derived guide content (thin content is the #1 ranking risk),
// expanded JSON-LD (Service/Breadcrumb/FAQ/Review/geo/sameAs/priceRange),
// dynamic branded OG images, per-page unique titles, FAQ sections,
// cache/ETag headers, sitemap lastmod + platform sitemap index, hreflang,
// Search Console verification meta, and a public partner directory.
//
// Pages: landing, per-service booking pages, per-area programmatic pages
// (generated from the tenant's real catalog × real service areas — data,
// not thin content), partner directory, sitemap.xml, robots.txt. Every CTA
// lands on the capture flow with channel=seo stamped, so organic arrivals
// attribute correctly through first-touch inheritance and the acquisition
// fee applies when they convert.
//
// Not tenant-middleware scoped: /site/ is public marketing content by
// design (tenant name, catalog, service areas, VETTED partners). No client
// data is ever rendered here.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (h *handlers) registerSite(mux *http.ServeMux) {
	mux.HandleFunc("GET /site/{slug}/", h.siteLanding)
	mux.HandleFunc("GET /site/{slug}/book/{serviceID}", h.siteService)
	mux.HandleFunc("GET /site/{slug}/areas/{areaID}", h.siteArea)
	mux.HandleFunc("GET /site/{slug}/links", h.siteLinks)
	mux.HandleFunc("GET /site/{slug}/partners", h.sitePartners)
	mux.HandleFunc("GET /site/{slug}/sitemap.xml", h.siteSitemap)
	mux.HandleFunc("GET /site/{slug}/robots.txt", h.siteRobots)
	mux.HandleFunc("GET /site/{slug}/og.png", h.siteOGLanding)
	mux.HandleFunc("GET /site/{slug}/book/{serviceID}/og.png", h.siteOGService)
	// Platform-level sitemap index: one entry per tenant sitemap, so the
	// operator submits a single URL to Search Console and every tenant
	// (path-based or custom-domain) is discovered.
	mux.HandleFunc("GET /sitemap.xml", h.platformSitemapIndex)
	// Phase 27: embeddable booking widget — the brownfield play. Tenants
	// with an existing website iframe this page (or paste the snippet the
	// onboarding wizard shows); bookings arrive stamped channel=widget.
	mux.HandleFunc("GET /embed/{slug}", h.embedWidget)
}

// ---------------------------------------------------------- custom domains (Phase 28, lever #1)

// siteHostHeader carries the public origin of a resolved custom domain
// (e.g. https://concierge.acme.com) from the rewrite middleware to the
// page handlers, so canonical/sitemap/robots URLs are built on the
// tenant's own domain — that is where authority must compound.
const siteHostHeader = "X-CE-Site-Host"

// normalizeHost lowercases, strips the port and a leading www. so
// concierge.acme.com, Concierge.acme.com:443 and www.concierge.acme.com
// all resolve to the same tenant.
func normalizeHost(hostport string) string {
	h := strings.ToLower(strings.TrimSpace(hostport))
	if i := strings.LastIndex(h, ":"); i > -1 && !strings.Contains(h[i:], "]") {
		h = h[:i]
	}
	h = strings.TrimPrefix(h, "www.")
	return h
}

// hostRewrite maps custom tenant domains onto the site surface. A request
// to https://concierge.acme.com/book/xyz is internally rewritten to
// /site/{slug}/book/xyz before routing, with the public origin carried on
// an internal header. The header is stripped unconditionally first — it is
// trusted only when set here, after a real domain lookup.
func (h *handlers) hostRewrite(next http.Handler) http.Handler {
	platformHost := ""
	if u, err := url.Parse(h.svc.PublicBase()); err == nil {
		platformHost = normalizeHost(u.Host)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del(siteHostHeader)
		host := normalizeHost(r.Host)
		if host == "" || host == platformHost {
			next.ServeHTTP(w, r)
			return
		}
		t, err := h.svc.Store.Tenants().GetByDomain(r.Context(), host)
		if err != nil {
			next.ServeHTTP(w, r) // unknown host: platform routes still work
			return
		}
		proto := r.Header.Get("X-Forwarded-Proto")
		if proto != "http" && proto != "https" {
			proto = "https"
		}
		r.Header.Set(siteHostHeader, proto+"://"+host)
		if r.URL.Path == "/" {
			r.URL.Path = "/site/" + t.Slug + "/"
		} else {
			r.URL.Path = "/site/" + t.Slug + r.URL.Path
		}
		next.ServeHTTP(w, r)
	})
}

// siteBase builds the public base for a tenant's pages — the custom domain
// when the request arrived on one, otherwise the path-based platform URL.
func (h *handlers) siteBase(r *http.Request, slug string) string {
	if v := r.Header.Get(siteHostHeader); v != "" {
		return v
	}
	return h.svc.PublicBase() + "/site/" + slug
}

// ---------------------------------------------------------- page model + template

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

type siteFAQ struct {
	Q string
	A string
}

type sitePartnerView struct {
	Name      string
	Verticals string
	Markets   string
	BookURL   string
}

type sitePage struct {
	Title        string // unique per page (Phase 28) — falls back to TenantName — SubHead
	TenantName   string
	Description  string
	Canonical    string
	OGImage      string
	Lang         string
	Verification string
	JSONLD       template.JS // json.Marshal output (HTML-escaped by encoding/json)
	Heading      string
	SubHead      string
	Services     []siteServiceView
	Areas        []siteAreaView
	Partners     []sitePartnerView
	Sections     []string // data-derived guide paragraphs (thin-content fix)
	FAQs         []siteFAQ
	CTA          string
	CTALabel     string
	// Phase 27: consented review aggregate — only publish-consented
	// reviews, so the stars shown are exactly the evidence held.
	ReviewCount int
	ReviewAvg   string // e.g. "4.8"
}

var siteTmpl = template.Must(template.New("site").Parse(`<!doctype html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<meta name="description" content="{{.Description}}">
<link rel="canonical" href="{{.Canonical}}">
<link rel="alternate" hreflang="{{.Lang}}" href="{{.Canonical}}">
<link rel="alternate" hreflang="x-default" href="{{.Canonical}}">
{{if .Verification}}<meta name="google-site-verification" content="{{.Verification}}">{{end}}
<meta property="og:type" content="website">
<meta property="og:title" content="{{.Title}}">
<meta property="og:description" content="{{.Description}}">
<meta property="og:url" content="{{.Canonical}}">
{{if .OGImage}}<meta property="og:image" content="{{.OGImage}}">
<meta name="twitter:card" content="summary_large_image">
<meta name="twitter:image" content="{{.OGImage}}">{{else}}<meta name="twitter:card" content="summary">{{end}}
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
.guide{line-height:1.6;color:#333}
dl dt{font-weight:600;margin-top:12px}
dl dd{margin:4px 0 0;color:#444;line-height:1.5}
footer{text-align:center;color:#888;font-size:.85rem;padding:24px}
</style>
</head>
<body>
<header><h1>{{.Heading}}</h1><p>{{.Description}}</p>{{if .ReviewCount}}<p>★ {{.ReviewAvg}} · {{.ReviewCount}} client reviews</p>{{end}}</header>
<main>
{{range .Sections}}<p class="guide">{{.}}</p>{{end}}
{{if .Services}}<h2>Services</h2><ul>{{range .Services}}
<li><a href="{{.URL}}">{{.Name}}</a>{{if .PriceLabel}} <span class="price">— from {{.PriceLabel}}</span>{{end}}</li>
{{end}}</ul>{{end}}
{{if .Areas}}<h2>Where we operate</h2><ul>{{range .Areas}}
<li><a href="{{.URL}}">{{.Name}}</a></li>
{{end}}</ul>{{end}}
{{if .Partners}}<h2>Our partners</h2><ul>{{range .Partners}}
<li>{{.Name}}{{if .Verticals}} <span class="price">— {{.Verticals}}</span>{{end}}</li>
{{end}}</ul>{{end}}
{{if .FAQs}}<h2>Common questions</h2><dl>{{range .FAQs}}
<dt>{{.Q}}</dt><dd>{{.A}}</dd>
{{end}}</dl>{{end}}
<a class="cta" href="{{.CTA}}">{{.CTALabel}}</a>
</main>
<footer>{{.TenantName}} — concierge, arranged end to end.</footer>
</body>
</html>`))

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

// basePage fills the cross-page fields from tenant config: lang, Search
// Console verification meta, and the OG card URL.
func (h *handlers) basePage(r *http.Request, t *model.Tenant, ogPath string) sitePage {
	return sitePage{
		TenantName:   t.Name,
		Lang:         t.SiteConfig.Locale,
		Verification: t.SiteConfig.GoogleVerification,
		OGImage:      h.siteBase(r, t.Slug) + ogPath,
	}
}

func priceLabel(s model.CatalogService) string {
	if s.BaseMinor <= 0 {
		return ""
	}
	return fmt.Sprintf("%s %.2f", s.Currency, float64(s.BaseMinor)/100)
}

func perUnitLabel(currency string, minor int64) string {
	return fmt.Sprintf("%s %.2f", currency, float64(minor)/100)
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

// priceRange derives the schema.org priceRange from the real catalog —
// the cheapest and most expensive base prices, never invented.
func priceRange(services []model.CatalogService) string {
	var lo, hi int64
	for _, s := range services {
		if s.BaseMinor <= 0 {
			continue
		}
		if lo == 0 || s.BaseMinor < lo {
			lo = s.BaseMinor
		}
		if s.BaseMinor > hi {
			hi = s.BaseMinor
		}
	}
	if lo == 0 {
		return ""
	}
	if len(services) > 0 {
		return fmt.Sprintf("%s %d-%d", services[0].Currency, lo/100, hi/100)
	}
	return ""
}

// areasCentroid averages the polygon vertices of all service areas into a
// single representative point for LocalBusiness geo. Honest: it is the
// centroid of real coverage, not a fabricated street address.
func areasCentroid(areas []model.ServiceArea) (lat, lng float64, ok bool) {
	var n int
	for _, a := range areas {
		for _, p := range a.Polygon {
			lat += p[0]
			lng += p[1]
			n++
		}
	}
	if n == 0 {
		return 0, 0, false
	}
	return lat / float64(n), lng / float64(n), true
}

// joinNames renders "A, B and C" (capped) for prose from real data.
func joinNames(names []string, cap int) string {
	if len(names) > cap {
		names = names[:cap]
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// ---------------------------------------------------------- guide content (Phase 28, lever #2)
// Thin content is the top ranking risk for programmatic pages. These
// sections are assembled strictly from real tenant data — services with
// real prices, real areas, real payment rails — rules-generated, never
// AI-invented copy.

func serviceSections(t *model.Tenant, svc *model.CatalogService, areas []model.ServiceArea) []string {
	vertical := strings.ToLower(strings.ReplaceAll(svc.Vertical, "_", " "))
	intro := fmt.Sprintf("%s provides %s arranged end to end by %s. Every booking includes a dedicated concierge, live trip tracking and WhatsApp updates from confirmation to completion.",
		t.Name, indefinite(vertical), t.Name)
	if pl := priceLabel(*svc); pl != "" {
		intro = fmt.Sprintf("%s provides %s from %s, arranged end to end. Every booking includes a dedicated concierge, live trip tracking and WhatsApp updates from confirmation to completion.",
			t.Name, vertical, pl)
	}
	sections := []string{intro}
	if len(areas) > 0 {
		names := make([]string, 0, len(areas))
		for _, a := range areas {
			names = append(names, a.Name)
		}
		sections = append(sections, fmt.Sprintf("%s is available across %s. Coverage is verified per booking — if your pickup sits outside a listed area, your concierge will confirm availability before quoting.",
			svc.Name, joinNames(names, 6)))
	}
	return sections
}

func areaSections(t *model.Tenant, area *model.ServiceArea, services []model.CatalogService) []string {
	if len(services) == 0 {
		return nil
	}
	parts := make([]string, 0, len(services))
	for _, s := range services {
		if pl := priceLabel(s); pl != "" {
			parts = append(parts, fmt.Sprintf("%s (from %s)", s.Name, pl))
		} else {
			parts = append(parts, s.Name)
		}
	}
	return []string{
		fmt.Sprintf("%s operates %d concierge %s in %s: %s. Each is booked with live pricing and confirmed by a dedicated concierge on WhatsApp.",
			t.Name, len(services), pluralizeCount(len(services), "service", "services"), area.Name, joinNames(parts, 8)),
		fmt.Sprintf("Bookings in %s include driver or provider details shared before pickup, live trip tracking for the passenger, and post-trip receipts — all in one WhatsApp thread.", area.Name),
	}
}

func pluralizeCount(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func indefinite(nounPhrase string) string {
	if nounPhrase == "" {
		return "concierge services"
	}
	switch nounPhrase[0] {
	case 'a', 'e', 'i', 'o', 'u':
		return "an " + nounPhrase
	}
	return "a " + nounPhrase
}

// ---------------------------------------------------------- FAQs (Phase 28, lever #6)

// faqsForService builds the FAQ block strictly from catalog/config truth.
// Every answer is verifiable against the tenant's own records.
func faqsForService(t *model.Tenant, svc *model.CatalogService, areas []model.ServiceArea) []siteFAQ {
	var faqs []siteFAQ
	if pl := priceLabel(*svc); pl != "" {
		a := fmt.Sprintf("%s starts at %s.", svc.Name, pl)
		if svc.PerKMMinor > 0 {
			a += fmt.Sprintf(" Additional distance is billed at %s per kilometre.", perUnitLabel(svc.Currency, svc.PerKMMinor))
		}
		if svc.PerHourMinor > 0 {
			a += fmt.Sprintf(" Additional time is billed at %s per hour.", perUnitLabel(svc.Currency, svc.PerHourMinor))
		}
		faqs = append(faqs, siteFAQ{"How much does " + svc.Name + " cost?", a})
	} else {
		faqs = append(faqs, siteFAQ{"How much does " + svc.Name + " cost?",
			"Every booking is quoted live before you commit — your concierge confirms the full price and you approve it first."})
	}
	if len(areas) > 0 {
		names := make([]string, 0, len(areas))
		for _, a := range areas {
			names = append(names, a.Name)
		}
		faqs = append(faqs, siteFAQ{"Where is " + svc.Name + " available?",
			fmt.Sprintf("%s currently serves %s.", t.Name, joinNames(names, 6))})
	}
	faqs = append(faqs, siteFAQ{"How do I book?",
		"Tap the booking button on this page — your concierge confirms the details on WhatsApp within minutes."})
	switch {
	case t.PaymentConfig.DisableStripe && t.PaymentConfig.DisablePaystack:
		faqs = append(faqs, siteFAQ{"How do I pay?", "Your concierge arranges payment with you directly when confirming the booking."})
	case t.PaymentConfig.DisableStripe:
		faqs = append(faqs, siteFAQ{"How do I pay?", "You receive a secure Paystack payment link when your booking is confirmed — card, bank transfer or mobile money where supported."})
	case t.PaymentConfig.DisablePaystack:
		faqs = append(faqs, siteFAQ{"How do I pay?", "You receive a secure Stripe payment link when your booking is confirmed — all major cards and local payment methods where supported."})
	default:
		faqs = append(faqs, siteFAQ{"How do I pay?", "You receive a secure payment link when your booking is confirmed — all major cards and local payment methods where supported."})
	}
	return faqs
}

func faqLD(faqs []siteFAQ) map[string]any {
	list := make([]any, 0, len(faqs))
	for _, f := range faqs {
		list = append(list, map[string]any{
			"@type": "Question", "name": f.Q,
			"acceptedAnswer": map[string]any{"@type": "Answer", "text": f.A},
		})
	}
	return map[string]any{"@type": "FAQPage", "mainEntity": list}
}

// breadcrumbLD: crumbs are (name, url) pairs; an empty url means the
// tenant landing (position 1).
func breadcrumbLD(base string, crumbs ...[2]string) map[string]any {
	items := make([]any, 0, len(crumbs))
	for i, c := range crumbs {
		item := c[1]
		if item == "" {
			item = base + "/"
		}
		items = append(items, map[string]any{
			"@type": "ListItem", "position": i + 1, "name": c[0], "item": item,
		})
	}
	return map[string]any{"@type": "BreadcrumbList", "itemListElement": items}
}

// ---------------------------------------------------------- landing

func (h *handlers) siteLanding(w http.ResponseWriter, r *http.Request) {
	t, services, areas, err := h.siteData(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	base := h.siteBase(r, t.Slug)
	p := h.basePage(r, t, "/og.png")
	p.Description = brandDescription(t)
	p.Canonical = base + "/"
	p.Heading = t.Name
	p.SubHead = "VIP concierge"
	p.CTA, p.CTALabel = h.captureURL(t.ID, "landing"), "Book your concierge"
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
	p.JSONLD = marshalLDGraph(h.landingLD(r, t, p, services, areas)...)
	renderSite(w, r, p)
}

// landingLD builds the LocalBusiness JSON-LD graph, attaching consented
// reviews + aggregate (star rich snippets), geo centroid of real coverage,
// catalog-derived priceRange and configured social sameAs links.
func (h *handlers) landingLD(r *http.Request, t *model.Tenant, p sitePage, services []model.CatalogService, areas []model.ServiceArea) []map[string]any {
	ld := map[string]any{
		"@context": "https://schema.org", "@type": "LocalBusiness",
		"name": t.Name, "description": p.Description, "url": p.Canonical,
	}
	if pr := priceRange(services); pr != "" {
		ld["priceRange"] = pr
	}
	if lat, lng, ok := areasCentroid(areas); ok {
		ld["geo"] = map[string]any{
			"@type":    "GeoCoordinates",
			"latitude": fmt.Sprintf("%.6f", lat), "longitude": fmt.Sprintf("%.6f", lng),
		}
	}
	if len(t.SiteConfig.SocialLinks) > 0 {
		ld["sameAs"] = t.SiteConfig.SocialLinks
	}
	if sum, err := h.svc.ReviewSummary(r.Context(), t.ID); err == nil && sum.Count > 0 {
		ld["aggregateRating"] = map[string]any{
			"@type":       "AggregateRating",
			"ratingValue": fmt.Sprintf("%.1f", sum.Average),
			"reviewCount": sum.Count,
		}
	}
	// Individual review nodes (consented only, capped at 3): eligible for
	// review snippet display alongside the aggregate.
	if reviews, err := h.svc.Store.Reviews().Consented(r.Context(), t.ID); err == nil {
		var nodes []any
		for _, rv := range reviews {
			if len(nodes) >= 3 {
				break
			}
			node := map[string]any{
				"@type": "Review",
				"reviewRating": map[string]any{
					"@type": "Rating", "ratingValue": rv.Rating, "bestRating": 5, "worstRating": 1,
				},
				"author": map[string]any{"@type": "Person", "name": "Verified client"},
			}
			if rv.Text != "" {
				node["reviewBody"] = rv.Text
			}
			nodes = append(nodes, node)
		}
		if len(nodes) > 0 {
			ld["review"] = nodes
		}
	}
	return []map[string]any{ld}
}

// ---------------------------------------------------------- service pages

func (h *handlers) siteService(w http.ResponseWriter, r *http.Request) {
	t, services, areas, err := h.siteData(r)
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
	base := h.siteBase(r, t.Slug)
	desc := fmt.Sprintf("%s by %s — %s, arranged end to end with a dedicated concierge.",
		svc.Name, t.Name, strings.ToLower(strings.ReplaceAll(svc.Vertical, "_", " ")))
	title := fmt.Sprintf("%s | %s", svc.Name, t.Name)
	if pl := priceLabel(*svc); pl != "" {
		title = fmt.Sprintf("%s — from %s | %s", svc.Name, pl, t.Name)
		desc = fmt.Sprintf("%s from %s — %s arranges %s end to end with a dedicated concierge, live tracking and WhatsApp updates.",
			svc.Name, pl, t.Name, strings.ToLower(strings.ReplaceAll(svc.Vertical, "_", " ")))
	}
	p := h.basePage(r, t, fmt.Sprintf("/book/%s/og.png", svc.ID))
	p.Title = title
	p.Description = desc
	p.Canonical = fmt.Sprintf("%s/book/%s", base, svc.ID)
	p.Heading, p.SubHead = svc.Name, svc.Name
	p.CTA, p.CTALabel = h.captureURL(t.ID, "svc-"+svc.ID), "Book "+svc.Name
	p.Services = []siteServiceView{{
		Name: svc.Name, PriceLabel: priceLabel(*svc),
		URL: fmt.Sprintf("%s/book/%s", base, svc.ID),
	}}
	p.Sections = serviceSections(t, svc, areas)
	p.FAQs = faqsForService(t, svc, areas)
	if sum, err := h.svc.ReviewSummary(r.Context(), t.ID); err == nil && sum.Count > 0 {
		p.ReviewCount = sum.Count
		p.ReviewAvg = fmt.Sprintf("%.1f", sum.Average)
	}

	ld := map[string]any{
		"@type": "Service",
		"name":  svc.Name, "provider": map[string]any{"@type": "LocalBusiness", "name": t.Name},
		"description": desc,
	}
	if svc.BaseMinor > 0 {
		ld["offers"] = map[string]any{"@type": "Offer", "price": fmt.Sprintf("%.2f", float64(svc.BaseMinor)/100),
			"priceCurrency": svc.Currency}
	}
	if len(areas) > 0 {
		served := make([]any, 0, len(areas))
		for _, a := range areas {
			served = append(served, map[string]any{"@type": "Place", "name": a.Name})
		}
		ld["areaServed"] = served
	}
	p.JSONLD = marshalLDGraph([]map[string]any{
		ld,
		faqLD(p.FAQs),
		breadcrumbLD(base, [2]string{"Home", ""}, [2]string{svc.Name, p.Canonical}),
	}...)
	renderSite(w, r, p)
}

// ---------------------------------------------------------- area pages

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
	base := h.siteBase(r, t.Slug)
	desc := fmt.Sprintf("%s in %s — %s", t.Name, area.Name, brandDescription(t))
	p := h.basePage(r, t, "/og.png")
	p.Title = fmt.Sprintf("%s in %s — %d %s | Book online", t.Name, area.Name,
		len(services), pluralizeCount(len(services), "concierge service", "concierge services"))
	p.Description = desc
	p.Canonical = fmt.Sprintf("%s/areas/%s", base, area.ID)
	p.Heading = fmt.Sprintf("%s in %s", t.Name, area.Name)
	p.SubHead = "concierge in " + area.Name
	p.CTA, p.CTALabel = h.captureURL(t.ID, "area-"+area.ID), "Book in "+area.Name
	p.Sections = areaSections(t, area, services)
	for _, s := range services {
		p.Services = append(p.Services, siteServiceView{
			Name: s.Name, PriceLabel: priceLabel(s),
			URL: fmt.Sprintf("%s/book/%s", base, s.ID),
		})
	}
	p.JSONLD = marshalLDGraph([]map[string]any{
		{
			"@type":      "Service",
			"name":       fmt.Sprintf("%s — %s", t.Name, area.Name),
			"provider":   map[string]any{"@type": "LocalBusiness", "name": t.Name},
			"areaServed": map[string]any{"@type": "Place", "name": area.Name},
		},
		breadcrumbLD(base, [2]string{"Home", ""}, [2]string{area.Name, p.Canonical}),
	}...)
	renderSite(w, r, p)
}

// ---------------------------------------------------------- partner directory (Phase 28, lever #10)

// sitePartners is the public partner directory — indexable content plus a
// compliant backlink surface. Only VETTED partners are listed (PENDING and
// SUSPENDED never appear), and every "book via partner" link carries the
// partner campaign so co-marketing attribution closes the loop.
func (h *handlers) sitePartners(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.Store.Tenants().GetBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	partners, err := h.svc.Store.Partners().List(r.Context(), t.ID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	base := h.siteBase(r, t.Slug)
	p := h.basePage(r, t, "/og.png")
	p.Title = fmt.Sprintf("Partners of %s — vetted concierge providers", t.Name)
	p.Description = fmt.Sprintf("The vetted partner network behind %s — chauffeur, travel and lifestyle providers confirmed to the same service standard.", t.Name)
	p.Canonical = base + "/partners"
	p.Heading = "Our partners"
	p.SubHead = "vetted partner network"
	p.CTA, p.CTALabel = h.captureURL(t.ID, "partners"), "Book your concierge"
	for _, pt := range partners {
		if pt.Status != "VETTED" {
			continue
		}
		p.Partners = append(p.Partners, sitePartnerView{
			Name:      pt.Name,
			Verticals: joinNames(pt.Verticals, 4),
			Markets:   joinNames(pt.Markets, 4),
			BookURL:   h.captureChannelURL(t.ID, "partner", "directory", "partner:"+pt.ID),
		})
	}
	if n := len(p.Partners); n > 0 {
		p.Sections = []string{fmt.Sprintf("%s works with %d vetted partner %s. Every partner is quality-scored and operates under the same booking, tracking and payment standards as %s itself.",
			t.Name, n, pluralizeCount(n, "provider", "providers"), t.Name)}
	}
	items := make([]any, 0, len(p.Partners))
	for i, pv := range p.Partners {
		items = append(items, map[string]any{
			"@type": "ListItem", "position": i + 1,
			"item": map[string]any{"@type": "Organization", "name": pv.Name},
		})
	}
	p.JSONLD = marshalLDGraph([]map[string]any{
		{"@type": "ItemList", "name": t.Name + " — vetted partners", "itemListElement": items},
		breadcrumbLD(base, [2]string{"Home", ""}, [2]string{"Partners", p.Canonical}),
	}...)
	renderSite(w, r, p)
}

// ---------------------------------------------------------- OG cards (Phase 28, lever #4)

func (h *handlers) siteOGLanding(w http.ResponseWriter, r *http.Request) {
	t, services, _, err := h.siteData(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	sub := brandDescription(t)
	rating := ""
	if sum, err := h.svc.ReviewSummary(r.Context(), t.ID); err == nil && sum.Count > 0 {
		rating = ogRatingLine(fmt.Sprintf("%.1f", sum.Average), sum.Count)
	}
	footer := fmt.Sprintf("%d %s — book your concierge", len(services),
		pluralizeCount(len(services), "service", "services"))
	renderOG(w, t.Name, sub, rating, footer)
}

func (h *handlers) siteOGService(w http.ResponseWriter, r *http.Request) {
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
	sub := "arranged end to end by " + t.Name
	if pl := priceLabel(*svc); pl != "" {
		sub = "from " + pl + " — arranged end to end"
	}
	renderOG(w, svc.Name, sub, "", t.Name+" — book your concierge")
}

// ---------------------------------------------------------- sitemap + robots

func (h *handlers) siteSitemap(w http.ResponseWriter, r *http.Request) {
	t, services, areas, err := h.siteData(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	base := h.siteBase(r, t.Slug)
	// lastmod: the freshest real timestamp we hold for site content. Never
	// future-dated, never fabricated per-URL — an honest floor.
	lastmod := t.CreatedAt
	for _, a := range areas {
		if a.CreatedAt.After(lastmod) {
			lastmod = a.CreatedAt
		}
	}
	lm := lastmod.Format("2006-01-02")
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	writeURL := func(loc, priority string) {
		fmt.Fprintf(&b, "<url><loc>%s</loc><lastmod>%s</lastmod><priority>%s</priority></url>\n",
			template.HTMLEscapeString(loc), lm, priority)
	}
	writeURL(base+"/", "1.0")
	writeURL(base+"/links", "0.5")
	writeURL(base+"/partners", "0.6")
	for _, s := range services {
		writeURL(fmt.Sprintf("%s/book/%s", base, s.ID), "0.9")
	}
	for _, a := range areas {
		writeURL(fmt.Sprintf("%s/areas/%s", base, a.ID), "0.8")
	}
	b.WriteString("</urlset>\n")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(b.String()))
}

func (h *handlers) siteRobots(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.Store.Tenants().GetBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	fmt.Fprintf(w, "User-agent: *\nAllow: /\nSitemap: %s/sitemap.xml\n", h.siteBase(r, t.Slug))
}

// platformSitemapIndex lists every tenant sitemap — the single URL an
// operator submits to Search Console to discover all tenants.
func (h *handlers) platformSitemapIndex(w http.ResponseWriter, r *http.Request) {
	tenants, err := h.svc.Store.Tenants().List(r.Context())
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, t := range tenants {
		base := h.svc.PublicBase() + "/site/" + t.Slug
		if len(t.SiteConfig.CustomDomains) > 0 {
			base = "https://" + t.SiteConfig.CustomDomains[0]
		}
		fmt.Fprintf(&b, "<sitemap><loc>%s/sitemap.xml</loc></sitemap>\n", template.HTMLEscapeString(base))
	}
	b.WriteString("</sitemapindex>\n")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(b.String()))
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
	base := h.siteBase(r, t.Slug)
	p := h.basePage(r, t, "/og.png")
	p.Description = brandDescription(t)
	p.Canonical = base + "/links"
	p.Heading, p.SubHead = t.Name, "book your concierge"
	p.CTA, p.CTALabel = h.captureChannelURL(t.ID, "social", "bio", "links"), "Book now"
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
	renderSite(w, r, p)
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

// ---------------------------------------------------------- rendering

// marshalLD renders a single JSON-LD node. encoding/json escapes <, >, &
// as unicode escapes, so the output is safe inside a <script> tag.
func marshalLD(v map[string]any) template.JS {
	b, _ := json.Marshal(v)
	return template.JS(b) //nolint:gosec // see above — json output is script-safe
}

// marshalLDGraph wraps multiple nodes in one @graph block so a page can
// carry LocalBusiness + FAQPage + BreadcrumbList in a single script tag.
func marshalLDGraph(nodes ...map[string]any) template.JS {
	b, _ := json.Marshal(map[string]any{
		"@context": "https://schema.org", "@graph": nodes,
	})
	return template.JS(b) //nolint:gosec // json output is script-safe
}

// renderSite renders through a buffer so the response carries an ETag and
// cache headers (Phase 28, lever #7): repeat crawls get 304s, and pages
// are publicly cacheable for 5 minutes — content changes are reflected
// fast while crawlers and CDNs stay off the hot path.
func renderSite(w http.ResponseWriter, r *http.Request, p sitePage) {
	if p.Title == "" {
		p.Title = p.TenantName + " — " + p.SubHead
	}
	if p.Lang == "" {
		p.Lang = "en"
	}
	var buf bytes.Buffer
	if err := siteTmpl.Execute(&buf, p); err != nil {
		httpx.WriteError(w, err)
		return
	}
	body := buf.Bytes()
	sum := fnv.New32a()
	_, _ = sum.Write(body)
	etag := fmt.Sprintf(`W/"%08x"`, sum.Sum32())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(body)
}
