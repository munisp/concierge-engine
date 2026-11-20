# Phase 70 — partial payments against folios

Phase 66 made statements; Phase 67 made them dunned. But collection was
all-or-nothing — ISSUED → SETTLED in one shot — and a hotel desk pays
what it has collected, when it has collected it. Phase 70 makes
collection incremental without giving up a single money-truth guarantee.

## The payment ledger

Every amount received is a verbatim `FolioPayment` row on the folio
(migration 0046, `payments jsonb` — same pattern as the Phase 67
reminders ledger): amount, method (`bank_transfer` | `cash` | `card` |
`settle` | `other`), optional reference, received-at, recorded-by.
**Paid and outstanding are DERIVED from this ledger**
(`PaidMinor()`/`OutstandingMinor()`) — never stored twice, so the
balance can never disagree with the evidence.

## Rules

- **ISSUED-only.** An OPEN folio is still accruing (its total is not
  frozen — nothing to pay against); a SETTLED folio is terminal.
- **Positive amounts only; overpayment is a 409.** The recorded money
  can never exceed the statement.
- **Proportional money truth.** Each payment posts the same net/fee
  split as a card capture, keyed `folio:net:{id}:{paymentID}` /
  `folio:fee:{id}:{paymentID}` — a retried request posts once, and
  every payment carries its own ledger evidence.
- **Auto-settlement at zero.** When outstanding reaches zero the folio
  becomes SETTLED (same terminal state, same event) — and
  `channel.folio_settled` carries the cumulative net/fee across the
  whole payment ledger, matching the postings exactly.
- **Settle converges.** `POST /v1/folios/{id}/settle` after partial
  payments records only the outstanding remainder as the final payment
  (method `settle`) and posts only that remainder's split. Both paths
  end with a ledger that sums exactly to the frozen total: **SETTLED
  always means paid == total**.

## Everything downstream chases the balance

- **Aging** buckets the outstanding balance, not the face value; rows
  also carry `collected_minor` so the operator sees progress.
- **Dunning** quotes the outstanding balance in the reminder — a
  half-paid statement is dunned for the half still owed.
- **Console** folio card: Payment action, paid/owed display, payment
  history. **Aging page**: collected per currency. **Partner portal**:
  paid/owed on every statement.

## Files

- `model/model22.go` — `FolioPayment`, `PaidMinor`, `OutstandingMinor`.
- `model/model20.go` / `model21.go` — `Folio.Payments`; aging row
  `collected_minor` + outstanding semantics.
- `db/migrations/0046_phase70.sql` — payments column.
- `store/memory24.go` / `store/postgres26.go` — ledger persistence.
- `domain/folios70.go` — `RecordPayment`, shared `emitSettled`.
- `domain/folios66.go` — Settle records the remainder.
- `domain/folios67.go` / `folios68.go` — aging/dunning/portal on the
  balance.
- `server/phase70.go` — `POST /v1/folios/{id}/payments`.
- Tests: `domain/phase70_test.go`, `server/phase70_test.go`,
  `store/pg_integration_phase70_test.go` (incl. RLS).
