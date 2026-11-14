# Phase 19 — Reward Sharing Program

Phase 18 shipped single-sided referrer credit at a flat 5%. Phase 19 turns it
into a proper **reward sharing program**: two-sided (the referee shares in
the reward), per-tenant configurable, capped, optionally expiring, and
visible to operators and clients.

## Program configuration (`model.RewardProgram`)

Per-tenant, patched via `PUT /v1/tenant` (`reward_program` jsonb):

| Field | Default | Meaning |
|---|---|---|
| `disabled` | false | Kill-switch: no accruals on either side. |
| `referrer_pct` | 0.05 | Referrer's share of the referee's first booking value. Falls back to the legacy `PricingRules.ReferralRewardPct`, then 5%. |
| `referee_pct` | 0 | Referee's welcome credit on their first booking (0 = none — Phase 18 behavior). |
| `max_reward_minor` | 0 | Cap per accrual (0 = uncapped). |
| `expiry_days` | 0 | Credit validity. **0 = never expires** — credit is never silently burned; a tenant must opt into expiry. |

## Two-sided accrual (`domain/rewards.go`, `domain/acquisition.go`)

On the referee's first booking under a `ref:<code>` campaign:

- **Referrer side**: `referral_reward` credit (self-referrals excluded, as before).
- **Referee side**: `referee_welcome` credit when `referee_pct > 0`.
- Both idempotent per `(tenant, client, source booking)` — the UNIQUE key
  makes replays safe — and both advisory: a reward failure never blocks a
  booking confirmation.
- `referral.reward_accrued` payload gains `kind`; `referral.converted`
  gains `referee_reward_minor`.

## Expiry

Lazy, on access — no background job: `Balance`, `Redeem` and `List` sweep
lapsed ACCRUED rewards to EXPIRED first. Expired credit never redeems, but
stays visible in the client's history (honest provenance — the client can
see what lapsed and when).

## Surfaces

- `GET /v1/clients/{id}/rewards` → `{balance_minor, rewards[]}` — expiry-aware
  balance plus full history (newest first), for console and client surfaces.
- Console growth board: the referral card gains a **Rewards** lookup —
  redeemable balance plus the reward history table (kind, amount, state,
  expiry).
- Redemption against payment links is unchanged from Phase 18: the intent
  keeps the full obligation, the PSP charges the reduced amount, capture
  posts the `PLATFORM_FEES → CLIENT_CLEARING` compensation, and a refund
  returns the credit — for both kinds of reward.

## Schema

Migration `0014_reward_program.sql`: `tenants.reward_program` (jsonb),
`client_rewards.expires_at`, state CHECK extended with `EXPIRED`, partial
index for expiry sweeps.

## Verification

- 5 new domain tests: two-sided accrual + welcome credit applied at link
  creation, accrual cap, disabled program, expiry sweep (balance/list/
  redeem), empty summary.
- 2 new pg integration tests: `reward_program` roundtrip, expiry sweep +
  history + redeem-skips-expired.
- All Phase 18 reward tests still green (legacy fallback chain honored).
- `go build/vet` both tags, `go test ./...`, console build — all green.
