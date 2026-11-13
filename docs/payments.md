# Payments & ledger (Master Spec §11)

## Model

- **Platform-of-record**: the platform contracts with PSPs (Stripe first) and
  holds sub-merchant accounts per tenant; BYO-PSP mode for flagships.
- **PCI SAQ-A posture**: hosted payment links/fields only — no PAN ever touches
  platform servers (chat included).
- **TigerBeetle** holds money truth: client balances, deposits, pre-auths,
  partner payables, platform fees. Postgres mirrors the reconciled projection
  for queries/dashboards. Temporal workflows reconcile PSP settlement reports
  against clearing accounts nightly; mismatches open ops tickets.

## Settlement walkthrough (implemented)

1. Client pays hosted link (deposit or full).
2. PSP webhook -> `core-api` -> intent `CAPTURED`.
3. Split settlement posts (idempotent, deterministic transfer ids):
   - debit `CLIENT_CLEARING` -> credit `TENANT_REVENUE` (net)
   - debit `CLIENT_CLEARING` -> credit `PLATFORM_FEES` (take rate 2–5%)
4. Refunds post the mirror-image transfer. `TIER-3` — human-only via policy engine.

## TigerBeetle client crate

`crates/ledger-adapter` speaks HTTP/JSON to core-api. The production backend
pins the TigerBeetle client crate and maps (tenant, ledger code, currency) to
deterministic 128-bit account ids; idempotency keys map to transfer ids for
exactly-once semantics. The dev journal (`journal.rs`) implements the same
contract for local dev/tests.

## Mojaloop (Phase 2+)

Un-defer triggers are in the deferred register. The adapter interface is
PSP/Mojaloop-agnostic from day one — un-deferring is an adapter project, not
a ledger migration: partner payout via FSPIOP quote -> accept -> fulfill,
posting through `IN_FLIGHT_SETTLEMENT` with compensating workflows on timeout.
