# Phase 74 — partner self-service payment links

Dunning (Phase 67/72) tells the partner what they owe; statement
delivery (Phase 71) hands them the document. But the only way to pay
was a bank transfer the operator then keyed in by hand. This phase
closes the loop: the statement carries its own collection rail.

## What landed

- **`pay/` HMAC capability token** (tenantctx-exempt, cross-family
  tokens rejected — a statement link can never open a payment page).
  `GET /v1/pay/{token}` renders a public pay page quoting the DERIVED
  outstanding balance: after partial payments (Phase 70) and credit
  notes (Phase 73), never the stale face value. Settled statements show
  PAID; open ones say they are still accruing.
- **Hosted checkout on the tenant's live rail**:
  `POST /v1/pay/{token}/checkout` mints a Stripe Checkout Session — or a
  Paystack transaction for the Africa-corridor currencies, same routing
  as guest checkout (Phase 15) — for the outstanding amount and 303s to
  it. No PAN ever touches platform servers (PCI SAQ-A, same as guest
  payments). Each checkout is a fresh quote-less INVOICE intent keyed
  `foliopay:{folio}:{intent}`; the signed-webhook metadata carries
  `folio_id` so captures route back without trusting tenant headers.
- **Capture convergence**: the Stripe/Paystack webhook branches on
  `folio_id` metadata into `HandleFolioCapture`, which records through
  the SAME `recordPayment` path as an operator-keyed payment
  (`recorded_by: "system"`) — the payment ledger, proportional net/fee
  money truth and auto-settlement are identical regardless of who paid.
  Replay-safe on the intent state.
- **Honest degrade**: with no live rail for the tenant+currency the
  page says online payment is not enabled and gives the bank-transfer
  reference — no fake checkout, and checkout POST is a 409.
- **Loud mismatch**: a capture whose balance already moved (partner
  also paid by transfer; two open checkout sessions) is acknowledged
  to the PSP but never absorbed — `channel.folio_payment_mismatch`
  fires with the reference for manual reconciliation, and Phase 73's
  refund is the remedy.
- **Console**: Pay link action on issued folios copies the public link.
- **Events**: `channel.folio_checkout_started`,
  `channel.folio_payment_captured`, `channel.folio_payment_mismatch`.

## Honesty notes

- The pay page never claims a rail it cannot charge: `rail_live` comes
  from configured PSP credentials AND the tenant's payment config, the
  same honesty gate as the guest flows (Phase 32).
- No schema migration: checkout intents reuse `payment_intents` with a
  NULL quote_id (the charge is against a folio, not a guest quote);
  the folio itself needs nothing new — the payment lands on the Phase
  70 ledger.
