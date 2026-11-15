# Phase 42 — WhatsApp list hygiene: unreachable-number hold + reply-reset sunset

Phase 41 hardened the email lane (suppression + sunsetting). WhatsApp is the
platform's primary lane — and it had no equivalent: a dead number kept
receiving marketing sends, and Meta's quality rating punishes exactly that
the way mailbox providers punish bounces, up to restricting the tenant's
business number. Phase 42 brings the WA lane to parity.

## Signals (attribution spine, no new tables)

- `wa_failed` / `wa_delivered` / `wa_read` — tracked when a WhatsApp
  receipt's `external_ref` (wamid) resolves to an outreach send via the new
  `OutreachStore.SendByExternalRef` (iface + memory + pg; pg backed by
  `idx_outreach_sends_ref`, migration 0030). Receipts for non-outreach
  messages (case replies, operator alerts) resolve to nothing and record no
  event. Email receipts are skipped — email outcomes already arrive via
  `esp_events` with their own kinds.
- `client_replied` — tracked when a `CLIENT`-author message lands on a
  conversation (`ConversationService.AppendMessage`), carrying the
  conversation's channel and client. WhatsApp has no open pixel; a reply is
  THE engagement proof. Advisory: a lookup/track failure loses a signal,
  never the message.

## Verdict rules (marketing + whatsapp, after the frequency cap)

- **Unreachable hold** — `WaUnreachableMinFailures = 3`: three or more
  failed WA deliveries with no `wa_delivered`/`wa_read` more recent than the
  latest failure → held (`wa_unreachable: true`), "verify the client's
  number". One confirmed delivery after the failures clears it.
- **Engagement sunset** — `WaSunsetMinSends = 8`: eight or more all-time
  marketing WA sends with zero `client_replied` (channel whatsapp) ever →
  held. One reply, of any age, resets it permanently.

Both are advisory (store errors fail open), marketing-lane only — service
messages are owed regardless — and WhatsApp-only: email keeps its Phase 41
suppression/sunset, each lane judged by its own signal.

## Console

The Growth verdict line shows `WA number unreachable` alongside the Phase 41
`address suppressed` chip; sunset holds surface through the reason text as
before.

## Tests

- domain: 3 failed receipts (distinct wamids via a counting stub) hold
  marketing WA / adapter not called / service exempt / a delivered receipt
  after the failures clears / unrelated receipts harmless; sunset at 8
  sends, one conversation reply resets, service + email exempt.
- server: verdict endpoint exposes `wa_unreachable` over HTTP; unrelated
  receipt 201; reply via `POST /v1/conversations/{id}/messages` resets the
  sunset over HTTP.
- pg: `SendByExternalRef` round-trip, unknown ref → ErrNotFound, RLS tenant
  isolation.
