# Phase 41 — email list hygiene: hard-bounce suppression + engagement sunsetting

Phase 40's deliverability guard pauses a tenant whose sender reputation is
burning, but nothing stopped re-mailing a known-dead address — the single
biggest bounce driver. Phase 41 closes that: the ESP already reports which
address bounced; the platform now suppresses it permanently, and clients who
never open anything stop receiving marketing email before they become
complaints.

## Suppression list (`email_suppressions`, migration 0029)

One row per (tenant, normalized email) — lowercase, trimmed. Reasons:
`hard_bounce` (ESP webhook), `complaint`, `manual` (operator block). FORCE
RLS + `tenant_isolation` policy; idempotency rides `UNIQUE (tenant_id,
email)` (`ON CONFLICT DO NOTHING RETURNING id` → no row → conflict → the
domain returns the existing suppression, so ESP webhook retries are safe).

The verdict checks the list for `channel == email` when the client carries an
email address, after the erasure check and **before the purpose branch** —
suppression holds BOTH lanes deliberately: a dead address delivers nothing,
service email included. The refusal is auditable (`held`, with
`email_suppressed: true` on the verdict). Store errors fail open; consent,
cap, and erasure remain the authoritative refusals.

```
POST   /v1/email/suppressions            {email, reason, source_ref?} → 201;
                                           replay (same address) → 200 + original row
GET    /v1/email/suppressions            → {suppressions: [...]}
DELETE /v1/email/suppressions/{email}    operator recovery → 200; missing → 404
```

The email adapter's bounce branch now POSTs the suppression (reason
`hard_bounce`, source_ref = the ESP message ref) alongside the Phase 40
`email_bounced` attribution — the address is out of rotation from the first
bounce, so the 7-day guard's bounce window stops accumulating and the guard
stays inert.

## Engagement sunset (marketing email only)

`EmailSunsetMinSends = 8`: a client sent 8 or more all-time marketing emails
with zero recorded `email_opened`/`email_clicked` attribution events is held
out of the marketing lane — chronic non-openers are tomorrow's spam
complaints, and every send to them dilutes the engagement signals mailbox
providers use to score the tenant. A single open or click, of any age,
resets the sunset permanently. Service-lane email is exempt (invoices and
reminders are owed regardless of marketing engagement); WhatsApp is
untouched. Advisory: store failures fail open.

Order in the verdict: erasure → suppression → consent set → service
early-return → per-channel frequency cap → Phase 40 tenant guard → **sunset**
→ consent gate → intro lane → deny.

## Console

Growth gains an "Email suppressions" card: the live list (address, reason,
ESP source ref, date), a manual-add form (address + reason), and a remove
button per row. The outreach verdict line shows `address suppressed` when
the held reason is the list, next to the Phase 40 bounce-rate readout.

## Tests

- domain: suppression holds both lanes / adapter never called / replay
  returns the original / unsuppress reopens / validation; sunset at 8 sends,
  open resets it, service + WhatsApp exempt.
- server: 201 create, 200 replay, list, delete, 404 re-delete, 422
  validation; send to a suppressed client held over HTTP; WA unaffected.
- pg: RLS round-trip, UNIQUE replay → conflict, list order, tenant
  isolation, delete → 404 on missing.
- adapter: bounce event fires the suppression POST with the recipient and
  message ref.
