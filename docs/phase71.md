# Phase 71 — Statement delivery

A statement the partner never receives is a statement that never gets
paid. Phase 67's dunning chases, but the chase pointed at the portal —
the partner still had to find the document. Phase 71 delivers the
statement itself.

## What ships

- **`stmt/` HMAC capability token** — same discipline as `track/`,
  `consent/`, `unsub/`: the token IS the credential (no tenant header;
  tenantctx exempts `/v1/statement/`), and cross-family tokens are
  rejected — a trip link can never open a financial statement.
- **Public printable statement page** — `GET /v1/statement/{token}`
  renders the folio as the partner-facing document: from/to, period,
  issued/due dates (due = issued + tenant terms, derived on read — the
  document can never disagree with the operator's aging), charges,
  payments received, totals (total / paid / outstanding), and a PAID /
  DUE / ACCRUING banner. The page is print-optimized — the partner
  saves it as PDF from the browser (print-to-PDF, honest: no fake
  "PDF generation"). A saved link keeps resolving after settlement,
  marked PAID.
- **Delivery over the partner's best channel** —
  `POST /v1/folios/{id}/send-statement`: WhatsApp when a phone is on
  file, otherwise the new **partner billing email** (`partners.email`,
  migration 0047 — the address a hotel desk actually reads), otherwise
  a verbatim `skipped:no-contact`. Adapter absent → `skipped:no-adapter`.
  ISSUED-only (409): an OPEN folio is still accruing — delivering it
  would freeze a moving total in the partner's inbox; a SETTLED folio
  needs no collection document.
- **Deliveries ledger** — every send (or honest skip/failure) lands on
  the folio's own `deliveries` jsonb ledger (migration 0047) next to
  the reminders and payments ledgers: the statement carries its own
  delivery evidence. Events `channel.folio_statement_sent` /
  `channel.folio_statement_skipped`; every outcome audited.
- **Console** — Partners folio card: **Statement** button (delivery
  count), **Link** (copy the public statement URL), delivery history
  rows in the expanded view; partner form gains the billing-email
  field.

## Honesty properties

- "Sent" only when the channel adapter accepted the message; every
  other outcome is recorded verbatim (`skipped:no-contact`,
  `skipped:no-adapter`, `failed:…`) — never faked.
- Due/overdue on the document are derived from tenant terms on read —
  the statement, the portal, and the aging report can never disagree.
- Email subject/body quote the **outstanding** balance (Phase 70
  derivation), never just the face value.

## Files

- `services/core-api/internal/model/model22.go` — `FolioDelivery`
  (edited in place; folio-ledger siblings stay together).
- `services/core-api/internal/model/model20.go` — `Folio.Deliveries`.
- `services/core-api/internal/model/model2.go` — `Partner.Email`.
- `db/migrations/0047_phase71.sql` — `partner_folios.deliveries`,
  `partners.email`.
- `services/core-api/internal/store/memory24.go`, `postgres26.go` —
  deliveries ledger persistence (edited in place; memory25/postgres27
  slots stay free). `postgres2.go` — partner email persistence.
- `services/core-api/internal/domain/folios71.go` — token family,
  `StatementView`/`StatementByToken`, `SendStatement`,
  `StatementURL`.
- `services/core-api/internal/domain/domain.go` — `Services.emailURL`
  (the Phase 39 adapter, reused).
- `services/core-api/internal/server/phase71.go` — the three routes +
  the SSR page; `tenantctx` exemption; `server.go` registration.
- `services/core-api/internal/domain/eventcatalog.go` — the two
  delivery events.
- Tests: `domain/phase71_test.go` (6 tests), `server/phase71_test.go`
  (full HTTP flow), `store/pg_integration_phase71_test.go` (jsonb +
  email round-trips, RLS).
- `web/console/src/pages/Partners.tsx` — Statement/Link actions,
  delivery history, billing-email field.
