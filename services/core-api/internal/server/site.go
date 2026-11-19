package server

// Phase 26: public acquisition site (the SEO surface). Server-rendered per
// tenant at /site/{slug}/ — the console SPA is client-rendered and
// invisible to crawlers, so indexable marketing pages live here instead.
//
// Phase 28: optimization wave — custom domains, data-derived guide
// content, expanded JSON-LD, OG images, cache/ETag, sitemap lastmod +
// platform index, hreflang, Search Console verification, partner
// directory.
//
// Phase 29: multi-locale engine + premium redesign. Each configured locale
// gets its own URL tree (/site/{slug}/{locale}/...) with a full hreflang
// cluster; names/descriptions resolve from per-resource i18n maps. The
// template is rebuilt to a top-player layout (hero, card grid, trust bar,
// sticky CTA) and every page carries a WhatsApp chat CTA routed into the
// AI concierge — inquiries are answered by the agent with policy-gated
// tools and human handoff, not a contact form.
//
// Pages: landing, per-service booking pages, per-area programmatic pages,
// partner directory, link-in-bio, sitemap.xml, robots.txt. Every CTA lands
// on the capture flow with channel=seo stamped, so organic arrivals
// attribute correctly through first-touch inheritance and the acquisition
// fee applies when they convert.
//
// Not tenant-middleware scoped: /site/ is public marketing content by
// design. No client data is ever rendered here.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// themeCSS renders the studio theme as a :root override block (Phase 30).
// Tokens are validated at write time (SetTheme), so this can emit raw CSS
// without re-escaping; an unset theme emits nothing and the template
// defaults (the noir-gold preset) apply.
func themeCSS(theme *model.SiteTheme) template.CSS {
	if theme == nil {
		return ""
	}
	r := domain.ResolveTheme(theme)
	font := "Georgia,'Times New Roman',serif"
	if r.Font == "sans" {
		font = "system-ui,sans-serif"
	}
	radius := "8px"
	if r.Corners == "sharp" {
		radius = "0"
	}
	return template.CSS(fmt.Sprintf(":root{--ink:%s;--accent:%s;--font-main:%s;--radius:%s}",
		r.Primary, r.Accent, font, radius))
}

func (h *handlers) registerSite(mux *http.ServeMux) {
	// One subtree handler for the whole tenant site. Go's ServeMux cannot
	// express "/site/{slug}/{locale}/book/{id}" alongside
	// "/site/{slug}/book/{id}" (wildcard and literal segments overlap), so
	// the optional locale prefix is resolved by siteDispatch instead.
	mux.HandleFunc("GET /site/{slug}/", h.siteDispatch)
	// Platform-level sitemap index: one entry per tenant sitemap, so the
	// operator submits a single URL to Search Console and every tenant
	// (path-based or custom-domain) is discovered.
	mux.HandleFunc("GET /sitemap.xml", h.platformSitemapIndex)
	// Phase 27: embeddable booking widget — the brownfield play. Tenants
	// with an existing website iframe this page (or paste the snippet the
	// onboarding wizard shows); bookings arrive stamped channel=widget.
	mux.HandleFunc("GET /embed/{slug}", h.embedWidget)
}

// siteReserved are the fixed first segments of the site surface — a first
// segment that is not one of these is treated as a locale prefix and
// validated against the tenant's configured locales.
var siteReserved = map[string]bool{
	"book": true, "areas": true, "links": true, "partners": true,
	"sitemap.xml": true, "robots.txt": true, "og.png": true,
	"media": true, // Phase 31: public media serving
	// Phase 35: acquisition pages
	"airport": true, "routes": true, "gift": true,
}

// siteDispatch routes /site/{slug}/... manually: optional {locale} prefix,
// then the page kind. Unknown shapes 404; unconfigured locale prefixes 404
// inside the handlers via requestLocale (no duplicate-content pages).
func (h *handlers) siteDispatch(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	rest := strings.TrimPrefix(r.URL.Path, "/site/"+slug+"/")
	segs := strings.Split(strings.Trim(rest, "/"), "/")
	if segs[0] != "" && !siteReserved[segs[0]] {
		r.SetPathValue("locale", segs[0])
		segs = segs[1:]
		if len(segs) == 0 {
			segs = []string{""}
		}
	}
	switch {
	case len(segs) == 1 && segs[0] == "":
		h.siteLanding(w, r)
	case len(segs) == 1 && segs[0] == "links":
		h.siteLinks(w, r)
	case len(segs) == 1 && segs[0] == "partners":
		h.sitePartners(w, r)
	case len(segs) == 1 && segs[0] == "sitemap.xml":
		if r.PathValue("locale") != "" {
			httpx.WriteError(w, httpx.ErrNotFound)
			return
		}
		h.siteSitemap(w, r)
	case len(segs) == 1 && segs[0] == "robots.txt":
		if r.PathValue("locale") != "" {
			httpx.WriteError(w, httpx.ErrNotFound)
			return
		}
		h.siteRobots(w, r)
	case len(segs) == 1 && segs[0] == "og.png":
		h.siteOGLanding(w, r)
	case len(segs) == 2 && segs[0] == "book":
		r.SetPathValue("serviceID", segs[1])
		h.siteService(w, r)
	case len(segs) == 3 && segs[0] == "book" && segs[2] == "og.png":
		r.SetPathValue("serviceID", segs[1])
		h.siteOGService(w, r)
	case len(segs) == 2 && segs[0] == "areas":
		r.SetPathValue("areaID", segs[1])
		h.siteArea(w, r)
	case len(segs) == 2 && segs[0] == "media":
		r.SetPathValue("mediaID", segs[1])
		h.siteMedia(w, r)
	// Phase 35: acquisition pages
	case len(segs) == 2 && segs[0] == "airport":
		r.SetPathValue("iata", segs[1])
		h.siteAirport(w, r)
	case len(segs) == 2 && segs[0] == "routes":
		r.SetPathValue("pair", segs[1])
		h.siteRoute(w, r)
	case len(segs) == 1 && segs[0] == "gift":
		h.siteGift(w, r)
	default:
		httpx.WriteError(w, httpx.ErrNotFound)
	}
}

// ---------------------------------------------------------- custom domains (Phase 28)

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
	return strings.TrimPrefix(h, "www.")
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

// localeBase appends the locale prefix for non-default locales
// (Phase 29): the default locale keeps canonical unprefixed URLs.
func localeBase(base string, t *model.Tenant, locale string) string {
	if locale == defaultLocale(t) {
		return base
	}
	return base + "/" + locale
}

// ---------------------------------------------------------- page model + template

type siteServiceView struct {
	Name       string
	Desc       string
	PriceLabel string
	URL        string
	Initial    string // first letter, rendered as the card monogram
	ImageURL   string // Phase 31: card image; empty = monogram tile
}

type siteStat struct {
	Value string
	Label string
}

type siteReviewView struct {
	Stars string // e.g. "★★★★★"
	Text  string
}

type siteFeature struct {
	Icon  template.HTML // static inline SVG (trusted constants)
	Title string
	Body  string
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

type hrefAlt struct {
	Lang string
	URL  string
}

type sitePage struct {
	Title        string
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
	Sections     []string
	FAQs         []siteFAQ
	CTA          string
	CTALabel     string
	// Phase 29: chrome + engagement
	D              siteDict
	HrefLangs      []hrefAlt
	WhatsApp       string // wa.me link, empty when no number configured
	PartnersURL    string
	LinksURL       string
	HomeURL        string
	TrustedPayment bool   // claim "secure online payment" only when a rail is enabled
	ReviewCount    int    // Phase 27: consented-only aggregate
	ReviewAvg      string // e.g. "4.8"
	// ThemeCSS (Phase 30): :root overrides from the studio theme — pure
	// design tokens, never structural.
	ThemeCSS template.CSS
	// Phase 31: premium template v2 — hero imagery, stats, consented review
	// quotes, the platform-features band and the inline booking widget.
	// EmbedURL always points at the platform origin (PublicBase): the
	// hostRewrite middleware only maps the /site/ surface, so an iframe src
	// on a custom domain would miss the /embed route.
	HeroImageURL string
	EmbedURL     string
	Stats        []siteStat
	Reviews      []siteReviewView
	Features     []siteFeature
	// Phase 32: design layer — layout family (classic|editorial, editorial
	// is a CSS re-flow over the same semantic DOM), section visibility and
	// the gallery strip. Show() is the template's visibility gate.
	Design    string
	Hidden    map[string]bool
	Gallery   []string     // resolved public media URLs (dangling ids skipped)
	DesignCSS template.CSS // editorial layout re-flow, empty for classic
	// Phase 54: marketer content blocks (site_config.content_blocks).
	// HeroTag is the hero paragraph (defaults to Description — the block
	// overrides DISPLAY only; meta/OG keep the engine copy). Announcement
	// renders as a slim top bar when set; empty = no bar in the DOM.
	HeroTag      string
	Announcement string
}

// Show reports whether a landing section is visible (Phase 32).
func (p sitePage) Show(id string) bool { return !p.Hidden[id] }

// hreflangCluster builds the alternate set for a page path: every
// configured locale plus x-default → the default-locale URL.
func (h *handlers) hreflangCluster(t *model.Tenant, base, pagePath string) []hrefAlt {
	var out []hrefAlt
	for _, l := range tenantLocales(t) {
		out = append(out, hrefAlt{Lang: l, URL: localeBase(base, t, l) + pagePath})
	}
	out = append(out, hrefAlt{Lang: "x-default", URL: localeBase(base, t, defaultLocale(t)) + pagePath})
	return out
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

// requestLocale resolves the locale for this request: the {locale} path
// segment when present (validated against tenant config), else the
// tenant default. ok=false → handler must 404 (unconfigured locale).
func requestLocale(r *http.Request, t *model.Tenant) (string, bool) {
	if l := r.PathValue("locale"); l != "" {
		if !validLocale(t, l) {
			return "", false
		}
		return l, true
	}
	return defaultLocale(t), true
}

// basePage fills the cross-page fields from tenant config + locale:
// lang, verification meta, OG card URL, chrome dictionary, WhatsApp chat
// link, and the page-family URLs used by the template chrome.
func (h *handlers) basePage(r *http.Request, t *model.Tenant, locale, ogPath string) sitePage {
	base := localeBase(h.siteBase(r, t.Slug), t, locale)
	p := sitePage{
		TenantName:   t.Name,
		Lang:         locale,
		Verification: t.SiteConfig.GoogleVerification,
		OGImage:      h.siteBase(r, t.Slug) + ogPath,
		D:            dictFor(locale),
		HomeURL:      base + "/",
		PartnersURL:  base + "/partners",
		LinksURL:     base + "/links",
	}
	if n, _ := t.Brand["whatsapp_number"].(string); n != "" {
		p.WhatsApp = "https://wa.me/" + strings.TrimPrefix(strings.TrimSpace(n), "+")
	}
	// Phase 32 honesty fix: a payment claim requires a LIVE rail (platform
	// PSP credentials present AND the tenant hasn't disabled it) — flag
	// state alone used to render "secure online payment" on instances with
	// no keys at all, where every link would have been a sandbox.
	p.TrustedPayment = len(h.svc.Payments.LiveRails(t.PaymentConfig)) > 0
	if !p.TrustedPayment {
		// Honesty rule (same as the trust chips): no payment-link claims
		// when no rail is enabled.
		p.D.Step2Body = p.D.Step2BodyNoPay
	}
	p.ThemeCSS = themeCSS(t.SiteConfig.Theme)
	// Phase 32: layout family + section visibility. Editorial is a CSS
	// re-flow of the same semantic DOM — one structure, two designs, no
	// template drift.
	p.Design = t.SiteConfig.Design
	if p.Design == "" {
		p.Design = "classic"
	}
	if p.Design == "editorial" {
		p.DesignCSS = editorialCSS
	}
	if len(t.SiteConfig.HiddenSections) > 0 {
		p.Hidden = map[string]bool{}
		for _, s := range t.SiteConfig.HiddenSections {
			p.Hidden[s] = true
		}
	}
	// Phase 31: the embed widget always lives on the platform origin — the
	// hostRewrite middleware only maps the /site/ surface, so a custom
	// domain iframe src would miss /embed.
	p.EmbedURL = h.svc.PublicBase() + "/embed/" + t.Slug
	p.Features = featureCards(p.D, p.WhatsApp != "", p.TrustedPayment)
	return p
}

// mediaURL builds the public URL for a media asset on the raw (locale-less)
// site base — media is locale-independent content.
func mediaURL(rawBase, id string) string {
	if id == "" {
		return ""
	}
	return rawBase + "/media/" + id
}

// liveMediaURL (Phase 33) resolves a tenant_media id to its public URL
// only when the asset actually exists. DELETE /v1/media scrubs all
// references, so this is defense-in-depth for legacy rows — a dangling id
// renders nothing rather than a broken img on the public site.
func (h *handlers) liveMediaURL(ctx context.Context, tenantID, rawBase, id string) string {
	if id == "" {
		return ""
	}
	if _, err := h.svc.Store.Media().Get(ctx, tenantID, id); err != nil {
		return ""
	}
	return mediaURL(rawBase, id)
}

// featureCards assembles the platform-features band (Phase 31). The
// WhatsApp card describes the concierge-agent channel and is always shown;
// the payments card is shown only when a payment rail is enabled (the
// same honesty rule as the trust chips).
func featureCards(d siteDict, whatsapp, trustedPayment bool) []siteFeature {
	out := []siteFeature{
		{Icon: iconCalendar, Title: d.WidgetTitle, Body: d.WidgetBody},
		{Icon: iconChat, Title: d.ConciergeTitle, Body: d.ConciergeBody},
		{Icon: iconPin, Title: d.TrackingTitle, Body: d.TrackingBody},
	}
	if trustedPayment {
		out = append(out, siteFeature{Icon: iconCard, Title: d.PaymentsTitle, Body: d.PaymentsBody})
	}
	out = append(out, siteFeature{Icon: iconWheel, Title: d.FleetTitle, Body: d.FleetBody})
	return out
}

// Static inline SVG icons for the feature band — trusted constants, safe
// as template.HTML (no tenant data is ever interpolated).
const (
	iconCalendar = template.HTML(`<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="17" rx="2"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/><path d="m9 15 2 2 4-4"/></svg>`)
	iconChat     = template.HTML(`<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/></svg>`)
	iconPin      = template.HTML(`<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 10c0 6-8 12-8 12S4 16 4 10a8 8 0 0 1 16 0z"/><circle cx="12" cy="10" r="3"/></svg>`)
	iconCard     = template.HTML(`<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="5" width="20" height="14" rx="2"/><line x1="2" y1="10" x2="22" y2="10"/></svg>`)
	iconWheel    = template.HTML(`<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="2.5"/><line x1="12" y1="3" x2="12" y2="9.5"/><line x1="4.2" y1="16.5" x2="9.8" y2="13.5"/><line x1="19.8" y1="16.5" x2="14.2" y2="13.5"/></svg>`)
)

// stars renders a 1..5 rating as a star string for the reviews grid.
func stars(n int) string {
	if n < 1 {
		n = 1
	}
	if n > 5 {
		n = 5
	}
	out := make([]byte, 0, n*3)
	for i := 0; i < n; i++ {
		out = append(out, "★"...)
	}
	return string(out)
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

// serviceCards builds the card grid views with localized names, per-locale
// descriptions (falling back to the localized vertical label), card images
// (Phase 31 — mediaBase is the raw site base, media is locale-independent)
// and monogram initials. live resolves a media id to its URL only when the
// asset exists (Phase 33 — dangling ids render the monogram, not a broken
// img).
func serviceCards(t *model.Tenant, services []model.CatalogService, base, mediaBase, locale string, live func(string) string) []siteServiceView {
	var out []siteServiceView
	for _, s := range services {
		name := localizedServiceName(s, locale)
		desc := localizedServiceDesc(s, locale)
		if desc == "" {
			desc = verticalLabel(s.Vertical, locale)
		}
		initial := "◆"
		if r := []rune(strings.ToUpper(name)); len(r) > 0 {
			initial = string(r[0])
		}
		out = append(out, siteServiceView{
			Name: name, Desc: desc, PriceLabel: priceLabel(s), Initial: initial,
			URL:      fmt.Sprintf("%s/book/%s", base, s.ID),
			ImageURL: live(s.ImageID),
		})
	}
	return out
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
	return fmt.Sprintf("%s %d-%d", services[0].Currency, lo/100, hi/100)
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

// ---------------------------------------------------------- structured data helpers

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

// contentBlock resolves a Phase 54 marketer copy override for the page
// locale, falling back to the tenant's default locale, then to "" (engine
// copy renders). The block vocabulary and plain-text rules are enforced
// on write, so render-time is a pure lookup.
func contentBlock(t *model.Tenant, locale, key string) string {
	blocks := t.SiteConfig.ContentBlocks
	if len(blocks) == 0 {
		return ""
	}
	perLocale := blocks[key]
	if perLocale == nil {
		return ""
	}
	if v := perLocale[locale]; v != "" {
		return v
	}
	def := t.SiteConfig.Locale
	if len(t.SiteConfig.Locales) > 0 {
		def = t.SiteConfig.Locales[0]
	}
	if def == "" {
		def = "en"
	}
	return perLocale[def]
}

func (h *handlers) siteLanding(w http.ResponseWriter, r *http.Request) {
	t, services, areas, err := h.siteData(r)
	if err != nil {
		httpx.WriteError(w, err)
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
	p.Description = localizedBrand(t, locale)
	p.Canonical = base + "/"
	p.HrefLangs = h.hreflangCluster(t, rawBase, "/")
	p.Heading = t.Name
	p.SubHead = "VIP concierge"
	p.CTA, p.CTALabel = h.captureURL(t.ID, "landing"), p.D.BookYourConcierge
	// Phase 54: marketer content blocks — render-time copy overrides.
	p.HeroTag = p.Description
	if v := contentBlock(t, locale, "hero_tagline"); v != "" {
		p.HeroTag = v
	}
	if v := contentBlock(t, locale, "cta_label"); v != "" {
		p.CTALabel = v
	}
	if v := contentBlock(t, locale, "footer_note"); v != "" {
		p.D.FooterTagline = v
	}
	p.Announcement = contentBlock(t, locale, "announcement")
	if sum, err := h.svc.ReviewSummary(r.Context(), t.ID); err == nil && sum.Count > 0 {
		p.ReviewCount = sum.Count
		p.ReviewAvg = fmt.Sprintf("%.1f", sum.Average)
	}
	// Phase 31: hero image (tenant media), stats strip and consented review
	// quotes. A configured hero doubles as the OG card — a real photo beats
	// the bitmap renderer for link shares. Phase 33: resolved through
	// liveMediaURL — a dangling id (legacy rows) falls back to the gradient.
	if u := h.liveMediaURL(r.Context(), t.ID, rawBase, t.SiteConfig.HeroImageID); u != "" {
		p.HeroImageURL = u
		p.OGImage = u
	}
	// Phase 32: gallery strip — resolve ids through the media store so a
	// deleted asset silently drops out rather than rendering a broken img.
	for _, id := range t.SiteConfig.GalleryIDs {
		if u := h.liveMediaURL(r.Context(), t.ID, rawBase, id); u != "" {
			p.Gallery = append(p.Gallery, u)
		}
	}
	if len(services) > 0 {
		p.Stats = append(p.Stats, siteStat{Value: fmt.Sprintf("%d", len(services)), Label: p.D.ServicesWord})
	}
	if len(areas) > 0 {
		p.Stats = append(p.Stats, siteStat{Value: fmt.Sprintf("%d", len(areas)), Label: p.D.AreasWord})
	}
	if p.ReviewCount > 0 {
		p.Stats = append(p.Stats, siteStat{Value: "★ " + p.ReviewAvg, Label: p.D.ClientReviews})
	}
	// Phase 35 (A10): the social-proof band — real ledger counts only,
	// rendered only when non-zero. "Clients served" counts clients holding
	// at least one booking; "trips completed" counts COMPLETED_TRIP
	// assignments. No rounding up, no projections.
	if proof, err := h.svc.SocialProof(r.Context(), t.ID); err == nil {
		if proof.ClientsServed > 0 {
			p.Stats = append(p.Stats, siteStat{Value: fmt.Sprintf("%d", proof.ClientsServed), Label: p.D.ClientsServed})
		}
		if proof.TripsCompleted > 0 {
			p.Stats = append(p.Stats, siteStat{Value: fmt.Sprintf("%d", proof.TripsCompleted), Label: p.D.TripsCompleted})
		}
	}
	if reviews, err := h.svc.Store.Reviews().Consented(r.Context(), t.ID); err == nil {
		for _, rv := range reviews {
			if rv.Text == "" || len(p.Reviews) >= 3 {
				continue
			}
			p.Reviews = append(p.Reviews, siteReviewView{Stars: stars(rv.Rating), Text: rv.Text})
		}
	}
	p.Services = serviceCards(t, services, base, rawBase, locale,
		func(id string) string { return h.liveMediaURL(r.Context(), t.ID, rawBase, id) })
	for _, a := range areas {
		p.Areas = append(p.Areas, siteAreaView{
			Name: localizedAreaName(a, locale),
			URL:  fmt.Sprintf("%s/areas/%s", base, a.ID),
		})
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
	locale, ok := requestLocale(r, t)
	if !ok {
		httpx.WriteError(w, httpx.ErrNotFound)
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
	name := localizedServiceName(*svc, locale)
	rawBase := h.siteBase(r, t.Slug)
	base := localeBase(rawBase, t, locale)
	pagePath := fmt.Sprintf("/book/%s", svc.ID)

	desc := localizedServiceDesc(*svc, locale)
	if desc == "" {
		desc = fmt.Sprintf("%s — %s, %s.", name, t.Name, verticalLabel(svc.Vertical, locale))
	}
	title := fmt.Sprintf("%s | %s", name, t.Name)
	if pl := priceLabel(*svc); pl != "" {
		title = fmt.Sprintf("%s — %s %s | %s", name, dictFor(locale).FromPrefix, pl, t.Name)
	}

	p := h.basePage(r, t, locale, fmt.Sprintf("/book/%s/og.png", svc.ID))
	p.Title = title
	p.Description = desc
	p.Canonical = base + pagePath
	p.HrefLangs = h.hreflangCluster(t, rawBase, pagePath)
	p.Heading, p.SubHead = name, name
	p.CTA, p.CTALabel = h.captureURL(t.ID, "svc-"+svc.ID), p.D.BookPrefix+name
	if svc.ImageID != "" {
		// Phase 31: the service card image doubles as the page hero.
		// Phase 33: resolved through liveMediaURL — a dangling id renders
		// the theme hero fallback, not a broken img.
		p.HeroImageURL = h.liveMediaURL(r.Context(), t.ID, rawBase, svc.ImageID)
	}
	p.Services = serviceCards(t, []model.CatalogService{*svc}, base, rawBase, locale,
		func(id string) string { return h.liveMediaURL(r.Context(), t.ID, rawBase, id) })
	p.Sections = []string{guideServiceIntro(t, svc, locale, priceLabel(*svc))}
	if cov := guideCoverage(name, t.Name, areas, locale); cov != "" {
		p.Sections = append(p.Sections, cov)
	}
	p.FAQs = faqsForServiceL(t, svc, name, areas, locale, h.svc.Payments.LiveRails(t.PaymentConfig))
	if sum, err := h.svc.ReviewSummary(r.Context(), t.ID); err == nil && sum.Count > 0 {
		p.ReviewCount = sum.Count
		p.ReviewAvg = fmt.Sprintf("%.1f", sum.Average)
	}

	ld := map[string]any{
		"@type": "Service",
		"name":  name, "provider": map[string]any{"@type": "LocalBusiness", "name": t.Name},
		"description": desc,
	}
	if svc.BaseMinor > 0 {
		ld["offers"] = map[string]any{"@type": "Offer", "price": fmt.Sprintf("%.2f", float64(svc.BaseMinor)/100),
			"priceCurrency": svc.Currency}
	}
	if len(areas) > 0 {
		served := make([]any, 0, len(areas))
		for _, a := range areas {
			served = append(served, map[string]any{"@type": "Place", "name": localizedAreaName(a, locale)})
		}
		ld["areaServed"] = served
	}
	p.JSONLD = marshalLDGraph([]map[string]any{
		ld,
		faqLD(p.FAQs),
		breadcrumbLD(base, [2]string{"Home", ""}, [2]string{name, p.Canonical}),
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
	locale, ok := requestLocale(r, t)
	if !ok {
		httpx.WriteError(w, httpx.ErrNotFound)
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
	areaName := localizedAreaName(*area, locale)
	rawBase := h.siteBase(r, t.Slug)
	base := localeBase(rawBase, t, locale)
	pagePath := fmt.Sprintf("/areas/%s", area.ID)

	p := h.basePage(r, t, locale, "/og.png")
	if proseLocale(locale) == "pt-PT" {
		p.Title = fmt.Sprintf("%s em %s — %d %s", t.Name, areaName,
			len(services), map[bool]string{true: "serviço de concierge", false: "serviços de concierge"}[len(services) == 1])
		p.Description = fmt.Sprintf("%s em %s — %s", t.Name, areaName, localizedBrand(t, locale))
	} else {
		p.Title = fmt.Sprintf("%s in %s — %d %s | Book online", t.Name, areaName,
			len(services), pluralizeCount(len(services), "concierge service", "concierge services"))
		p.Description = fmt.Sprintf("%s in %s — %s", t.Name, areaName, localizedBrand(t, locale))
	}
	p.Canonical = base + pagePath
	p.HrefLangs = h.hreflangCluster(t, rawBase, pagePath)
	if proseLocale(locale) == "pt-PT" {
		p.Heading = fmt.Sprintf("%s em %s", t.Name, areaName)
		p.SubHead = "concierge em " + areaName
	} else {
		p.Heading = fmt.Sprintf("%s in %s", t.Name, areaName)
		p.SubHead = "concierge in " + areaName
	}
	p.CTA = h.captureURL(t.ID, "area-"+area.ID)
	if proseLocale(locale) == "pt-PT" {
		p.CTALabel = "Reservar em " + areaName
	} else {
		p.CTALabel = "Book in " + areaName
	}

	var parts []string
	for _, s := range services {
		if pl := priceLabel(s); pl != "" {
			parts = append(parts, fmt.Sprintf("%s (%s %s)", localizedServiceName(s, locale), p.D.FromPrefix, pl))
		} else {
			parts = append(parts, localizedServiceName(s, locale))
		}
	}
	if len(services) > 0 {
		p.Sections = []string{
			guideAreaIntro(t, areaName, parts, len(services), locale),
			guideAreaTrust(areaName, locale),
		}
	}
	p.Services = serviceCards(t, services, base, rawBase, locale,
		func(id string) string { return h.liveMediaURL(r.Context(), t.ID, rawBase, id) })
	p.JSONLD = marshalLDGraph([]map[string]any{
		{
			"@type":      "Service",
			"name":       fmt.Sprintf("%s — %s", t.Name, areaName),
			"provider":   map[string]any{"@type": "LocalBusiness", "name": t.Name},
			"areaServed": map[string]any{"@type": "Place", "name": areaName},
		},
		breadcrumbLD(base, [2]string{"Home", ""}, [2]string{areaName, p.Canonical}),
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
	locale, ok := requestLocale(r, t)
	if !ok {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	partners, err := h.svc.Store.Partners().List(r.Context(), t.ID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	rawBase := h.siteBase(r, t.Slug)
	base := localeBase(rawBase, t, locale)
	p := h.basePage(r, t, locale, "/og.png")
	if proseLocale(locale) == "pt-PT" {
		p.Title = fmt.Sprintf("Parceiros da %s — fornecedores certificados", t.Name)
		p.Description = fmt.Sprintf("A rede de parceiros certificados da %s — fornecedores de transporte, viagens e lifestyle sob o mesmo padrão de serviço.", t.Name)
	} else {
		p.Title = fmt.Sprintf("Partners of %s — vetted concierge providers", t.Name)
		p.Description = fmt.Sprintf("The vetted partner network behind %s — chauffeur, travel and lifestyle providers confirmed to the same service standard.", t.Name)
	}
	p.Canonical = base + "/partners"
	p.HrefLangs = h.hreflangCluster(t, rawBase, "/partners")
	p.Heading = p.D.OurPartners
	p.SubHead = "vetted partner network"
	p.CTA, p.CTALabel = h.captureURL(t.ID, "partners"), p.D.BookYourConcierge
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
		if proseLocale(locale) == "pt-PT" {
			p.Sections = []string{fmt.Sprintf("%s trabalha com %d %s certificados. Cada parceiro é avaliado por qualidade e opera sob os mesmos padrões de reserva, acompanhamento e pagamento da %s.",
				t.Name, n, map[bool]string{true: "parceiro", false: "parceiros"}[n == 1], t.Name)}
		} else {
			p.Sections = []string{fmt.Sprintf("%s works with %d vetted partner %s. Every partner is quality-scored and operates under the same booking, tracking and payment standards as %s itself.",
				t.Name, n, pluralizeCount(n, "provider", "providers"), t.Name)}
		}
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
	// asciiFold: the embedded bitmap font is ASCII-only (page HTML is
	// full UTF-8; this applies only to the card).
	renderOG(w, asciiFold(t.Name), asciiFold(sub), rating, footer)
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
	renderOG(w, asciiFold(svc.Name), asciiFold(sub), "", asciiFold(t.Name)+" — book your concierge")
}

// ---------------------------------------------------------- sitemap + robots

func (h *handlers) siteSitemap(w http.ResponseWriter, r *http.Request) {
	t, services, areas, err := h.siteData(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	rawBase := h.siteBase(r, t.Slug)
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
	// Every configured locale tree is listed (Phase 29).
	for _, l := range tenantLocales(t) {
		base := localeBase(rawBase, t, l)
		writeURL(base+"/", "1.0")
		writeURL(base+"/links", "0.5")
		writeURL(base+"/partners", "0.6")
		for _, s := range services {
			writeURL(fmt.Sprintf("%s/book/%s", base, s.ID), "0.9")
		}
		for _, a := range areas {
			writeURL(fmt.Sprintf("%s/areas/%s", base, a.ID), "0.8")
		}
	}
	// Phase 35 (A15): acquisition pages — but only URLs that actually
	// resolve. A sitemap that lists 404s teaches crawlers to distrust the
	// whole site, so every entry here passes the same honesty gate the page
	// handler applies.
	hasPerKM := false
	for _, s := range services {
		if s.Vertical == "CHAUFFEUR_POINT_TO_POINT" && s.PerKMMinor > 0 && s.AutoQuotable {
			hasPerKM = true
			break
		}
	}
	areaCache := map[string]bool{}
	covered := func(lat, lng float64) bool {
		key := fmt.Sprintf("%.4f,%.4f", lat, lng)
		if v, ok := areaCache[key]; ok {
			return v
		}
		hits, err := h.svc.Geo.Contains(r.Context(), t.ID, lat, lng)
		ok := err == nil && len(hits) > 0
		areaCache[key] = ok
		return ok
	}
	// Airports the tenant covers (A1).
	for _, ap := range airports35 {
		if !covered(ap.Lat, ap.Lng) {
			continue
		}
		for _, l := range tenantLocales(t) {
			writeURL(fmt.Sprintf("%s/airport/%s", localeBase(rawBase, t, l), ap.Code), "0.7")
		}
	}
	// City-pair routes where BOTH endpoints are covered and the tenant can
	// price the distance (A2). Cap at the 40 strongest pairs per tenant —
	// sitemap quality beats sitemap size.
	if hasPerKM {
		var coveredCities []geoPoint
		for _, c := range cities35 {
			if covered(c.Lat, c.Lng) {
				coveredCities = append(coveredCities, c)
			}
		}
		n := 0
		for i := 0; i < len(coveredCities) && n < 40; i++ {
			for j := 0; j < len(coveredCities) && n < 40; j++ {
				if i == j {
					continue
				}
				writeURL(fmt.Sprintf("%s/routes/%s-to-%s", localeBase(rawBase, t, tenantLocales(t)[0]),
					coveredCities[i].Code, coveredCities[j].Code), "0.6")
				n++
			}
		}
	}
	// Gift storefront, only when vouchers exist (A14).
	if promos, err := h.svc.Store.Promos().List(r.Context(), t.ID); err == nil {
		now := h.svc.Now()
		for _, pr := range promos {
			if pr.Gift && pr.Active && (pr.ExpiresAt.IsZero() || now.Before(pr.ExpiresAt)) {
				writeURL(localeBase(rawBase, t, tenantLocales(t)[0])+"/gift", "0.5")
				break
			}
		}
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
	locale, ok := requestLocale(r, t)
	if !ok {
		httpx.WriteError(w, httpx.ErrNotFound)
		return
	}
	rawBase := h.siteBase(r, t.Slug)
	base := localeBase(rawBase, t, locale)
	p := h.basePage(r, t, locale, "/og.png")
	p.Description = localizedBrand(t, locale)
	p.Canonical = base + "/links"
	p.HrefLangs = h.hreflangCluster(t, rawBase, "/links")
	p.Heading, p.SubHead = t.Name, "book your concierge"
	p.CTA, p.CTALabel = h.captureChannelURL(t.ID, "social", "bio", "links"), p.D.BookNow
	p.Services = serviceCards(t, services, base, rawBase, locale,
		func(id string) string { return h.liveMediaURL(r.Context(), t.ID, rawBase, id) })
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
// Phase 35 (A13): ?theme=light inverts the palette for dark host pages and
// &compact=1 renders a single slim CTA bar — both are pure presentation;
// the booking flow and attribution are identical.
func (h *handlers) embedWidget(w http.ResponseWriter, r *http.Request) {
	t, services, _, err := h.siteData(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	cta := h.captureChannelURLFor(r, t, "widget", "embed", "widget-"+t.Slug)
	light := r.URL.Query().Get("theme") == "light"
	compact := r.URL.Query().Get("compact") == "1"
	fg, bg, hover := "#fff", "#0b5fff", "#0946c0"
	body := "#fff"
	if light {
		// light theme = dark widget chrome for light-on-dark embeds
		fg, bg, hover, body = "#0b5fff", "#ffffff", "#e8eeff", "transparent"
	}
	if compact {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>body{font-family:system-ui,sans-serif;margin:0;background:%s}
a{display:block;background:%s;color:%s;text-decoration:none;padding:10px 14px;border-radius:8px;text-align:center;font-weight:600;font-size:14px}
a:hover{background:%s}</style></head><body>
<a target="_top" href="%s">Book %s</a></body></html>`,
			body, bg, fg, hover, template.HTMLEscapeString(cta), template.HTMLEscapeString(t.Name))
		return
	}
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
<style>body{font-family:system-ui,sans-serif;margin:0;padding:12px;background:%s}
a{display:block;background:%s;color:%s;text-decoration:none;padding:12px;border-radius:8px;margin:8px 0;text-align:center;font-weight:600}
a:hover{background:%s}</style></head><body>%s</body></html>`,
		body, bg, fg, hover, items.String())
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
// cache headers (Phase 28): repeat crawls get 304s, and pages are publicly
// cacheable for 5 minutes — content changes are reflected fast while
// crawlers and CDNs stay off the hot path.
func renderSite(w http.ResponseWriter, r *http.Request, p sitePage) {
	if p.Title == "" {
		p.Title = p.TenantName + " — " + p.SubHead
	}
	if p.Lang == "" {
		p.Lang = "en"
	}
	// Phase 54: the hero paragraph binds HeroTag (landing may override it
	// with a content block); every other page falls back to Description.
	if p.HeroTag == "" {
		p.HeroTag = p.Description
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
