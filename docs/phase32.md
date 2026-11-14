# Phase 32 — End-to-end onboarding, console gap closure, design layer

**Status: delivered.** Trigger: "don't see payment and other catalog
services setup… onboarding should allow tenants to set up everything they
need to run the business end to end." Three threads: (1) honesty — the
site claimed "secure online payment" whenever the tenant hadn't disabled
rails, even with zero PSP keys configured; (2) onboarding covered only a
slice of go-live, and several steps had no console surface at all;
(3) the public site had exactly one layout, which read as "replica of the
template" rather than a designed site.

## 1. Payment honesty — `LiveRails`

The trust gate now reads **rail availability**, not flag state:

```go
func (svc *PaymentService) LiveRails(pc model.PaymentConfig) []string
```

A rail is live iff the platform has the PSP key (`CE_STRIPE_KEY` /
`CE_PAYSTACK_KEY`) AND the tenant hasn't disabled that rail. Consumed by:

- the landing trust chip (`TrustedPayment`),
- the platform-feature band card ("Secure payments" / "Pagamentos
  seguros" only renders with a live rail),
- per-service FAQ payment answers (rail-specific copy: Paystack-only,
  Stripe-only, generic, or the honest "the concierge arranges payment
  with you directly" when no rail is live),
- the onboarding payments step, whose detail now says "payment links run
  in SANDBOX mode — platform operations sets CE_STRIPE_KEY /
  CE_PAYSTACK_KEY" instead of pretending.

Tests: `TestSitePaymentHonesty32`, `TestLiveRails32`.

## 2. Onboarding: 21 steps, 4 groups, all derived from real resources

`OnboardingStep` gains `group` (`essentials | operations | presence |
growth`). The journey now covers the full business setup:

- **essentials** — profile, catalog, areas, payments, pricing, tax
- **operations** — whatsapp, team, fleet, drivers, dispatch_cfg
- **presence** — website, site_design, imagery, socials, custom_domain
- **growth** — templates, lifecycle, first_campaign, first_booking,
  go_live

Statuses are still derived server-side from real resources on every read
(fleet/crew counts from dispatch, template count, media count, live
rails…); persisted state is only the track choice and skipped ids. Every
step carries `action` — the console path that completes it — and the
console renders it as an "Open →" link per pending step, grouped with
per-group progress.

Test: `TestOnboardingEndToEnd32` (fresh tenant → pending set; ops core →
done; payments honestly pending without keys; skip/unskip on the new ids;
bogus skip id → 422).

## 3. Console gap closure — every onboarding step completable in the UI

The audit found four holes where the API existed but the console didn't:

- **Catalog page** (`/catalog`, new): create services (vertical, name,
  currency, base/per-km/per-hour pricing, auto-quotable, card image from
  the media library); inline edit of every mutable field; deactivate /
  reactivate. Backing change: `PUT /v1/catalog/{id}` grew from
  `image_id`-only to the full mutable field set (PATCH-shaped PUT with
  validation — explicit empty name/vertical/currency and negative money
  rejected). **Bug found by the new test:** `CatalogStore.List` filters
  on `Active`, so a deactivated service became unreachable by the update
  path — no reactivation was possible. Fixed with `CatalogStore.Get`
  (active-agnostic, tenant-scoped) implemented in both engines.
- **Dispatch fleet & crew** (`/dispatch`): add vehicles (kind + label)
  and crew (name + certificates) inline; lists show alongside the board.
- **Studio media library** (`/studio`): upload (file → base64 JSON),
  thumbnail grid via the new tenant-scoped `GET /v1/media/{id}/raw`,
  set-as-hero, gallery membership, delete (with reference cleanup:
  deleting the hero clears `hero_image_id`, deleting a gallery image
  removes it from `gallery_ids` before save).
- **Onboarding page**: grouped rendering + working action links
  (`step.action` was previously collected by the API and never used).

Because `PUT /v1/tenant` patches `site_config` **wholesale**, every
console save loads the tenant first and carries the whole object
(theme, brand_i18n, locales…) back — no partial-clobber regressions.

Tests: `TestCatalogConsole32` (mutable-field update, deactivate →
reactivate via Get, validation, raw-media bytes + cross-tenant 404).

## 4. Design layer — tokens vs layout

User feedback: "the website is a direct replica of the existing website,
really not innovation." Root cause: Phase 30/31 delivered *tokens*
(theme presets) on exactly one layout. Phase 32 splits the two:

- **Theme** (unchanged): palette/font/corner tokens — `site_config.theme`.
- **Design** (new): the layout family — `site_config.design`:
  `classic` (default; the Phase 31 card grid) or `editorial`. Editorial
  is a pure CSS re-flow of the SAME DOM (`body.design-editorial` +
  an injected stylesheet): static transparent nav, left-aligned hero with
  a masked side image, `clamp()`-sized serif display type, alternating
  service rows, hairline stats band, squared bands, 4-column gallery,
  mobile fallbacks. Same DOM, same data — a different design.
- **Section visibility** (new): `site_config.hidden_sections` — any of
  `trust | stats | services | steps | platform | gallery | reviews |
  areas | cta`. Visibility only; ordering stays engine-owned so the
  conversion narrative can't be scrambled into incoherence.
- **Gallery strip** (new): `site_config.gallery_ids` (≤ 8) rendered
  between services and how-it-works; dangling ids are skipped at render
  (never a broken `<img>`).

Validation: `ValidateSiteConfig` (theme + design + sections + gallery
cap) now runs on BOTH write paths — the studio endpoint and the `PUT
/v1/tenant` wholesale patch, which previously bypassed theme validation
entirely. Unknown design/section or >8 gallery → 422.

Console: Site Studio gained a "Layout & sections" card (family radio +
per-section toggles, saves immediately) and the media library drives
hero/gallery selection visually.

Tests: `TestSiteDesignLayer32` (editorial markers, classic cleanliness,
hidden sections absent from the DOM, three validation 422s, gallery
renders the live asset and skips the dangling id).

## 5. Visual site-builder spike — Puck (evaluation, not adopted)

Question: should the studio embed an open-source drag-and-drop page
builder instead of curated layout families?

**Candidate: [Puck](https://github.com/puckeditor/puck)** — the
React-based visual editor originally from measured.co, now maintained at
puckeditor/puck. License re-verified as **MIT** (GitHub API reports
SPDX `MIT`, ~13.7k stars at time of writing). React-only, which matches the console but
NOT the server-rendered Go template that serves the public site today.

Mapping sketch if adopted:

- Puck outputs a block tree (`{ content: [{ type, props }] }`) — that
  JSON would ride on `site_config` (jsonb, no migration) exactly like
  `theme`/`design` do.
- A renderer port: block type → Go template partial. Blocks would be a
  closed set (hero, services-grid, text, gallery, CTA) mapped onto the
  existing locale/i18n + catalog-resolution pipeline — never raw HTML
  (same XSS reasoning as the SVG refusal).
- Editor embed: Puck in the console's Studio page, config bound to the
  tenant's real catalog (service picker props), save → `PUT /v1/tenant`.

**Why not now:** the free-form canvas fights the two things that keep
tenant sites trustworthy here — (a) content is derived from real
resources (catalog prices, live rails, areas), never typed into the page;
(b) locale/hreflang/SEO/structured-data invariants are template-owned.
A block editor makes both the tenant's problem. Effort estimate: ~2
phases (editor embed + block JSON schema + renderer port + migration of
existing fields), against near-zero benefit over one more curated layout
family. **Recommendation: stay curated** — add a third design family when
asked (a family is ~150 lines of CSS), revisit Puck if tenants demand
per-page composition.

Revisit triggers: tenants asking for landing pages beyond the one-pager
(campaign pages, per-service pages), or a white-label agency tier.

## Verification

- `go build ./...`, `go vet ./...` (both tag sets), `gofmt` clean on
  touched files; full core-api suite green incl. the four Phase 32 suites
  (`site32_test.go`).
- Console: `tsc -b && vite build` clean (57 modules).
- pg roundtrip: `site_config` design/sections/gallery persist through
  Postgres jsonb (integration test, `go test -tags pg`).
