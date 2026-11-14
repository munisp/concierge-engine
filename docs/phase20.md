# Phase 20 — Promo Campaigns + Per-Tenant Marketing Caps + Reward Visibility

Phase 19 made the reward program two-sided but kept it referral-only. Phase 20
generalizes credit accrual to **promo-code campaigns**, lets tenants tune the
outreach frequency cap, and surfaces reward credit to clients at the moment it
matters — the payment.

## Promo campaigns (`domain/promos.go`)

Tenants mint fixed-credit codes (`POST /v1/promos`, listed at `GET /v1/promos`,
managed on the console growth board):

- **Fixed credit** (`credit_minor` + `currency`) — no percentage math without a
  booking context, no ambiguity about what a code is worth.
- **Once per client per code** — structural: a partial UNIQUE index on
  `(tenant_id, client_id, promo_code)` makes double redemption and replays
  safe in both store engines.
- **Caps and validity**: `max_redemptions` (0 = unlimited), `expiry_days` —
  the credit lives and dies with the campaign window (`expires_at` copied onto
  the reward, swept by the Phase 19 lazy expiry).
- **Redemption**: `POST /v1/promos/redeem {code, client_id}`; codes are
  case-insensitive (stored uppercase). The reward lands as
  `kind="promo_credit"` in the same `client_rewards` ledger as referral
  credit — it redeems against payment links with the Phase 18 mechanics
  (compensation posting, refund-returns-credit) unchanged.
- Events: `promo.created`, `promo.redeemed` (catalog updated).

Schema (`0015_promo_campaigns.sql`): `promo_campaigns` table (RLS);
`client_rewards.source_booking_id` goes NULL-able (promo credit has no source
booking) + `promo_code` column + the partial UNIQUE; `tenants.outreach_config`.

## Per-tenant marketing cap

`Tenant.OutreachConfig.MarketingCap7d` overrides the platform default of 3
marketing sends per client per rolling 7 days (0 = default). The outreach
verdict and send gates resolve the tenant's cap; the console composer's live
verdict shows it. Patched via `PUT /v1/tenant` (`outreach_config`).

## Client-facing reward visibility

- **Capture page**: promo-code input on the quote card (redeem → credit
  message); after the payment link is minted, the page states exactly what was
  applied — "€25.00 reward credit applied — you pay €X" — computed from the
  intent's `reward_applied_minor`, never assumed.
- **WhatsApp agent**: the `create_payment_link` tool result now carries
  `reward_applied_minor` + `amount_minor` + `currency`, so the reply can tell
  the client what their credit did — and the factuality pipeline admits those
  exact values (no invented numbers).

## Verification

- 5 new domain tests: redeem→credit→payment-link application, once-per-client,
  cap exhaustion + expired campaign + unknown code, create validation +
  idempotent re-create, marketing-cap override enforced in verdict and send.
- 2 new pg integration tests: campaign lifecycle + NULL-source accrual +
  partial-unique conflict + redemption counting + history roundtrip;
  `outreach_config` tenant roundtrip.
- `go build/vet` both tags, `go test ./...`, concierge-agent 26, adapter 50,
  console build — all green.
