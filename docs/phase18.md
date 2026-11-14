# Phase 18 — Referrer Ledger Rewards + pg CI Coverage Extension

Phase 14 shipped the referral conversion spine (deterministic client codes,
`ref:<code>` attribution, first-booking conversion) but deferred the actual
reward: referrers were tracked, never credited. Phase 18 closes that loop
end-to-end — accrual, redemption against the referrer's own payments, and
refund safety — and extends the pg CI gate to the stores that had no
integration coverage.

## Reward accrual (`domain/acquisition.go`)

On a referee's FIRST confirmed booking under a `ref:<code>` campaign:

- The referrer accrues `round(booking_value × pct)` of credit —
  `Tenant.PricingRules.ReferralRewardPct`, default **5%** when unset.
- Accrual is idempotent per `(tenant, referrer, source booking)` — a
  replayed conversion never double-credits (UNIQUE key in pg, conflict
  scan in memory).
- Self-referrals are excluded (`referrer.ID != clientID`).
- `referral.reward_accrued` is emitted with the amount and source booking;
  the `referral.converted` payload gains `reward_minor`.
- Advisory by design: a reward failure never blocks a booking confirmation.

## Redemption against payments (`domain/payments.go`)

The ledger invariant: **`PaymentIntent.AmountMinor` stays the FULL client
obligation** — settlement splits, reporting and refunds stay consistent —
while the PSP is charged `AmountMinor - RewardAppliedMinor`.

- **Link creation**: the client's ACCRUED balance (capped at the amount)
  is stamped on the intent as `reward_applied_minor`; the PSP call uses a
  charge copy with the reduced amount.
- **Capture**: settle posts the splits on the full amount, rewards are
  redeemed oldest-first (FIFO — rewards expire conceptually in order), and
  a compensation posting `PLATFORM_FEES → CLIENT_CLEARING` (idempotency key
  `reward:redeem:<intent>`) covers the difference, so the clearing account
  nets to what the PSP actually captured. `referral.reward_redeemed` emitted.
- **Refund**: the PSP is refunded at most the captured (reward-reduced)
  amount; the compensation is clawed back (`CLIENT_CLEARING → PLATFORM_FEES`,
  key `reward:refund-clawback:<intent>`) and the redeemed rewards return to
  ACCRUED — **a refund never burns referral credit**.

## Schema

Migration `0013_client_rewards.sql`:

- `client_rewards` — uuid PK, `UNIQUE (tenant_id, client_id,
  source_booking_id)` (accrual idempotency), `state` CHECK
  (`ACCRUED`/`REDEEMED`), balance index `(tenant_id, client_id, state,
  created_at)`, RLS tenant isolation.
- `payment_intents.reward_applied_minor bigint NOT NULL DEFAULT 0`.

## Store coverage

New `RewardStore` (`Accrue` / `Balance` / `Redeem` / `RefundRedemption`)
implemented in both engines: memory (conflict-scan idempotency, sorted
oldest-first redeem) and pg (`INSERT ... ON CONFLICT DO NOTHING`,
transactional `SELECT ... FOR UPDATE` redemption inside the tenant RLS
transaction). `payment_intents` create/select paths carry
`reward_applied_minor`.

## pg CI coverage extension

`pg_integration_phase18_test.go` adds integration coverage for stores that
previously only shipped with memory tests:

- **Rewards**: full lifecycle + duplicate-accrual conflict + cross-tenant
  RLS invisibility.
- **Dispatch**: fleet resource, crew member, assignment roundtrip with
  status transition (against a quote-born booking).
- **Payments**: intent with `reward_applied_minor` roundtrip; compensation
  posting idempotency.
- **Handoffs**: open → get → list-open → resolve leaves the queue.
- **Quotes**: zero `expires_at` roundtrips as NULL (`nilIfZero` hardening —
  a zero time would defeat the expiring-quotes partial index).

## Verification

- 5 new domain tests: accrual (5% default + no double-accrual), self-referral
  exclusion, tenant 10% override, full apply→capture→refund lifecycle with
  compensation + clawback postings, store semantics (conflict, FIFO,
  refund re-accrual).
- 5 new pg integration tests (above).
- `go build/vet` both with and without `-tags pg`, `go test ./...`,
  whatsapp-adapter (50 passed), concierge-agent (26 passed), analytics
  (16 passed) — all green locally; pg + Temporal suites verified in CI.

## Honest boundaries

- Rewards are platform credit, not cash: redemption happens only against
  future payments; there is no payout path (that would be a partner
  settlement, not a client reward).
- The compensation posting is attributed to `PLATFORM_FEES` — the platform
  subsidizes referral rewards out of its take rate. Tenants wanting the
  cost in a dedicated marketing ledger account can re-map codes when they
  need that reporting split.
