# Phase 13 — Operator alert push, gold-layer channel SLA, pg coverage expansion

Scope: `case.reply_failed` now reaches the operator on WhatsApp instead of
living only in the event stream; the lakehouse turns receipt evidence into
per-tenant/channel SLA medians; and the live-pg CI gate expands from the
Phase 6/11/12 surfaces to the Phase 1–3 CRM spine — catching another three
latent pg bugs.

## 1. Operator alert push (`case.reply_failed`)

`ReceiptsService` gains the adapter URL + operator phone (Deps →
`CE_WHATSAPP_ADAPTER_URL` + new `CE_OPERATOR_PHONE`). When a `failed`
receipt lands on a linked reply, after the case is annotated/escalated the
operator gets a WhatsApp message through the same adapter:

```
DELIVERY FAILED — case <id> (<title>): your approved reply never reached
the client (ref <wamid>). Resend advised.
```

Advisory everywhere: alert delivery failure never blocks receipt
recording; the event stream already carries `case.reply_failed` for
console/webhook consumers. Without `CE_OPERATOR_PHONE` the behavior is
unchanged (events + notes only).

## 2. Gold-layer channel SLA metrics

`analytics/lakehouse/gold.py` gains `build_channel_sla` →
`gold/channel_sla.json`, grain tenant|channel:

- `messages`, `failure_rate` (distinct failed refs / distinct refs)
- `median_sent_to_delivered_s`, `median_sent_to_read_s` — medians, robust
  to WhatsApp's long delivery tail; null when no pair exists (never
  invented)
- Provider timestamps preferred (`channel.receipt` events now carry the
  receipt's `at`), falling back to event time
- Replay-safe: first observation of a status wins, so at-least-once
  redelivery storms don't shift medians
- Labeled `receipt-latency/v1` — descriptive statistics over evidence,
  not a prediction, and the label says so

Tests: latency/failure computation, redelivery first-wins, tenant
isolation, empty.

## 3. pg CI coverage: Phase 1–3 store surfaces

`pg_integration_phase13_test.go` exercises the CRM spine against real
Postgres (same `CE_PG_TEST_DSN` gate, runs in the CI `pg` job):

- **Clients** — upsert (true upsert), get, find-by-phone, preferences
- **Catalog → request → quote → booking** — the full booking flow incl.
  idempotent request replay and `LatestForRequest`
- **Conversations** — create, append, timeline order, state transitions
- **Partners** — CRUD + RLS isolation + Phase 6 coordinates roundtrip
- **Referrals** — cross-tenant create/list/get/update under the bilateral
  RLS policy (both sides see it, nobody else)

### Latent bugs found desk-checking the exercised paths (fixed pre-push)

1. **`pgPartners` never read or wrote `lat`/`lng`** (added in migration
   0006) — on the pg path *every* partner would have been "unlocated" in
   NearestPartners. Columns added to `partnerCols`, `sql.NullFloat64`
   scan, `nullZeroFloat` write (0,0 = "no coordinates", per the domain
   convention).
2. **`pgReferrals.Create`/`Update`** — `""` strings into nullable uuid
   `request_id`/`booking_id` → cast error on any referral not yet linked
   to a booking (i.e. all of them at create time). `nilIfEmpty` applied.
3. **`scanReferral`** — NULL `request_id`/`booking_id` scanned into plain
   strings → any unlinked referral unreadable. `sql.NullString`.

The Phase 1–3 stores were otherwise written carefully (`nullif(...)::uuid`
writes, `coalesce` reads) — the Phase 4 cases code (fixed in Phase 12)
was the outlier, and referrals/partners had drifted since.

## 4. Verification

| Suite | Result |
|---|---|
| Go gofmt / vet (default + `-tags pg`) / tests | clean |
| `go build -tags pg ./...` | OK |
| domain tests | pass (+2: operator alert push, no-operator no-send) |
| analytics pytest | 16 passed (+3 channel SLA) |
| pg integration | 8 tests compile, skip w/o DSN, run in CI |
| whatsapp-adapter pytest | 52 passed |

## 5. Deferred / not in this phase

- **Per-tenant operator rosters** — `CE_OPERATOR_PHONE` is one global
  operator contact; per-tenant on-call rosters belong in tenant config.
- **Quotes `expires_at` zero-time** — exercised paths always set it; a
  `nilIfZero` hardening is noted for the next pg sweep.
- **Dispatch/payments/handoffs pg coverage** — next expansion wave.
