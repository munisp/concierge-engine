# Phase 67 — folio aging & dunning

Phase 66 made partner money owed a first-class statement; Phase 67 makes
it **collected**. An ISSUED bill-to-desk folio is a receivable: it ages
against the tenant's payment terms, and the platform duns the partner on
an escalating cadence until it settles.

## Design

### Payment terms

`tenants.pricing_rules.folio_terms_days` (jsonb — no tenants migration).
`0`/unset = the 14-day platform default; negative never applies
(`PricingRules.FolioTerms()`). Due date = `issued_at + terms`.

### Aging — derived, never stored

`GET /v1/folios/aging` computes the receivables view on read from the
folio ledger: every ISSUED (unpaid) folio grouped per **partner ×
currency**, bucketed by whole days past due:

| bucket      | meaning                    |
|-------------|----------------------------|
| `current`   | inside terms               |
| `1_7`       | 1–7 days overdue           |
| `8_30`      | 8–30 days overdue          |
| `30_plus`   | 30+ days overdue           |

Rows carry per-bucket minor totals, the grand total, open-folio count,
and `oldest_overdue_days` — the sort key (most-overdue first = the next
collection call). Currencies are never blended (same rule as the channel
margin analytics). There is no aging table that could disagree with the
statements.

### Dunning — the folio keeps its own evidence

`partner_folios.reminders` (migration 0045, jsonb) is the dunning ledger.
Every touch is a `FolioReminder{n, channel, status, created_at}` where
status is verbatim: `sent` (the adapter accepted it), `skipped:no-contact`,
`skipped:no-adapter`, or `failed:…`. A reminder is never marked sent that
was not.

The cadence is **derived** from the ledger — the next rung is
`len(reminders)+1` over the ladder **1 / 7 / 14 / 30 days past due**;
a rung fires when the folio is at least that many days overdue:

- the **hourly sweeper** (`main.go`, per tenant) and
  `POST /v1/dunning/run` send at most **one rung per folio per sweep** —
  a long-overdue folio catches up over successive sweeps instead of
  bursting four messages in a minute;
- `POST /v1/folios/{id}/remind` is the operator's manual nudge, guarded
  to **one touch per 24h** across sweep and manual sends;
- only ISSUED folios are dunned — OPEN is not yet a debt, SETTLED leaves
  the working set (`ListIssued`) and the aging view.

The message names the period, the exact amount and currency, the issue
date, days past due, the terms, and links the partner portal. Delivery is
WhatsApp to the partner's phone; no phone or no adapter records the
honest skip. Events: `channel.folio_reminder_sent` /
`channel.folio_reminder_skipped` (status in payload), plus audit.

### Console

Partners → folio card: ISSUED rows show their age in days, a **Remind**
button with the touch count, the reminder history inline with the lines
(status verbatim), and Settle.

## API

| method | path | notes |
|--------|------|-------|
| GET  | `/v1/folios/aging`         | derived receivables report |
| POST | `/v1/dunning/run`          | on-demand sweep → `{sent, skipped}` |
| POST | `/v1/folios/{id}/remind`   | manual nudge (24h guard; 409 otherwise) |

## Files

- model: `model21.go` (FolioReminder, cadence, aging types); `model.go`
  (`PricingRules.FolioTermsDays` + `FolioTerms()`); `model20.go`
  (`Folio.Reminders`)
- store: `memory24.go` + `postgres26.go` edited in place (the Phase 66
  folio store evolves: reminders in copy/scan/Update, new `ListIssued`),
  `store.go` interface — slots `memory25.go`/`postgres27.go` stay free
- domain: `folios67.go` (Aging / RunDunning / RemindNow / sendReminder)
- server: `phase67.go`; sweep wired in `cmd/core-api/main.go`
- migration: `db/migrations/0045_phase67.sql`
- console: `web/console/src/pages/Partners.tsx`
- tests: `domain/phase67_test.go`, `server/phase67_test.go`,
  `store/pg_integration_phase67_test.go`

## Honesty properties

- Aging is computed from the statements themselves — no derived table to
  drift.
- The reminders ledger records the truth of every touch, including
  skips; cadence derives from it.
- A settled folio is never dunned and disappears from aging.
- One rung per sweep; one manual touch per 24h — the partner is never
  spammed.
