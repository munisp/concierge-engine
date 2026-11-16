# Phase 48: Mojaloop Settlement / Payout Depth

The payout saga from Phase 2 moved money, but four production gaps
remained: the callback endpoint was unauthenticated, quote expiry was
stored nowhere (stale FX locks were executable), adapter notifications were
fire-and-forget with no core-side recovery, and every payout paid its own
scheme fee. Phase 48 closes all four.

## Callback authentication

When `CE_PAYOUT_CALLBACK_SECRET` is configured, the Mojaloop adapter
presents it as `X-Callback-Secret` on every notification and the core
verifies it in constant time — an unauthenticated callback endpoint is a
"settle anything" primitive. Empty secret = dev mode, no check. The Rust
adapter reads the same variable and attaches the header when set.

## Quote expiry is enforced

The scheme's quote carries an FX-lock expiry; the core now stores it
(`payouts.quote_expires_at`, `settlement_batches.quote_expires_at`) and
`Execute`/`ExecuteBatch` refuse a stale quote with 409 "quote expired —
re-quote". Riding an expired lock would settle at a rate the scheme no
longer honors.

## Core-side timeout recovery

Adapter callbacks are fire-and-forget HTTP. A crash or a dropped
notification used to strand partner funds in `IN_FLIGHT_SETTLEMENT`
forever. Now the hourly sweep (and `POST /v1/payouts/sweep` on demand)
compensates any IN_FLIGHT payout or batch older than 48 hours
(`PayoutSettlementTimeoutHours`), restoring the partner payable and paging
ops through the HITL lane. Compensation is idempotent — a late callback
after a sweep is an absorbing no-op.

## Settlement batching (netting)

A partner with a dozen completed trips used to pay twelve scheme fees.
Batching nets one partner's PENDING payouts (same currency — currencies
are never blended, the Phase 40 lesson) into ONE scheme transfer:

```
OPEN --quote--> QUOTED --execute--> IN_FLIGHT --fulfil--> SETTLED
                                       |
                                       +-- timeout/abort --> COMPENSATED
```

- **Assemble** (`POST /v1/settlements/batches`) collects the partner's
  unbatched pending payouts (≥ 2; a single payout settles cheaper through
  the single flow) into an OPEN batch. Idempotent on the caller key —
  replay returns the original batch even after its lines are linked.
- **QuoteBatch** locks FX + fees on the NETTED amount. The fee saving
  versus per-payout quotes is the point.
- **ExecuteBatch** reserves every member line in-ledger with the SAME
  idempotency keys as the single flow (`payout:reserve:{id}`), then
  initiates one scheme transfer (`batch:{id}`).
- **Fulfil** allocates the netted receive amount pro-rata by line amount
  (rounding remainder to the last line) and settles each line with the
  single-flow `payout:fulfil` / `payout:fxdiff` keys — a line's ledger
  footprint is identical whether it settled alone or in a batch, so lines
  can never double-post.
- **Callback routing**: member lines deliberately do NOT carry the batch
  transfer id. The callback router tries the single-payout saga first and
  falls back to the batch saga on miss — the transfer id decides which
  saga owns the transfer.
- **Abort/timeout** compensates every line back to the partner payable and
  pages ops via HITL.

## Settlement statement

`GET /v1/partners/{id}/settlement-statement` is the reconciliation
surface: payouts bucketed per currency (pending / in-flight / settled /
compensated, gross vs received, fees) plus the partner's batches. Answers
"what did we owe, what did we pay, what did the scheme take" without
blending currencies.

## Console

The Partners page gains a Settlements card: partner picker, assemble
action, per-currency statement table, and the batch list with
quote/execute actions.

## Endpoints

```
POST /v1/settlements/batches                  assemble (201, idempotent)
GET  /v1/settlements/batches                  list
GET  /v1/settlements/batches/{id}             detail
POST /v1/settlements/batches/{id}/quote       quote netted amount
POST /v1/settlements/batches/{id}/execute     one scheme transfer
GET  /v1/partners/{id}/settlement-statement   per-currency reconciliation
POST /v1/payouts/sweep                        on-demand timeout recovery
POST /v1/payouts/callbacks/mojaloop           now: X-Callback-Secret auth + batch routing
```
