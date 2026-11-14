# Phase 25 — Client journey tracking & lifecycle follow-ups

Two surfaces that turn a completed booking into an ongoing relationship:
the passenger can watch their own trip live (Uber-style), and the tenant
stays in touch all year without anyone remembering to.

## Client journey tracking (Uber-like monitoring)

The chauffeur side already existed (Phase 2 trip tokens, Phase 21 status
machine, geostamps, SSE board). The missing half was the passenger's.

- **Token family separation.** Client tracking tokens share the chauffeur
  link's HMAC discipline but carry a `track/` payload prefix.
  `ParseToken` (driver) rejects track-prefixed payloads and
  `ParseClientToken` rejects driver payloads — a monitoring link can
  **never** advance status or stamp positions, and a chauffeur link never
  masquerades as monitoring. Both reject tampering with 403.
- **`GET /v1/track/{token}`** — public like the chauffeur routes (the
  token is the credential; the tenant middleware exempts `/v1/track/`
  exactly like `/v1/trips/`). Returns a `TrackView`: status, pickup /
  drop-off / pickup time, service window, vehicle label, chauffeur
  **first name only**, and the latest geostamp as `position`.
- **Honest position.** Until the chauffeur's first stamp, `position` is
  null and the page says "position pending" — the UI never invents a
  location.
- **Operator mint:** `POST /v1/dispatch/assignments/{id}/track-link`
  returns `{token, url}`; the dispatch board's "Client link" button copies
  the absolute URL for WhatsApp/SMS to the passenger.
- **Console `/track`** — the passenger page: status stepper
  (assigned → en route → arrived → on board → in trip → complete),
  journey details, live position refreshed every 5s with last-update age,
  and an OpenStreetMap deep link. No login; no tenant header.

## Lifecycle follow-ups (all-year engagement)

The churn-hazard model existed in analytics but nothing acted on it. The
lifecycle engine closes that loop with two rules, both delivered through
the Phase 14 outreach engine — consent gates, frequency caps, idempotency
and delivery receipts apply unchanged:

- **post_trip** — `post_trip_hours` (default 24) after a trip completes,
  the client gets a service-purpose thank-you that asks for one
  preference to remember. Service lane: exempt from the marketing cap, no
  marketing consent required (it is part of the service they bought).
  Completion is anchored on the assignment window's end (bookings carry
  no completed_at; zero-window trips anchor on booking creation).
- **win_back** — `win_back_days` (default 90) without a booking marks the
  client for a marketing win-back. The consent gate decides: clients who
  never opted in are recorded **held**, never contacted — the refusal is
  auditable on the send record.

**Idempotency.** Post-trip keys are per assignment (`followup:post_trip:…`
— fires once ever); win-back keys are per client per ISO week
(`followup:winback:{client}:{year}-W{week}` — one attempt per week,
retryable next week; a held verdict replays as held, not re-forced).
Run counters only count records created by that run, so the hourly sweep
stays quiet once the work is done.

**Operation.** An hourly sweeper in `core-api` runs both rules per tenant
(mirroring the case-SLA sweeper); operators can trigger a run and see
`{post_trip_sent, win_back_sent, win_back_held, clients_scanned}` via
`POST /v1/followups/run` or the "Run sweep now" button on the new
Settings lifecycle card (kill-switch + both cadences, saved as a
selective `lifecycle_config` patch).

## Schema & tenancy

- Migration `0018_phase25.sql`: `tenants.lifecycle_config jsonb` (default
  `{}` — missing means the 24h/90d defaults, enabled).
- `PUT /v1/tenant` accepts `lifecycle_config` as a selective patch;
  pg roundtrips it through all five tenant code paths (insert, get,
  get-by-slug, list, update).
- Store gains `Clients().List` (sweep source, newest-first, limit) and
  `Dispatch().LatestGeoStamp` (tracking feed) in all three layers:
  interface, memory, pg — with RLS exercised in the pg tests.

## Tests

- **Domain (5):** track-token roundtrip + both families rejecting each
  other + tamper; TrackView joins (addresses, vehicle label, first-name
  chauffeur, honest pending → live position) and refuses ghost
  assignments; post-trip sends once and hourly re-runs don't double-fire;
  win-back consent gate (sent vs held, weekly idempotency); disabled
  config is a full no-op.
- **Server (2):** `/v1/track/{token}` 403s garbage, 404s a validly-signed
  ghost; follow-ups run endpoint + `lifecycle_config` surviving unrelated
  selective patches.
- **pg (3, CI):** LatestGeoStamp newest-first + not-found + RLS;
  lifecycle_config roundtrip incl. List and non-wipe by a dispatch
  update; clients List scope/order/limit/RLS.
