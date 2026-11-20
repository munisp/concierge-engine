# Phase 72 — dunning over email

Phase 67's escalating reminder cadence only spoke WhatsApp — a
phoneless partner (a hotel desk whose billing contact is an inbox, not
a handset) was silently unreachable: every rung recorded
`skipped:no-contact` and the debt aged untouched. Phase 71 gave the
partner a billing email (`partners.email`); this phase lets the
dunning engine use it.

## What ships

- **Channel-picked dunning** — `sendReminder` now picks the partner's
  best lane: WhatsApp when a phone is on file (Phase 67 behavior
  unchanged), the billing email otherwise. Only a partner with
  neither records `skipped:no-contact`, with `channel: "none"`.
- **The email rung** (`sendReminderEmail`, folios72.go) carries the
  same facts as the WhatsApp rung — the outstanding balance (Phase 70
  derivation, never just the face value), days past due against
  tenant terms — plus the printable statement document link
  (Phase 71), the page an accounts inbox actually needs. Subject
  names the rung, period, amount and days overdue.
- **Verbatim ledger, unchanged discipline** — the channel lands on
  every reminder row (`whatsapp` | `email` | `none`), so the
  statement says exactly how each touch went out; cadence stays
  derived from the ledger, one rung per sweep, 24h manual-nudge
  guard, ISSUED-only. No migration (email column landed in 0047).

## Honesty properties

- "Sent" only when the email adapter accepted the message; no adapter
  configured records `skipped:no-adapter` with the email channel
  named — the operator sees the lane that was attempted, never a
  faked delivery.
- WhatsApp behavior is byte-identical for partners with phones
  (Phase 67/70 tests unchanged and passing).

## Files

- `services/core-api/internal/domain/folios67.go` — channel picking
  in `sendReminder` (edited in place).
- `services/core-api/internal/domain/folios72.go` — the email rung.
- `services/core-api/internal/domain/phase72_test.go` — email-lane
  sweep, verbatim payload, both honest skips.
