# Phase 31 — Media pipeline + premium site template v2

**Status: delivered.** The public acquisition site stops looking like a
placeholder: tenant-owned imagery, a premium multi-section layout, and the
platform's own features visible on the page (the user's Phase 30 feedback:
"site looks very plain… don't see how it integrates with the platform
features").

## Media pipeline

- `POST /v1/media` — JSON base64 upload (`kind`: hero | service | misc).
  Raster only (`image/jpeg|png|webp`) with content sniffing; SVG refused
  (scriptable markup on the platform origin is an XSS surface). 8 MB
  decoded cap, 12 MB JSON envelope cap.
- Storage: `tenant_media` (migration 0023) — bytea with a column CHECK on
  the size cap, FORCE RLS tenant isolation; in-memory engine mirrors it
  (`memory11.go` / `postgres12.go`).
- `GET /site/{slug}/media/{id}` — public serving through `siteDispatch`
  ("media" added to `siteReserved`). The tenant comes from the URL slug
  and the row must belong to it: cross-tenant ids 404. Immutable per id
  (re-upload = new id), so responses are `max-age=86400, immutable` with
  `nosniff` + a lockdown CSP.
- `GET /v1/media` (metadata-only list), `DELETE /v1/media/{id}`.
- `PUT /v1/catalog/{id}` — mutable catalog fields (`image_id`); the record
  is loaded first so untouched fields survive. `POST /v1/catalog` with an
  `image_id` works too. Both validate the media row exists and belongs to
  the tenant — dangling/cross-tenant ids are rejected, not discovered on
  the site.

## Template v2

Sticky glass nav (anchor links + book CTA) → full-bleed hero
(`site_config.hero_image_id`, gradient overlay, theme-gradient fallback;
the hero doubles as `og:image`) → trust chips → stats strip (services /
areas / ★ rating, only real values) → 16:9 image service cards (monogram
tile fallback) → how-it-works (3 steps) → dark platform-features band with
the inline booking widget → consented review quotes (capped at 3, text
only) → area pills → partners → FAQ → CTA band → sticky mobile CTA.

**Platform integration, visible:** the band presents five feature cards —
instant online booking, WhatsApp concierge, live trip tracking, secure
payments, vetted fleet & chauffeurs — plus the actual `/embed/{slug}`
booking widget iframed inline. The iframe always points at the platform
origin (`PublicBase`): hostRewrite only maps `/site/`, so a custom-domain
iframe src would miss `/embed`.

**Honesty rules preserved:** the payments card and the "secure payment
link" how-it-works step vanish/swap when all rails are disabled
(`Step2BodyNoPay`); review quotes are consented-only.

## Localization

New chrome strings (how-it-works, feature band, stats, CTA band, review
attribution) ship fully translated in en + pt-PT; other locales backfill
from English in `dictFor` — the same honest-degrade policy as Phase 29
guide prose.

## Export

`GET /v1/studio/export` now collects every referenced media asset (hero +
service cards), bundles them as `media-<id>.<ext>` in the zip, and rewrites
the URLs to the flat layout — the static export is fully self-contained
imagery-wise; booking CTAs and the widget iframe stay live-linked to the
platform (documented in the bundled README.txt).

## Verification

- 3 new server tests (`media31_test.go`): pipeline (upload/serve/validate/
  cross-tenant/delete), template v2 assertions (nav, hero, stats, band,
  iframe, card image, monogram fallback, service-page hero), catalog image
  validation + export bundling.
- 2 new pg tests (`pg_integration_phase31_test.go`): media round-trip +
  RLS isolation + delete; catalog `image_id` add/update persistence.
- Full suite green: `go test ./...`, `go vet` both tag sets, gofmt clean
  on touched files.
