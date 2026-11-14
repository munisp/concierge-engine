# Phase 26 — New-client acquisition: fee billing, SEO site, 90-day cohort tracker

The platform's monetization pitch: bring each tenant **100 new clients in
their first 90 days** and take **10% of the transactions those clients
generate**. No software can guarantee an outcome — demand depends on the
market — but the platform *can* guarantee the three things that make the
promise honest and the fee collectible: **measurement** (was this client
acquired through a platform channel?), **attribution evidence** (which
channel, provable per booking), and **billing** (the 10% is computed and
posted automatically at settlement, never invoiced by hand). Phase 26
builds exactly those three.

## Acquisition fee billing (the 10%)

The fee is assessed at the moment it becomes undeniable — payment
settlement — and only when both conditions hold:

1. This is the client's **first completed booking** with the tenant
   (their second booking onward is the tenant's retention, not our
   acquisition).
2. The client's **first-touch channel** (Phase 14 attribution spine:
   earliest `channel`/`campaign` evidence, inherited downstream) is in
   the tenant's configured acquisition channels — default
   `seo, referral, outreach, ads, partner`. A `ref:<code>` campaign
   classifies as `referral` regardless of the raw channel.

When both hold, settlement posts a second ledger movement alongside the
existing fee split: `TENANT_REVENUE → PLATFORM_ACQUISITION_FEES` for
`round(amount × fee_pct)` (default 10%, tenant-configurable), idempotency
key `acq:fee:{intentID}` — webhook replays can never double-assess. The
tenant keeps the client relationship and the lifetime value; the platform
takes its cut of the value it provably introduced.

**Refund symmetry.** `charge.refunded` claws back the **exact original
posting** — looked up by idempotency key, never recomputed — so a fee
assessed at 10% is reversed at the original amount even if the tenant
later changes their fee_pct. Posting
`PLATFORM_ACQUISITION_FEES → TENANT_REVENUE`, key
`acq:refund-clawback:{intentID}`.

**Evidence-grade invoicing.** `GET /v1/billing/acquisition-fees` returns
an `AcquisitionInvoice` for a window (default: current calendar month):
attributed GMV (recovered per line by joining the posting's booking →
intent), gross fees, clawbacks, net, and per-line channel evidence from
the first-touch spine. This is the document that makes the 10%
defensible: every cent traces to a booking, a client, and a channel.

## Public SEO site (the acquisition surface)

Tenants get a crawlable, server-rendered public site with zero setup —
this is the channel that compounds beyond the 90-day window (referrals
and outreach carry the early weeks; SEO arrives back-loaded and keeps
working for free afterward).

- **`GET /site/{slug}/`** — landing page: tenant name, brand tagline /
  description, active catalog services with prices, service areas,
  JSON-LD `LocalBusiness`, canonical URL, Open Graph tags.
- **`GET /site/{slug}/book/{serviceID}`** — one page per service with
  JSON-LD `Offer` (price, currency) — these are the pages that rank for
  "airport transfer <city>"-class queries.
- **`GET /site/{slug}/areas/{areaID}`** — one page per service area
  (`areaServed` in JSON-LD) — the local-intent long tail.
- **`/sitemap.xml`** listing every page and **`/robots.txt`** pointing
  crawlers at it.
- **Every CTA feeds the attribution spine**: buttons link to
  `{ConsoleBase}/capture?tenant={uuid}&channel=seo&utm_source=organic&utm_medium=seo&utm_campaign={...}`,
  and the capture page now honors the `?tenant=` param — an organic
  visitor is stamped `seo` at first touch, so when their first payment
  settles, the 10% assessment above fires with its evidence attached.
- Pages are `html/template` SSR (no SPA crawl problem); JSON-LD goes
  through `json.Marshal` so `<>&` are escaped script-safe. Routes live
  outside the `/v1/` tenant middleware — the slug identifies the tenant.

## 90-day cohort tracker (the guarantee's dashboard)

`GET /v1/acquisition/cohort` answers "are we going to hit 100?" at any
moment:

- **Cohort definition:** clients created inside the window
  (`cohort_started_at` or tenant creation, + `goal_days`, default 90)
  who completed ≥1 booking and whose first-touch channel is an
  acquisition channel. Unbooked signups, direct-channel arrivals and
  pre-window clients are excluded — the number is honestly scoped to
  what the fee actually bills on.
- **Pacing & projection:** expected = goal × elapsed/days;
  projection = count × days/elapsed; `on_track = projection ≥ goal`.
  Per-channel breakdown (`by_channel`) shows where the growth is coming
  from so the mix can be rebalanced mid-flight.
- **Monday 09:00 UTC pacing alert** (stateless gate inside the hourly
  lifecycle sweep — no new goroutine) pushes a WhatsApp message to the
  tenant's operator roster (fallback `CE_OPERATOR_PHONE`):
  "Acquisition pacing: 34/100 new clients, day 21/90 (projection 146) —
  on track." Silent while disabled, on error, or before day 7 with zero
  clients.
- **Console `/growth`** gets the cohort card (progress bar, pacing,
  projection, ON TRACK / BEHIND badge, per-channel split) and the fee
  invoice card (attributed GMV, gross / clawback / net, line table).
  **Settings** gets the acquisition card: kill-switch, fee %, channel
  list, goal/days — patched selectively like every other tenant config.

## What "guarantee" means, contractually

The honest version the sales team can stand behind: the platform
guarantees the **machinery** — every acquired client is provably counted,
every fee is provably tied to a booking, pacing shortfalls surface weekly
while there's still time to act — and the 10% only ever touches
transactions the platform's own channels introduced. Tenants pay for
measured growth, not for promises.

## Verification

4 domain tests (`phase19_test.go`: assessment incl. second-booking and
direct-channel exclusions + referral classification, kill-switch, refund
clawback with invoice netting and evidence line, cohort scoping +
projection), 2 server tests (`TestAcquisitionEndpoints` — defaults,
selective patch roundtrip; `TestSitePages` — landing/service/area/
sitemap/robots content, JSON-LD, 404s), 1 pg integration test
(acquisition_config column roundtrip, GetBySlug/List carry, no wipe by
unrelated config patches). Full Go sweep + `go vet` (both build tags),
console build, all four Python suites green.
