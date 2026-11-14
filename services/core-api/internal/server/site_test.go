package server_test

// Phase 28 tests: SEO optimization wave — custom domains, OG images,
// expanded structured data, per-page titles, FAQs, guide content, cache
// headers, sitemap index/lastmod, partner directory, verification meta.

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/model"
)

// getHost issues a raw GET with an explicit Host header (custom-domain
// simulation) and returns status, headers and body.
func getHost(t *testing.T, base, path, host, extraHeader, extraValue string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest("GET", base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	if extraHeader != "" {
		req.Header.Set(extraHeader, extraValue)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

func TestCustomDomainSite(t *testing.T) {
	env := newEnv(t) // slug "taylor"
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{
			"custom_domains":      []string{"https://www.Concierge.Taylor.test/"}, // normalized on write
			"google_verification": "goog-verify-123",
			"locale":              "en-GB",
			"social_links":        []string{"https://instagram.com/taylortravel"},
		},
	}, http.StatusOK, nil)

	// Landing served on the tenant's own domain; canonical + og:image +
	// sitemap all build on that origin so authority compounds there.
	status, _, body := getHost(t, env.server.URL, "/", "concierge.taylor.test", "", "")
	if status != http.StatusOK {
		t.Fatalf("custom-domain landing: %d", status)
	}
	for _, want := range []string{
		`rel="canonical" href="https://concierge.taylor.test/"`,
		`og:image" content="https://concierge.taylor.test/og.png"`,
		`google-site-verification" content="goog-verify-123"`,
		`<html lang="en-GB">`,
		`hreflang="x-default"`,
		"https://instagram.com/taylortravel", // sameAs in JSON-LD
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("custom-domain landing missing %q", want)
		}
	}

	// robots + sitemap follow the custom origin too.
	_, _, robots := getHost(t, env.server.URL, "/robots.txt", "concierge.taylor.test", "", "")
	if !strings.Contains(robots, "Sitemap: https://concierge.taylor.test/sitemap.xml") {
		t.Fatalf("custom-domain robots wrong: %s", robots)
	}

	// Unknown host falls through to platform routing (no rewrite).
	status, _, _ = getHost(t, env.server.URL, "/", "not-a-tenant.example", "", "")
	if status == http.StatusOK {
		t.Fatal("unknown host must not resolve to a tenant site")
	}

	// Header-spoof guard: X-CE-Site-Host set by the caller is stripped at
	// the middleware boundary — canonical must stay on the platform origin.
	_, _, body = getHost(t, env.server.URL, "/site/taylor/", "", "X-CE-Site-Host", "https://evil.example")
	if strings.Contains(body, "evil.example") {
		t.Fatal("siteHostHeader spoof leaked into canonical")
	}
	if !strings.Contains(body, "/site/taylor/") {
		t.Fatal("path-based landing broken after spoof attempt")
	}
}

func TestOGImages(t *testing.T) {
	env := newEnv(t)
	var svc model.CatalogService
	env.do("POST", "/v1/catalog", env.tenantID, model.CatalogService{
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Airport Transfer",
		BaseMinor: 12000, Currency: "EUR", Active: true,
	}, http.StatusCreated, &svc)

	for _, path := range []string{"/site/taylor/og.png", "/site/taylor/book/" + svc.ID + "/og.png"} {
		status, hdr, body := getHost(t, env.server.URL, path, "", "", "")
		if status != http.StatusOK {
			t.Fatalf("%s: %d", path, status)
		}
		if ct := hdr.Get("Content-Type"); ct != "image/png" {
			t.Fatalf("%s content-type %s", path, ct)
		}
		if !strings.HasPrefix(body, "\x89PNG") {
			t.Fatalf("%s not a PNG", path)
		}
		if hdr.Get("Cache-Control") == "" {
			t.Fatalf("%s missing cache header", path)
		}
	}
	// Unknown service OG is a clean 404, never a rendered lie.
	status, _, _ := getHost(t, env.server.URL, "/site/taylor/book/nope/og.png", "", "", "")
	if status != http.StatusNotFound {
		t.Fatalf("unknown service og: %d", status)
	}
}

func TestServicePageSEO28(t *testing.T) {
	env := newEnv(t)
	var svc model.CatalogService
	env.do("POST", "/v1/catalog", env.tenantID, model.CatalogService{
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Airport Transfer",
		BaseMinor: 12000, Currency: "EUR", PerKMMinor: 250, Active: true,
	}, http.StatusCreated, &svc)
	env.do("POST", "/v1/geo/areas", env.tenantID, map[string]any{
		"name":    "Amsterdam Centrum",
		"polygon": [][]float64{{52.36, 4.88}, {52.38, 4.88}, {52.38, 4.91}, {52.36, 4.91}},
	}, http.StatusCreated, nil)

	_, hdr, body := getHost(t, env.server.URL, "/site/taylor/book/"+svc.ID, "", "", "")
	for _, want := range []string{
		"<title>Airport Transfer — from EUR 120.00 | Taylor Travel Management</title>", // unique title
		"FAQPage",          // FAQ structured data
		"BreadcrumbList",   // breadcrumb structured data
		"Common questions", // FAQ HTML section
		"per kilometre",    // FAQ answer carries the real per-km rate
		"dedicated concierge",
		"areaServed",
		"og:image", "summary_large_image",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("service page missing %q", want)
		}
	}
	// Cache headers + ETag revalidation → 304.
	etag := hdr.Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	if cc := hdr.Get("Cache-Control"); !strings.Contains(cc, "public") {
		t.Fatalf("missing cache-control: %s", cc)
	}
	status, _, _ := getHost(t, env.server.URL, "/site/taylor/book/"+svc.ID, "", "If-None-Match", etag)
	if status != http.StatusNotModified {
		t.Fatalf("etag revalidation: %d", status)
	}
}

func TestLandingSEO28(t *testing.T) {
	env := newEnv(t)
	env.do("POST", "/v1/catalog", env.tenantID, model.CatalogService{
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Airport Transfer",
		BaseMinor: 12000, Currency: "EUR", Active: true,
	}, http.StatusCreated, nil)
	env.do("POST", "/v1/geo/areas", env.tenantID, map[string]any{
		"name":    "Amsterdam Centrum",
		"polygon": [][]float64{{52.36, 4.88}, {52.38, 4.88}, {52.38, 4.91}, {52.36, 4.91}},
	}, http.StatusCreated, nil)
	env.do("PUT", "/v1/tenant", env.tenantID, map[string]any{
		"site_config": map[string]any{"social_links": []string{"https://x.com/taylortravel"}},
	}, http.StatusOK, nil)

	_, _, body := getHost(t, env.server.URL, "/site/taylor/", "", "", "")
	for _, want := range []string{
		`"priceRange":"EUR 120-120"`, // catalog-derived, never invented
		"GeoCoordinates",             // centroid of real coverage polygon
		"https://x.com/taylortravel", // sameAs
		`hreflang="en"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("landing missing %q", want)
		}
	}
	// No consented reviews → no fabricated stars.
	if strings.Contains(body, "aggregateRating") {
		t.Fatal("aggregateRating present without consented reviews")
	}
}

func TestSitemapIndexAndPartners28(t *testing.T) {
	env := newEnv(t)
	env.do("POST", "/v1/catalog", env.tenantID, model.CatalogService{
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Airport Transfer",
		BaseMinor: 12000, Currency: "EUR", Active: true,
	}, http.StatusCreated, nil)

	// Tenant sitemap: lastmod + partner directory included.
	_, _, sm := getHost(t, env.server.URL, "/site/taylor/sitemap.xml", "", "", "")
	for _, want := range []string{"<lastmod>", "/partners", "/links", "/book/"} {
		if !strings.Contains(sm, want) {
			t.Fatalf("sitemap missing %q", want)
		}
	}
	// Platform index lists the tenant sitemap.
	_, _, idx := getHost(t, env.server.URL, "/sitemap.xml", "", "", "")
	if !strings.Contains(idx, "/site/taylor/sitemap.xml") || !strings.Contains(idx, "sitemapindex") {
		t.Fatalf("platform index wrong: %s", idx)
	}

	// Partner directory: only VETTED partners are public.
	var partner model.Partner
	env.do("POST", "/v1/partners", env.tenantID, model.Partner{
		Name: "Schiphol Limo", Verticals: []string{"chauffeur"}, Markets: []string{"AMS"},
	}, http.StatusCreated, &partner)
	_, _, body := getHost(t, env.server.URL, "/site/taylor/partners", "", "", "")
	if strings.Contains(body, "Schiphol Limo") {
		t.Fatal("PENDING partner must not appear in the public directory")
	}
	env.do("PUT", "/v1/partners/"+partner.ID+"/status", env.tenantID, map[string]any{"status": "VETTED"}, http.StatusOK, nil)
	_, _, body = getHost(t, env.server.URL, "/site/taylor/partners", "", "", "")
	for _, want := range []string{"Schiphol Limo", "chauffeur", "ItemList", "vetted partner"} {
		if !strings.Contains(body, want) {
			t.Fatalf("partners page missing %q", want)
		}
	}
}
