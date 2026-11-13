# Phase 3 — The industry OS (Master Spec §14, Months 14+)

Gate: **supply density and network effects.** This phase turns the platform
from one tenant's operating system into an industry network: public API +
webhooks marketplace, cross-tenant referrals, GDS supply (Amadeus NDC seed),
ML v0, new-entrant instant-on, and UAE residency.

## Delivered

### Public API + webhooks marketplace
- **API keys** (`domain/apikeys.go`): tenant-scoped `cek_` bearer credentials.
  Secret shown once; only the SHA-256 hash persists; prefix lookup +
  constant-time compare; scopes `read` / `write` / `webhooks:manage`
  (`write` implies `read`). Middleware resolution in `tenantctx` — a request
  without `X-Tenant-ID` is authenticated via `Authorization: Bearer cek_...`,
  with method/scope enforcement at the edge.
- **Webhooks** (`domain/webhooks.go`): tenant-managed subscriptions with
  per-subscription `whk_` signing secrets. Every delivery is
  HMAC-SHA256-signed (`X-Concierge-Signature: sha256=<hex>` — the same
  verification pattern we enforce on Stripe/Meta ingress). At-least-once:
  dispatcher (`RunDispatcher`, started in `cmd/core-api`) retries with
  exponential backoff (`2^min(attempts,8)` minutes, capped 4h, max 8
  attempts); undelivered records stay queryable for manual replay. Fan-out
  hooks the EventBus *after* outbox persistence — the outbox remains the
  durable event spine.
- Console: **Integrations** page (`web/console/src/pages/Integrations.tsx`).

### Cross-tenant referral network
- `domain/referrals.go`: OFFERED → ACCEPTED/DECLINED → SETTLED lifecycle.
  Receiving tenant must opt in (`AcceptsReferrals`); consent is explicit
  per tenant (§13). Commission (default 10%, per-referrer
  `ReferralFeePct`) settles through the ledger
  (`TENANT_REVENUE → PARTNER_PAYABLE`, idempotency key
  `referral:commission:<id>`); settle is absorbing (replay-safe).
- Bilateral visibility enforced in the store + Postgres RLS policy
  `referral_bilateral` (either side sees the row, nobody else).
- Metrics endpoint: offered/accepted/settled, accept rate, commission
  earned/paid — the network-effect KPI surface for the phase gate.
- Console: **Referrals** page.

### GDS / hotels (Amadeus NDC seed)
- `domain/gds.go`: `GDSPort` with a live **Amadeus adapter**
  (`test.api.amadeus.com` sandbox; OAuth client-credentials with cached
  token, 30s margin) — NDC flight offers (`/v2/shopping/flight-offers`),
  hotels two-step (by-city → offers). No credentials → deterministic mock
  with the real search params, so dev/CI never need secrets.
- Live-first with graceful degrade: a failed live call falls back to mock
  and reports `source: "mock"` honestly (attach-rate analytics depend on
  truthful sourcing).
- **Attach rate** (the Amadeus pilot's proof metric): `POST /v1/gds/attach`
  links an air/hotel offer to a ground booking; emits `supply.attached`
  plus a `gds_attach` attribution event.
- Config: `CE_AMADEUS_BASE_URL`, `CE_AMADEUS_CLIENT_ID`,
  `CE_AMADEUS_CLIENT_SECRET`.

### ML v0 — pricing / churn / LTV
- `analytics/lakehouse/gold.py`: deterministic, explainable heuristics
  (trained models replace rules only on eval-harness evidence, per the AI
  spec's autonomy contract):
  - **LTV**: observed revenue + `avg_booking × repeat_probability × 4`.
  - **Churn risk**: `min(1, days_since_last / (2 × median_gap))` with
    low/medium/high tiers driving outreach playbooks.
  - **Pricing**: bounded ±5% suggestions from silver KPIs (instant-quote
    share, partner fill rate). Never self-executing — applied by a human
    or the autonomy-dial-gated agent.

### New-entrant instant-on
- `domain/instanton.go` + `POST /v1/platform/tenants/instant-on`: one call
  provisions a tenant (archetype `new_entrant`) with default pricing rules,
  a four-service starter catalog (airport transfer, hourly chauffeur, meet
  & greet, on-request helicopter — all quote-visible), and the sandbox
  WhatsApp BSP channel marker. A new operator can take their first booking
  in minutes.

### UAE residency
- `Tenant.Region` validated at creation (`EU` default | `UAE`); persisted
  in `db/migrations/0003_phase3.sql`.
- `GET /v1/tenant/residency`: tenant-facing residency statement — region,
  primary/backup homes, applicable framework (GDPR / UAE PDPL), data
  classes covered.

### SOC 2 Type II
- Control mapping: `docs/soc2-control-map.md` — every CC-series criterion
  mapped to a concrete implementation artifact in this repo.

## API summary

| Route | Purpose |
|---|---|
| `POST /v1/apikeys` · `GET /v1/apikeys` · `DELETE /v1/apikeys/{id}` | API keys (secret once) |
| `POST /v1/webhooks` · `GET` · `PUT /v1/webhooks/{id}` · `GET /v1/webhooks/deliveries` | Webhook subscriptions + delivery log |
| `POST /v1/referrals` · `GET` · `GET /v1/referrals/metrics` · `POST .../{id}/accept|decline|settle` | Referral network |
| `GET /v1/gds/flights` · `GET /v1/gds/hotels` · `POST /v1/gds/attach` | GDS supply + attach rate |
| `POST /v1/platform/tenants/instant-on` | New-entrant provisioning |
| `GET /v1/tenant/residency` | Residency statement |

## Tests

- `internal/domain/phase3_test.go` — key lifecycle (wrong/revoked secrets,
  scope validation), webhook fan-out + signed dispatch against a real
  `httptest` receiver, failure backoff, referral lifecycle incl. commission
  math + absorbing settle + bilateral visibility, Amadeus live parse +
  token caching + honest mock degrade, attach validation, instant-on,
  residency validation.
- `internal/tenantctx/tenantctx_test.go` — API-key middleware: scope gates,
  tenant resolution, bogus-key rejection, platform-route exemption.
- `analytics/tests/test_gold.py` — LTV math, churn tiers, pricing rules.
