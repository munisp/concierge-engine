# Phase 14 — Acquisition & Outreach hardening

Audit verdict going in: **acquisition — moderate** (attribution spine existed
but downstream events carried no channel/campaign, no funnel, no client
referral codes, capture ignored campaign params); **outreach — weak** (no
proactive outreach at all; client consent stored but never enforced; no
frequency caps). This phase closes those gaps end to end.

## Proactive outreach (new)

`OutreachService` (`internal/domain/outreach.go`) — the platform's only
non-reactive channel act:

- **Consent gate** — `marketing` sends require `client.Consent["marketing"] ==
  true`; `service` sends (booking updates, safety) are always allowed. A
  gated send is recorded as `held` with the reason — the decision is
  auditable, and replaying the same idempotency key returns the same verdict.
- **Frequency cap** — marketing sends limited to 3 per rolling 7-day window
  per client (`SendsForClient` window query; only `sent` records count, so a
  held attempt never consumes budget). Service sends are never capped.
- **Delivery** — through the channel adapter `/v1/send` (freeform inside the
  24h session, approved template outside it; the adapter decides and may
  hold). The channel message id (wamid) is captured onto the send record, so
  Phase 11 receipts close the delivery loop.
- **Honest degrade** — no adapter configured / adapter unreachable / bad
  response: the send is still recorded with status `failed` and the cause in
  `detail`; the attempt is never silently dropped.
- **Idempotency** — unique per `(tenant, idempotency_key)`; a replay returns
  the original record and never re-sends. Known limitation: the replay
  fast-path scans the tenant's 500 most recent sends, and a true concurrent
  race is caught by the store constraint but may double-notify the adapter —
  the store row remains single.

Routes (`internal/server/phase8.go`):
`POST /v1/outreach/send`, `GET /v1/outreach/sends?limit=`,
`GET /v1/outreach/verdict?client_id=&purpose=` (live consent/cap verdict for
the console composer).

Storage: `outreach_sends` table (`db/migrations/0009_outreach_sends.sql`) —
uuid PK, `purpose`/`status` CHECKs, params jsonb, `UNIQUE(tenant_id,
idempotency_key)`, window index `(tenant_id, client_id, purpose,
created_at)`, FORCE RLS. Memory + Postgres implementations, pg covered by
`TestPGOutreachSends` (idempotency, window, ordering, RLS isolation).

## Acquisition attribution (strengthened)

- **First-touch inheritance** (`internal/domain/acquisition.go`) —
  `request_created` and `booking` events copy channel/campaign from the
  client's earliest attributed touch (first event carrying a campaign wins;
  else first with a channel). The funnel now attributes demand and revenue
  to the acquisition source, not just the conversation.
- **Funnel** — `GET /v1/attribution/funnel` aggregates events by
  `(kind, channel, campaign)` with counts and value; console renders the
  stage table plus conversation→request and request→booking conversion
  percentages.
- **Client referral codes** — deterministic, derived from the client uuid
  (`VIP-` + first 8 hex chars, uppercase; `model.ClientReferralCode`). No
  column needed. `GET /v1/clients/{id}/referral-code` mints;
  `GET /v1/referrals/lookup?code=` resolves for capture pages.
- **Referral conversion** — a referee arriving via `/capture?ref=VIP-…`
  carries `campaign: "ref:<code>"` on their first touch; their **first**
  booking fires `referral.converted` (payload includes referrer client id
  when resolvable) and a `referral_converted` attribution row carrying the
  booking value. Conversion runs before the booking is tracked so
  first-booking detection can't count the booking itself.
  **Deferred honestly:** referrer ledger rewards are not yet posted — the
  conversion is visible in the event stream and funnel; a future payouts
  wave settles the reward to the ledger.
- **Capture page** — accepts `?ref=` / `?channel=` / `?campaign=` (plus
  `utm_source` / `utm_campaign` aliases), posts a `page_view` attribution
  event after client resolution. Advisory: attribution never blocks capture.

## Console

New **Growth** page (`web/console/src/pages/Growth.tsx`): funnel stage
summary + per-cell table, referral-code lookup with share-link format, live
outreach composer (verdict check → send), recent sends with delivery status.

## Verification

- `go build ./...` and `go build -tags pg ./...` — clean; `go vet` both tag
  variants — clean.
- `go test ./...` — all green, including 10 new domain tests
  (`phase9_test.go`): consent gate (adapter never called), service-always-
  allowed, frequency cap (4th held, service unaffected), idempotent replay
  (adapter called once), adapter-hold mapping, honest degrade without
  adapter, referral-code roundtrip (case-insensitive, malformed rejected),
  first-touch inheritance, referral conversion (exactly one across two
  bookings, value carried), funnel aggregation.
- pg integration (`pg_integration_phase14_test.go`, CI pg job): outreach
  roundtrip + idempotency conflict + window query + RLS isolation;
  attribution list (oldest-first) + funnel; referral-code lookup.
- Console `npm run build` — clean.
- Analytics `pytest` — 16 passed, 1 skipped (unchanged).
