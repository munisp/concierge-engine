# Phase 68 — partner-portal folio visibility

Phase 57 gave partners a magic-link portal (HMAC token, no login) for
offers and commissions. Phases 66–67 turned desk billing into statements
with collection evidence, but the partner only ever heard about a
statement over WhatsApp. Phase 68 puts the statements in the portal.

## What the partner sees

`GET /v1/portal/{token}` now carries `folios` — the partner's own
bill-to-desk statements, newest period first:

| field | meaning |
|---|---|
| `state` | `OPEN` (still accruing) · `ISSUED` (payable) · `SETTLED` (paid) |
| `total_minor`, `currency`, `lines` | the frozen statement and its booking lines |
| `issued_at` / `settled_at` | present only when reached (zero-time safe pointers) |
| `due_at` | ISSUED only — **derived** from the tenant's payment terms, never stored |
| `overdue_days` | ISSUED only — whole days past due; ≤ 0 means inside terms |
| `reminders` | the verbatim dunning ledger (Phase 67), statuses included |

Because `due_at`/`overdue_days` are computed from the same
`pricing_rules.folio_terms_days` that aging and dunning use, the portal
can never disagree with the operator's receivables view.

## Discipline

- **Best-effort**: a folio listing failure never locks the partner out
  of their offers and money (same as Phase 63 sourced requests).
- **Suspension still revokes everything**: status is checked on every
  portal request, so a suspended partner's link dies immediately.
- **No new routes, no migration** — the portal payload grew; the token
  contract is unchanged.
- Guest names on lines are safe: the partner sourced those guests.

## Files

- `services/core-api/internal/domain/folios68.go` — `PortalFolio` view +
  `PortalFolios` builder.
- `services/core-api/internal/domain/partner57.go` — `Folios` field on
  `PartnerPortalView`, best-effort wiring.
- `web/console/src/pages/PartnerPortal.tsx` — "Statements" section with
  expandable lines and reminder history.
- `services/core-api/internal/server/phase68_test.go` — HTTP round-trip:
  accruing → issued (derived due = issued + 14d) → settled; no-folio
  partner unchanged; suspended → 403.
