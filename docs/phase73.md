# Phase 73 — folio credit notes & refunds

Statements get corrected: a guest cancels after the period closed, a line
was mispriced, the desk is owed goodwill. Before this phase the only
options were dishonest ones — edit the frozen lines (destroying the
statement's integrity) or tell the partner to ignore the document. Now
the correction is a first-class row on the folio's own credit ledger.

## What landed

- **`FolioCredit` ledger on the folio** (migration 0048): every credit
  note or refund is a verbatim row `{id, amount_minor, kind, reason,
  created_at, recorded_by}` next to the reminders, payments and
  deliveries ledgers. The frozen lines are never edited.
- **Derived balances**: `CreditedMinor()` and `AdjustedTotalMinor()` are
  computed from the ledger, never stored — and `OutstandingMinor()` now
  subtracts credits, so aging, the dunning sweep, the statement page,
  the console and every delivery quote the corrected balance
  automatically. The document can never disagree with the books.
- **Credit notes (ISSUED)**: `POST /v1/folios/{id}/credits` reduces what
  the partner owes. Over-credit is a 409 — you cannot credit money the
  partner already paid. A credit that brings the balance to zero settles
  the folio: a fully-credited statement is collected, and
  `channel.folio_settled` reports the adjusted total (new
  `adjusted_total_minor` key; `net_minor` is measured against it).
- **Refunds (SETTLED)**: the money was collected, so the credit carries
  money truth flowing back — reversal postings `TENANT_REVENUE →
  PARTNER_CLEARING` (net) and `PLATFORM_FEES → PARTNER_CLEARING` (fee),
  proportional to the refund and keyed `folio:refundnet:{folio}:{credit}`
  / `folio:refundfee:{folio}:{credit}` so a retried request posts once.
  Cumulative refunds can never exceed what was actually paid (409).
- **OPEN folios take no credits** (409): the total is still accruing —
  dispute the line, not a moving statement.
- **Statement page**: a Credit notes section lists every row, and the
  totals show "Less credits" between the face value and the paid/
  outstanding lines — the partner-facing document explains the corrected
  balance.
- **Console**: Credit action on issued folios, Refund on settled ones
  (prompt quotes the correctable balance), credited amount in the
  paid/owed line, and the credit ledger in the expanded view.
- **Events**: `channel.folio_credit_recorded` (folio_id, credit_id,
  kind, amount_minor, reason, currency, outstanding_minor, settled).

## Honesty notes

- A refund posts reversal ledger entries immediately — the books reflect
  the money owed back to the partner. Actually moving the money (bank
  transfer back) is the operator's rail-side act; Phase 74's payment
  links cover collection, not payout.
- The proportional fee reversal assumes the fee is returned to the
  partner on refund — matching how the collection split was posted.
