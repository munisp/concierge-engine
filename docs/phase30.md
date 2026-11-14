# Phase 30 — Site Studio + Tenant Insights

Phase 30 adds two tenant-facing capabilities: a **website design studio**
(theme presets, business-case content generation, multi-shape deployment)
and an **operational analytics overview** (the KPI read across bookings,
revenue, service mix, channels, fleet, drivers, and reviews).

## Open-source builder research (the "is there an existing platform?" answer)

The landscape splits into three buckets:

| Option | License | Verdict |
|---|---|---|
| WordPress | GPL | Not embeddable — a separate stack. Treated as an **integration target** (shortcode plugin + iframe embed), not a foundation. |
| Ghost | MIT | Separate publishing stack; no booking/commerce primitives. Integration target at best. |
| Webstudio / Silex / VvvebJs | AGPL | **Rejected.** Copyleft obligations are incompatible with embedding inside a commercial multi-tenant SaaS. |
| GrapesJS | BSD-3 | Viable embeddable builder framework (vanilla JS). Heavy: a full drag-drop canvas is overkill when the site is already data-driven. |
| Puck | MIT | Viable React visual editor that stores JSON in *your* backend. Documented as the evaluated candidate for a free-form canvas in a later wave. |

**Decision: the studio is a data layer on our own SSR engine, not an
embedded canvas.** The public site is already fully data-driven (catalog,
service areas, i18n maps, reviews, brand config), so "website design"
decomposes into exactly two things: **theme tokens** (colors, font,
corners) and **content** (tagline, descriptions, per-service copy). Both
are data. A canvas editor would add megabytes of editor JS and a parallel
document model to produce the same pages the template engine already
renders — with worse SEO (client-side canvas state vs. server-rendered
HTML with hreflang clusters and JSON-LD).

## Site Studio

### Theme presets & design tokens

- `site_config.theme` (jsonb — no new migration) holds `{preset, primary,
  accent, font, corners}`.
- Five curated presets: **noir-gold** (the Phase 29 default — existing
  tenants render identically until they opt out), azure-modern,
  slate-minimal, emerald-classic, terracotta-warm, plus **custom** with
  explicit `#rrggbb` tokens.
- The SSR template is parameterized on CSS custom properties
  (`--ink`, `--accent`, `--font-main`, `--radius`); a theme change is a
  `:root` override — the next request re-renders in the new look. No
  rebuild, no deploy.
- Validation is strict: unknown presets, malformed hex, and unknown enum
  values are rejected (422) and never stored.

### Business-case content generation

`POST /v1/studio/generate` takes a free-text business case ("luxury
private tours around Lisbon for families and executives") and drafts a
tagline, brand description, and per-service descriptions **grounded in the
tenant's real catalog** — the generator never invents services. With the
concierge-agent configured, the agent writes the copy; any agent failure
falls back to deterministic template prose. The draft carries an explicit
`engine` provenance label (`agent:*` vs `template`), and **nothing is
written until `POST /v1/studio/apply`** — the tenant reviews first. Apply
validates every referenced service ID belongs to the tenant
(all-or-nothing; a foreign ID refuses the whole draft) and writes into the
Phase 29 `brand_i18n` / `desc_i18n` maps, so generated copy is per-locale.

### Deployment shapes — same host or different hosts

`GET /v1/studio/integrations` returns every shape as copy-paste artifacts:

1. **Hosted (same provider):** the platform serves the full site at
   `/site/{slug}/` — zero ops.
2. **Custom domain (same platform, tenant's domain):** CNAME to the
   platform host; Phase 28 domain resolution serves all pages, sitemap and
   OG cards on the tenant's domain.
3. **Static export (split-host):** `GET /v1/studio/export` renders the
   site through the real dispatcher into a zip (`index.html`,
   `book-*.html`, `area-*.html`, `links.html`, `partners.html`,
   `README.txt`). Internal links are relativized to the flat layout;
   **booking CTAs stay absolute to the platform** (`/capture?tenant=…`
   with full attribution). Host the files on Netlify/S3/GitHub Pages/shared
   hosting — the site and the platform run on different providers, and
   bookings still land in the same funnel.
4. **CMS embed:** an iframe snippet (`/embed/{slug}`) for WordPress
   (Custom HTML block), Webflow, Shopify, Wix — anything that can embed an
   iframe. Bookings arrive stamped `channel=widget`.
5. **WordPress plugin:** generated PHP source for a
   `[concierge_book]` shortcode wrapping the embed — save, activate, use.

## Tenant Insights — how robust is the analytics?

`GET /v1/analytics/overview?days=N` (default 30, clamped to 1–365) +
console `/insights` dashboard. Built entirely on existing stores — no new
tables, no ETL lag.

**What it covers:**

- **Bookings:** created / confirmed counts in the window.
- **Revenue:** GMV (confirmed bookings' quote totals) and collected
  revenue (actual `TENANT_REVENUE` ledger postings) — so tenants see the
  gap between booked and paid.
- **Service mix:** per-service bookings, GMV, share — which services
  actually earn.
- **Channels:** acquisition funnel reuse (SEO / widget / QR / partner /
  social) — where bookings come from.
- **Fleet (cars):** per-vehicle assignments, hours booked, utilization %
- **Drivers:** per-driver assignments, completed trips, review count,
  running average rating (joined via crew-by-assignment).
- **Clients:** new clients in window.
- **Reviews:** count + average rating.

**Honest limits (carried in the response `notes` and the console footer):**

- Utilization divides booked hours by a **10-hour assumed service day** —
  a planning heuristic, not telematics.
- This is the **operational read**: real-time, straight from the OLTP
  stores. The nightly gold layer (`analytics/`, LTV/churn/channel SLA)
  remains complementary for cohort economics.
- No per-trip cost accounting yet (fuel, tolls, driver payout netting) —
  GMV and collected revenue are gross.

## API surface

| Method | Path | Purpose |
|---|---|---|
| GET | `/v1/studio/presets` | Theme preset gallery |
| PUT | `/v1/studio/theme` | Set preset / custom tokens |
| POST | `/v1/studio/generate` | Draft content from business case |
| POST | `/v1/studio/apply` | Apply a reviewed draft |
| GET | `/v1/studio/export` | Static site zip (split-host) |
| GET | `/v1/studio/integrations` | All deployment shapes as artifacts |
| GET | `/v1/analytics/overview?days=N` | Tenant KPI overview |

## Console

- **Site Studio** (`/studio`): preset gallery with live color swatches,
  business-case generator with provenance label, review-and-apply flow,
  static export download, integration shapes with copy-paste snippets.
- **Insights** (`/insights`): 7/30/90/365-day window selector, eight KPI
  cards, service mix / fleet / drivers / channels tables, honesty notes.

## Testing

- Domain: preset validation + token resolution, generate-fallback
  provenance + apply round-trip, insights aggregation (bookings, GMV,
  collected, fleet hours with float tolerance).
- Server: theme API end-to-end (preset → landing CSS tokens; invalid →
  422, never stored; survives unrelated tenant patches), generate/apply
  through the API, static export (zip magic, flat-layout relativization,
  absolute CTAs, real rendered content, structured data preserved),
  integrations JSON (iframe + shortcode), analytics overview on a golden
  path (bookings/GMV/collected/service mix/clients) + tenant isolation +
  window clamping.
- Postgres (`-tags pg`): site_config.theme jsonb round-trip,
  CatalogStore.Update round-trip incl. foreign-ID and cross-tenant
  rejection.
