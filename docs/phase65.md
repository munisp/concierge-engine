# Phase 65 — Partner Rate Cards

Phase 63/64 built the demand channels and closed their engagement gaps;
Phase 65 adds the commercial layer: **negotiated per-channel pricing**.
A hotel contracts a fixed airport-transfer fare for its guests, a travel
agency earns a standing discount on chauffeur hourly, a transport company
farming overflow in carries a markup. Rate cards express exactly that.

## 1. The model

One card per `(tenant, partner, vertical)` — upsert is the write path,
so replaying a card is naturally idempotent.

| Kind | `value` | Effect on the item subtotal |
|---|---|---|
| `pct_discount` | percent, 1–50 | −value% |
| `pct_markup` | percent, 1–50 | +value% |
| `fixed_total` | minor units, currency required | item subtotal becomes value |

Percentages are capped at ±50 — a deeper "discount" is a catalog pricing
decision, not a rate card. Fixed totals name their currency; a card whose
currency mismatches the quote's is **skipped with an event carrying the
reason** — a wrong-currency charge is never invented.

## 2. Quoting integration

`BookingService.QuoteRequest` applies cards after `BuildQuote`, before
tax:

- Only **sourced** demand reprices (`source_partner_id` set, kind ≠
  direct). Direct demand never sees a card — the direct channel's margin
  stays pure.
- Every adjustment lands on the quote as an explicit partner-named line
  (`Partner rate — Grand Hotel (−10%)`) — repricing is never silent.
- Adjustments are **pre-tax commercial terms**: tax and the deposit base
  follow the adjusted subtotal.
- Phase 63 sourcing commission accrues on the **adjusted gross**
  automatically, because commission is computed from the quote total.
- Items that are not auto-quotable (human-quote fallback) carry no
  adjustment — the human quotes them.

## 3. API & surfaces

- `GET /v1/partners/{id}/rate-cards`
- `PUT /v1/partners/{id}/rate-cards` `{vertical, kind, value, currency?}`
- `DELETE /v1/partners/{id}/rate-cards/{vertical}` (missing → 404)
- Console Partners page: per-partner rate-card editor
  (`RateCardsCard`).

Unknown partner → 404; suspended partners keep their cards (sourcing is
already blocked at intake — Phase 63 — so a card on a suspended partner
is inert by construction).

## 4. Files

- `internal/model/model19.go` — RateCard + kind vocabulary + 50% cap
- `db/migrations/0043_phase65.sql` — `partner_rate_cards` (RLS, composite
  PK, kind/value/currency CHECKs)
- `internal/store/{store.go,memory.go,memory23.go,postgres25.go}` — store
- `internal/domain/ratecards65.go` — CRUD + validation + quote hook;
  `booking.go` hook; events `channel.rate_card_set` /
  `channel.rate_card_applied` / `channel.rate_card_skipped`
- `internal/server/phase65.go` — CRUD endpoints
- Console `Partners.tsx` — rate-card editor
- Tests: `domain/phase65_test.go`, `server/phase65_test.go`,
  `store/pg_integration_phase65_test.go`
