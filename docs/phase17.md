# Phase 17 — Tax/VAT Readiness + Per-Tenant Payment Configuration

EU/US launch follow-on: quotes must carry tax correctly, and tenants need to
control which PSP rails they offer (an EU-only tenant has no Paystack
entity; a Nigeria-only tenant has no Stripe account).

## Tax engine (`domain/tax.go`)

Per-tenant `TaxConfig` selects the engine; the chosen engine is stamped on
the quote (`tax_engine`) — provenance is never silent:

| Engine | Behavior |
|---|---|
| `""` (default) | None. B2B/exempt or unregistered — totals carry no tax. |
| `flat` | Tenant-configured rate in basis points on the service subtotal. Deterministic, offline, right for a single-registration domestic launch. |
| `stripe-tax` | Live Stripe Tax calculation per quote (`/v1/tax/calculations`). Falls back to the flat rate when Stripe is unreachable — the quote says `flat` when it did. |

- **Inclusive vs exclusive**: `prices_include_tax` follows the market
  convention (EU B2C inclusive, US exclusive). Inclusive math extracts the
  tax from the price; exclusive adds it on top.
- **Deposit on the grand total**: the client pays tax on the deposit too.
- **Display**: the tax component is appended as a quote line
  (`VAT 19.0% (DE)`) so console, capture page and receipts render it with
  no UI-specific logic; the capture page adds an "includes tax" note.
- **Stripe Checkout** marks the price `tax_behavior=inclusive` — our totals
  always include any applicable tax by construction.
- **Honest boundary**: this engine computes the tax component of OUR quote.
  Filing, remittance and multi-registration nexus belong to the tenant's
  Stripe Tax dashboard / accountant.

## Per-tenant payment configuration

`Tenant.PaymentConfig{disable_stripe, disable_paystack}`:
- `choosePSP` skips disabled rails (explicit requests for a disabled PSP are
  rejected with a validation error).
- `GET /v1/payments/methods` filters disabled rails out of discovery.
- Both configurable via the existing `PUT /v1/tenant` endpoint (now also
  patches `tax_config`).

## Schema

Migration `0012_tax_config.sql`: `quotes.tax_minor`, `quotes.tax_engine`;
`tenants.tax_config`, `tenants.payment_config` (jsonb).

## Events

`quote.created` payload gains `tax_minor` + `tax_engine` (catalog updated).

## Verification

- 7 new domain tests: none-by-default, flat exclusive, flat inclusive,
  stripe-tax live + honest fallback, deposit-on-grand-total, locale country
  extraction, tenant payment-config routing + discovery filtering.
- pg integration: tenant config + quote tax roundtrip.
- Go build+vet both tags, full suite green; console production build.
