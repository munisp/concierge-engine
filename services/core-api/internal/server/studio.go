package server

// Phase 30: Site Studio API + tenant insights.
//
// Studio: presets/theme (design tokens on site_config.theme), business-case
// content generation (concierge-agent with deterministic template fallback,
// provenance-labeled, draft-only until applied), static export (zip for
// external hosting — booking CTAs stay absolute to the platform so site and
// platform can live on different hosts), and CMS integration snippets
// (WordPress shortcode plugin, iframe embed, custom-domain steps).
//
// Insights: GET /v1/analytics/overview — the operational KPI read
// (bookings, revenue, service mix, channels, fleet, drivers, reviews).

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (h *handlers) registerStudio(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/studio/presets", h.studioPresets)
	mux.HandleFunc("PUT /v1/studio/theme", h.studioSetTheme)
	mux.HandleFunc("POST /v1/studio/generate", h.studioGenerate)
	mux.HandleFunc("POST /v1/studio/apply", h.studioApply)
	mux.HandleFunc("GET /v1/studio/export", h.studioExport)
	mux.HandleFunc("GET /v1/studio/integrations", h.studioIntegrations)
	mux.HandleFunc("GET /v1/analytics/overview", h.analyticsOverview)
}

func (h *handlers) studioPresets(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"presets": domain.StudioPresets()})
}

func (h *handlers) studioSetTheme(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Theme *model.SiteTheme `json:"theme"`
	}
	if err := httpx.Decode(r, &body); err != nil || body.Theme == nil {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	theme, err := h.svc.Studio.SetTheme(r.Context(), tenantOf(r), body.Theme)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"theme": theme, "resolved": domain.ResolveTheme(theme),
	})
}

func (h *handlers) studioGenerate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BusinessCase string `json:"business_case"`
		Locale       string `json:"locale"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if strings.TrimSpace(body.BusinessCase) == "" {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	draft, err := h.svc.Studio.GenerateContent(r.Context(), tenantOf(r), body.BusinessCase, body.Locale)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, draft)
}

func (h *handlers) studioApply(w http.ResponseWriter, r *http.Request) {
	var draft domain.StudioDraft
	if err := httpx.Decode(r, &draft); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.Studio.ApplyDraft(r.Context(), tenantOf(r), &draft); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"applied": true, "locale": draft.Locale})
}

// studioExport renders the tenant's current public site into a static zip:
// index.html + one page per service/area + links + partners, flat layout,
// internal links rewritten relative, booking CTAs left absolute to the
// platform. Deployable on any static host (Netlify/S3/shared hosting) —
// the split-host shape where the site lives away from the platform.
func (h *handlers) studioExport(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.Store.Tenants().Get(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	catalog, err := h.svc.Store.Catalog().List(r.Context(), t.ID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	services := catalog[:0]
	for _, s := range catalog {
		if s.Active {
			services = append(services, s)
		}
	}
	areas, err := h.svc.Store.Geo().Areas(r.Context(), t.ID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	render := func(path string, setters map[string]string) []byte {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.SetPathValue("slug", t.Slug)
		for k, v := range setters {
			req.SetPathValue(k, v)
		}
		h.siteDispatch(rec, req)
		if rec.Code != http.StatusOK {
			return nil
		}
		return rec.Body.Bytes()
	}
	base := h.svc.PublicBase() + "/site/" + t.Slug
	relativize := func(html []byte) []byte {
		s := string(html)
		// flat layout: /book/<id> → book-<id>.html, /areas/<id> → area-<id>.html
		for _, sv := range services {
			s = strings.ReplaceAll(s, base+"/book/"+sv.ID, "book-"+sv.ID+".html")
		}
		for _, a := range areas {
			s = strings.ReplaceAll(s, base+"/areas/"+a.ID, "area-"+a.ID+".html")
		}
		s = strings.ReplaceAll(s, base+"/links", "links.html")
		s = strings.ReplaceAll(s, base+"/partners", "partners.html")
		s = strings.ReplaceAll(s, `href="`+base+`/"`, `href="index.html"`)
		return []byte(s)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, body []byte) {
		if body == nil {
			return
		}
		f, _ := zw.Create(name)
		_, _ = f.Write(body)
	}
	add("index.html", relativize(render("/site/"+t.Slug+"/", nil)))
	for _, sv := range services {
		add("book-"+sv.ID+".html", relativize(render("/site/"+t.Slug+"/book/"+sv.ID,
			map[string]string{"serviceID": sv.ID})))
	}
	for _, a := range areas {
		add("area-"+a.ID+".html", relativize(render("/site/"+t.Slug+"/areas/"+a.ID,
			map[string]string{"areaID": a.ID})))
	}
	add("links.html", relativize(render("/site/"+t.Slug+"/links", nil)))
	add("partners.html", relativize(render("/site/"+t.Slug+"/partners", nil)))
	add("README.txt", []byte(fmt.Sprintf(`%s — static site export (concierge-engine Site Studio)

Deploy: upload these files to any static host (Netlify, S3+CloudFront,
GitHub Pages, shared hosting). No server-side runtime needed.

What works offline-host: all content, styling, localized pages.
What stays live-linked to the platform (%s):
  - booking CTAs (capture flow, payments, WhatsApp concierge)
  - canonical/hreflang tags point at the live site (recommended: keep the
    platform site canonical and treat this export as a mirror/failover)

This is a snapshot from %s — re-export after catalog or theme changes.
`, t.Name, h.svc.PublicBase(), h.svc.Now().UTC().Format("2006-01-02 15:04 UTC"))))
	if err := zw.Close(); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-site-export.zip"`, t.Slug))
	_, _ = w.Write(buf.Bytes())
}

// studioIntegrations: every deployment shape, as copy-paste artifacts.
func (h *handlers) studioIntegrations(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.Store.Tenants().Get(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	pub := h.svc.PublicBase()
	ifr := fmt.Sprintf(`<iframe src="%s/embed/%s" style="width:100%%;max-width:480px;border:0;height:420px" title="Book with %s" loading="lazy"></iframe>`,
		pub, t.Slug, t.Name)
	wpPlugin := fmt.Sprintf(`<?php
/**
 * Plugin Name: %s Booking Widget
 * Description: Adds the [concierge_book] shortcode — embeds the concierge-engine booking widget.
 * Version: 1.0.0
 */
add_shortcode('concierge_book', function () {
    return %s;
});
`, t.Name, varExport(ifr))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"shapes": map[string]any{
			"hosted": map[string]string{
				"description": "Same-host: the platform serves the full site at /site/" + t.Slug + "/ (or your custom domain).",
				"url":         pub + "/site/" + t.Slug + "/",
			},
			"custom_domain": map[string]string{
				"description": "Split-DNS: CNAME your domain to the platform host, list it under Settings → Public site & SEO. All pages, sitemap and OG cards serve on your domain.",
			},
			"static_export": map[string]string{
				"description": "Split-host: download the static export (GET /v1/studio/export) and host it anywhere; booking CTAs link back to the platform.",
				"endpoint":    "/v1/studio/export",
			},
			"embed": map[string]string{
				"description": "CMS-embed: paste this iframe into WordPress (Custom HTML block), Webflow, Shopify, Wix or any CMS. Bookings arrive stamped channel=widget.",
				"iframe":      ifr,
			},
			"wordpress": map[string]string{
				"description": "WordPress plugin: save as wp-content/plugins/concierge-booking/concierge-booking.php, activate, then use the [concierge_book] shortcode in any page or post.",
				"plugin_php":  wpPlugin,
				"plugin_file": "concierge-booking.php",
				"shortcode":   "[concierge_book]",
			},
		},
	})
}

// varExport renders a Go string as a single-quoted PHP string literal.
func varExport(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `'`, `\'`) + "'"
}

func (h *handlers) analyticsOverview(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	o, err := h.svc.Insights.Overview(r.Context(), tenantOf(r), days)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, o)
}
