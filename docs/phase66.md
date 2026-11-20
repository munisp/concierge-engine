# Phase 66 — Partner bill-to-desk folios

Monthly consolidated settlement for partner-sourced demand. A hotel desk
that books rides for its guests opts into desk billing; instead of charging
each guest per trip, every sourced CONFIRMED booking accrues its adjusted
gross onto the partner's OPEN folio for the booking month and currency. At
period end the operator issues the folio; settlement posts the money truth.

## Model

`Folio` per `(tenant, partner, period YYYY-MM, currency)` — migration
`0044_phase66.sql` (`partner_folios`, FORCE RLS, composite unique key,
period format CHECK, non-negative total CHECK). `partners` gains
`bill_to_desk boolean NOT NULL DEFAULT false` — desk billing is opt-in per
partner.

States: `OPEN → ISSUED → SETTLED`.

- **OPEN** — accruing. Each sourced confirm appends a `FolioLine`
  (booking, quote, guest name, amount) and increments the total.
- **ISSUED** — closed for the period; total frozen; payable.
- **SETTLED** — paid; ledger posted. Terminal.

## Where the amount comes from

The line amount is the **quote total at confirm time** — i.e. the adjusted
gross AFTER Phase 65 rate-card repricing. The hotel that negotiated −10%
is billed 8100 for a 9000 ride, exactly what its rate card promised.
Snapshot semantics: later rate-card changes never rewrite an issued folio.

Sourcing commission (Phase 63) accrues **independently** — the partner
still earns its channel commission on the same adjusted gross; the folio
is what the partner owes, the commission payout is what the platform owes.

## Lifecycle

| Action | Endpoint | Effect |
|---|---|---|
| Opt in/out | `PUT /v1/partners/{id}/bill-to-desk` `{enabled}` | toggles accrual; idempotent |
| List | `GET /v1/partners/{id}/folios` | newest period first |
| Statement | `GET /v1/folios/{id}` | folio + lines |
| Issue | `POST /v1/partners/{id}/folios/{period}/{currency}/issue` | OPEN → ISSUED, freezes total; replay converges; empty period → 404 |
| Settle | `POST /v1/folios/{id}/settle` | ISSUED → SETTLED + ledger postings; OPEN → 409; SETTLED replay → 200 |

## Money truth

Settle posts the same split as a card capture, because money truth must
not depend on the collection rail:

- `PARTNER_CLEARING → TENANT_REVENUE` — net of platform fee, key `folio:net:{id}`
- `PARTNER_CLEARING → PLATFORM_FEES` — platform take, key `folio:fee:{id}`

Idempotency-keyed per folio — a retried settle can never double-post.

## Honesty properties

- **Booking-once billing**: the store layer dedupes by booking ID — a
  retried confirm bills once.
- **Frozen issued periods**: accrual onto an ISSUED/SETTLED folio is
  refused (store: 409) and surfaces as `channel.folio_accrual_blocked`
  with the reason verbatim — the booking is real and billed manually;
  the statement is never silently reopened and the charge never silently
  dropped.
- **Per-currency folios**: a partner sourcing in two currencies gets two
  folios — totals are never blended (Phase 40 honesty).
- **Direct demand never accrues**: only sourced bookings
  (`SourcePartnerID` set, kind ≠ direct) hit the folio path.
- **Accrual never blocks the booking**: store failure is audit-logged,
  the confirm succeeds.

## Events

`channel.bill_to_desk_set`, `channel.folio_line_accrued`,
`channel.folio_accrual_blocked`, `channel.folio_issued`,
`channel.folio_settled`.

## Console

Partners page → **Bill-to-desk folios** card: opt-in toggle, folio table
(period, currency, total, state), expandable lines with guest names,
Issue / Settle actions.

## Files

`model/model20.go`, `store/memory24.go`, `store/postgres26.go`,
`domain/folios66.go`, `server/phase66.go`, `db/migrations/0044_phase66.sql`;
`postgres2.go` carries the `bill_to_desk` column; `booking.go` hooks
accrual into Confirm.
