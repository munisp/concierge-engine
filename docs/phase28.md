# Phase 28 — SEO optimization wave

**Goal.** Take the Phase 26/27 acquisition surface from technically clean to
maximum real-world effect. The audit's conclusion: the foundation (SSR,
canonical, JSON-LD, sitemap, review stars) was sound — the ranking gaps
were authority, content depth and shareability. This phase closes all
three, plus measurement and performance.

## What shipped (all ten levers)

### 1. Custom domains (the biggest lever)
Path-based tenants (`/site/{slug}`) give all link authority to the
platform domain. `tenant.site_config.custom_domains` (migration 0021)
enables host-based resolution: `hostRewrite` middleware normalizes the
Host header (lowercase, port/`www.` stripped), resolves the tenant via
`Tenants().GetByDomain` (pg: `site_config->'custom_domains' ? $1` jsonb
containment), rewrites `/book/xyz` → `/site/{slug}/book/xyz` before
routing, and carries the public origin on an internal
`X-CE-Site-Host` header so canonical/sitemap/robots/OG URLs are all built
on the tenant's own domain. The header is stripped unconditionally at the
middleware boundary — it is trusted only when set after a real domain
lookup (spoof-tested). Unknown hosts fall through to platform routing.
Domains are normalized on write in `PUT /v1/tenant` (scheme/`www.`/case
stripped).

### 2. Guide content (thin-content fix)
Service and area pages now carry prose sections assembled **strictly from
real tenant data** — services with real prices, real coverage areas, real
payment rails (`serviceSections` / `areaSections`). Rules-generated, never
AI-invented copy; a page that says "Airport Transfer from EUR 120.00,
available across Amsterdam Centrum" outranks generic copy because it is
specific and true.

### 3. Structured data expansion
`marshalLDGraph` emits one `@graph` per page:
- **Landing**: LocalBusiness + `priceRange` (min–max of real catalog),
  `geo` (centroid of real coverage polygons — never a fabricated street
  address), `sameAs` (configured social profiles), `aggregateRating` +
  individual `Review` nodes (consented only, capped 3, author rendered as
  "Verified client" — no client PII on public pages).
- **Service pages**: Service + Offer + `areaServed` + FAQPage +
  BreadcrumbList.
- **Area/partners pages**: Service/ItemList + BreadcrumbList.

### 4. Dynamic OG images
`GET /site/{slug}/og.png` and `/site/{slug}/book/{id}/og.png` render a
1200×630 branded card (tenant name, service + real price, consented
rating line, service count) in pure Go — `x/image` basicfont with
nearest-neighbor integer upscaling, no font files, no cgo, no network.
Deterministic output → long cache headers. `og:image` + `twitter:image`
with `summary_large_image` on every page. No consented reviews → no
stars on the card (honest degrade).

### 5. Per-page unique titles
Service pages: `Airport Transfer — from EUR 120.00 | Brand`. Area pages:
`Brand in Area — N concierge services | Book online`. Partners directory
and link-in-bio carry their own. Descriptions likewise per-page.

### 6. FAQ sections
`faqsForService` answers cost (real base + per-km/per-hour rates, or the
honest "quoted live" when unpriced), availability (real areas), booking
flow, and payment rails (derived from `payment_config` — the answer names
Stripe/Paystack only when actually enabled). Rendered as HTML and FAQPage
JSON-LD (rich-result eligible).

### 7. Performance / crawl efficiency
`renderSite` buffers → FNV ETag + `Cache-Control: public, max-age=300` +
304 on `If-None-Match`. Sitemaps/robots/OG carry their own cache headers.
Sitemap gains `lastmod` (freshest real timestamp held — tenant or area
`created_at`; never future-dated) and per-URL priority.

### 8. hreflang + locale
`site_config.locale` sets `<html lang>` and `hreflang` self + `x-default`
alternates. Default `en` when unset.

### 9. Search Console
`site_config.google_verification` emits the ownership meta tag on the
landing page. Platform-level `GET /sitemap.xml` is a sitemap **index**
listing every tenant sitemap (custom-domain origin when configured) — one
URL to submit for the whole platform. Console Settings card captures
domains / verification / locale / social links with CNAME instructions.

### 10. Partner directory
`GET /site/{slug}/partners` — public, indexable directory of **VETTED**
partners only (PENDING/SUSPENDED never render), ItemList JSON-LD,
breadcrumb, and partner-campaign booking links so directory clicks close
the attribution loop. Compliant by construction: real pages with real
content, not keyword-stuffed widget links.

## Console
Settings gains the "Public site & SEO" card (custom domains, Search
Console token, locale, social links). `types.ts` gains `site_config`.

## Verification
- `go build`, `go vet ./...`, `go vet -tags pg ./...` — clean.
- `go test ./...` — domain, server, tenantctx all pass.
- 5 new server tests (`site_test.go` external + `site_internal_test.go`):
  custom-domain resolution (canonical/robots on tenant domain, unknown
  host fallthrough, header-spoof guard), OG images (PNG bytes, cache
  headers, 404 honesty), service-page SEO (unique title, FAQPage,
  BreadcrumbList, FAQ HTML, ETag/304), landing SEO (priceRange, geo,
  sameAs, hreflang, no fabricated stars), sitemap lastmod + platform
  index + partner directory vetting gate.
- 1 new pg test (`pg_integration_phase28_test.go`): site_config roundtrip,
  non-wipe across unrelated patches, GetByDomain positive/negative, RLS-
  unaffected List/GetBySlug coverage.
- Python suites: 26 agent + 55/2skip whatsapp + 37 voice + 16/1skip
  analytics — all green. Console build green (251 kB).
- Rust untouched (no crate changes; CI rust job covers it).

## Honest constraints
SEO compounds over months — no code changes that. What changed: authority
now *can* compound on the tenant's own domain, every page has unique
substantive content, and every shared link renders a branded card. The
remaining external dependency is DNS: a custom domain only resolves after
the tenant points its CNAME at the platform host (console card says so).
