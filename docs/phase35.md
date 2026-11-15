# Phase 35 — Customer acquisition & outreach: 30 innovations

**Status: delivered.** Trigger: the platform could *serve* VIP clients
end to end, but its growth surface was thin — a landing page, a widget,
and consent-gated one-off sends. This phase adds 15 acquisition and 15
outreach innovations, each implemented behind the platform's standing
honesty gates: no invented coverage, no invented prices, no invented
audiences, no sends without consent.

## Acquisition (A1–A15)

| # | Innovation | Surface |
|---|------------|---------|
| A1 | Airport SEO landing pages — one per covered IATA airport, with airport-relevant services, FAQs and JSON-LD (`Service`+`FAQPage`+`BreadcrumbList`) | `GET /site/{slug}/airport/{iata}` |
| A2 | City-pair route pages with live-catalog indicative pricing | `GET /site/{slug}/routes/{from}-to-{to}` |
| A3 | Anonymous instant estimate — prices one service from the live catalog; no client record, no request row | `POST /site/{slug}/estimate` |
| A4 | Shareable quote links — HMAC `quote/` token family, standalone no-store page with lines, deposit, expiry, capture CTA and WhatsApp deep link; expired quotes still render honestly | `POST /v1/quotes/{id}/share`, `GET /quote/{token}` |
| A5 | Referral leaderboard — real conversion + reward-credit counts per client | `GET /v1/growth/referral-leaderboard` |
| A6 | Channel link builder — seo/qr/widget/social/email/ads/partner capture URLs with full UTM tagging | `GET /v1/marketing/links` |
| A7 | Referer auto-attribution — conservative classifier (search → `seo/organic:{engine}`, social → `social/referral:{network}`, else bare host); explicit channel always wins; unknown = no attribution invented | `POST /v1/attribution` |
| A8 | First-booking credit — fixed-amount reward on a client's first confirmed booking, idempotent per client | domain (booking confirm hook) |
| A9 | Corporate self-serve signup — one call creates the account AND the linked booker contact, stamped `corporate_signup` attribution | `POST /v1/capture/corporate` |
| A10 | Social proof band on the site landing — real counts only (clients served, trips completed); zero counts render nothing | site landing |
| A11 | Review deep links — the track view carries `review_url`/`review_submitted`; the console Track page anchors `#review` and shows the already-on-file state | track view + console |
| A12 | Campaign windows — while a named window covers now, every capture CTA carries `utm_campaign={window}` and the page context moves to `utm_content` | tenant `acquisition_config.campaign_windows` |
| A13 | Embed widget theming — `?theme=light` inverted palette, `&compact=1` slim CTA bar; CTA routed through the channel URL builder | `/embed/{slug}` |
| A14 | Gift voucher storefront — lists only live (gift-flagged, active, unexpired) vouchers; 404 when there is no stock | `GET /site/{slug}/gift` |
| A15 | Sitemap coverage — airport URLs only for covered airports (geo check), route URLs only when a per-km auto-quotable service exists and both cities are covered (cap 40 pairs), gift URL only with live vouchers | `/site/{slug}/sitemap.xml` |

## Outreach (O1–O15)

| # | Innovation | Surface |
|---|------------|---------|
| O1 | Segment broadcasts — one campaign name fans out to a ruleset audience; idempotent per (campaign, client); replay re-tallies the stored disposition without re-delivering | `POST /v1/outreach/broadcast` |
| O2 | Segment engine + preview — locale, loyalty tier, VIP flag, org, consent, booking-count, dormancy and age rules; the preview count is exactly the broadcast audience | `POST /v1/outreach/segments/preview` |
| O3 | Drip sequences — multi-step campaigns; per-client progress derived from the sends ledger (`seq:{name}:{step}:{client}`), one new step per sweep, no state table | domain sweep (hourly) |
| O4 | Template A/B — deterministic FNV split into `#a`/`#b` arms when a B template exists; the split is an audience property, counted regardless of delivery status | broadcast `ab_test` |
| O5 | STOP/START keywords — exact-match consent keywords handled at intake before triage; silent when already in the target state; confirmation is a service-lane send | domain intake |
| O6 | Scheduled sends — `send.scheduled_for` records the send as `scheduled`; the hourly sweep delivers due sends and re-validates consent at delivery time | `scheduled_for` + `RunDue` |
| O7 | Quiet hours + preferred send time — marketing sends inside the quiet window defer to the client's modal booking hour (≥1h future, default 10:00) | `outreach_config` |
| O8 | Quote abandonment nudge — unconfirmed quotes past `quote_nudge_hours` (default 24) get one service-lane nudge carrying the A4 share link; expired quotes are never nudged | lifecycle sweep |
| O9 | Tiered win-back — the single win-back rule becomes three escalating tiers at 1×/2×/3× the dormancy window; tier 1 keeps its original idempotency key shape | lifecycle sweep |
| O10 | Anniversary messages — first-booking anniversary, once per year per client | lifecycle sweep |
| O11 | Review requests — completed trips past 72h with no review on file get a service-lane ask carrying the `#review` deep link | lifecycle sweep |
| O12 | Journey notifications — every assignment status transition (en route, arrived, on board, completed, cancelled) notifies the client; in-trip and no-show stay silent | dispatch hook |
| O13 | Monthly corporate summaries — org-linked clients with bookings last month get a spend summary on the 1st | lifecycle sweep |
| O14 | Re-permission campaigns — marketing consent older than a year is asked to renew, once per year | lifecycle sweep |
| O15 | Campaign funnel — per-campaign sent/scheduled/held/failed/converted plus A/B arm counts, parsed from the ledger | `GET /v1/outreach/campaigns/{id}/funnel` |

## Console

Growth page gains four cards — marketing links (per-channel capture URLs
with active-window state), referral leaderboard, broadcast composer
(segment rules + preview + A/B toggle) and campaign funnel — plus a
`datetime-local` scheduler on the outreach composer and a `scheduled`
rendering in the sends table. Track page gains the `#review` anchor and
the already-submitted state.

## Data & invariants

- One migration (`0024_phase35.sql`): `outreach_sends` gains
  `scheduled_for`, the status check admits `scheduled`, and a partial
  index covers due-scheduled lookups.
- Drip/campaign state is **derived from the sends ledger** via
  idempotency keys — no new state tables, replay-safe sweeps.
- Scheduled sends mutate through one guarded `UPDATE ... WHERE
  status='scheduled'` — the sweep cannot double-deliver.
- The `quote/` HMAC token family is distinct from `track/`; the trip
  token parser rejects both prefixes.
- Every public page is gated on real coverage, real catalog rows or real
  voucher stock — a sitemap that lists a 404 is treated as a bug.

## Verification

- Domain: 17 Phase 35 tests (quiet hours wrap, RunDue delivery +
  consent re-validation, sequence progression + gating, segments,
  broadcast idempotency + A/B split, STOP/START, quote nudge, tiered
  win-back, anniversary, review ask, journey, first-booking credit,
  leaderboard, quote-token separation) — full package green, including
  the Phase 18 post-trip test updated for journey-notification deltas.
- Server: 11 tests over the new endpoints and public pages — 404 honesty
  gates, tampered-token 403, referer classification, corporate capture
  idempotency, storefront 404-without-stock, sitemap coverage, broadcast
  replay/funnel.
- pg: scheduled_for round-trip, guarded `UpdateSendResult`, conflict
  replay (`pg_integration_phase35_test.go`, CI pg job).
- Console build clean; all five Python suites green.
