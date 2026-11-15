# Phase 39 — Email outreach channel

Phase 38 made bought data lawful to *hold*; Phase 39 makes it lawful to
*use*. B2B contact data is email-first — the corporate travel buyers,
EAs and PAs on a Cognism/Apollo export have no WhatsApp relationship with
the tenant, and Phase 14's outreach engine was WhatsApp-only (Phase 37's
standing lesson: "email-only leads not WhatsApp-nurturable"). This phase
adds the email channel end to end, with the compliance envelope enforced
by the platform rather than by operator discipline.

## What shipped

**Core-api (Go)**

- `SendOutreachInput.Channel` — `whatsapp` (default, unchanged) | `email`.
  Unknown channels are 422. Email sends require a subject and a client
  email on file; the recorded `OutreachSend` persists both (`subject`
  column, migration 0027) so the hourly sweep can deliver scheduled email
  without losing it.
- **Per-channel frequency caps.** The 3-per-7-day marketing cap now counts
  per channel — a WhatsApp message and an email are different intrusions
  and deplete separate budgets. WhatsApp behavior is unchanged.
- **B2B-introduction lane (email only).** A client with no marketing
  consent who is linked to an OPEN lead with `contact_basis =
  b2b_legitimate_interest` (Phase 38) may receive at most
  `EmailIntroMaxTouches = 2` marketing emails. Touch count is derived from
  the sends ledger — no separate counter that could drift. The verdict
  exposes `intro_lane`/`intro_touches`/`intro_max` so the console can show
  the lane state, and every intro send's detail records "b2b introduction
  touch N of 2". After the cap the lane closes until consent is earned on
  a reply (the Phase 38 repeat-touch grant). WhatsApp is never opened by
  this exception.
- **Mandated unsubscribe footer.** Every marketing email's *recorded* body
  carries the tenant identity line and a one-click unsubscribe URL —
  server-side, so no template or operator can omit it, and provenance
  shows exactly what left the platform. Service emails carry no footer.
- **`unsub/` HMAC token family** (same discipline as `track/`/`quote/`):
  `GET /v1/unsub/{token}` is tenant-header-exempt (the token is the
  credential — a forwarded email must still unsubscribe the original
  recipient), flips marketing consent off on the shared consent spine,
  emits `email.unsubscribed` + an attribution event, and is idempotent
  ("already unsubscribed" on replay). Cross-family tokens are rejected.
- Broadcasts gain `channel` + `subject`; email sends get channel-suffixed
  idempotency keys (`bcast:{campaign}:{client}:email`) so the same campaign
  can run on both channels without one replay-swallowing the other.
- `CE_EMAIL_ADAPTER_URL` wires the delivery adapter; empty = record-only
  honest degrade, detail naming the missing variable.

**email-adapter (Python, `ai/email-adapter`)**

- `POST /v1/send` — builds the MIME message: From/To/Subject/Message-ID,
  `List-Unsubscribe` + `List-Unsubscribe-Post: List-Unsubscribe=One-Click`
  (RFC 2369/8058), plain-text body. Delivers via SMTP (starttls/ssl/none);
  without `CE_SMTP_HOST` it runs sandbox (record-only outbox, mode
  "sandbox") — never faked as live. SMTP failures return `held` with the
  exception class, which core-api records.
- **Tracking on the adapter's own public endpoints** (enabled when
  `CE_EMAIL_PUBLIC_BASE` + `CE_EMAIL_TRACKING_SECRET` are set):
  `GET /t/o/{token}` serves a 1×1 GIF and forwards `email_opened` to the
  attribution spine; `GET /t/c/{token}?u=` forwards `email_clicked` and
  302-redirects (non-http targets refused). Tokens are stateless HMAC —
  kind/tenant/client/send-ref — and tamper or cross-kind use is 403.
- `POST /webhooks/email/events` — generic ESP events: delivered/bounced
  forward to the Phase 11 receipt spine; **complained flips marketing
  consent off** (a complaint is an opt-out, not a statistic); opened/clicked
  forward attribution for ESP-native tracking.
- `POST /webhooks/email/inbound` — normalized inbound (SES/Mailgun/Postmark
  parse): the *receiving address* resolves the tenant
  (`CE_EMAIL_TENANT_MAP`), then the same resolve → conversation (channel
  EMAIL) → intake (`email:{message-id}` idempotent) → agent turn loop as
  the WhatsApp adapter, with the agent's reply delivered back by email
  (`Re:` subject threading).

**Console** — Growth broadcast form gains a channel selector and a required
subject field for email, with the governance rules stated inline; the
sends feed shows ✉ + subject for email and the table has a channel column.

## The F1 loop, completed

Phase 37's simulation ended at WhatsApp-only nurture. With Phase 38 + 39 a
zero-customer tenant can: import a Cognism list (basis auto-stamped
`b2b_legitimate_interest`, consent stripped) → run one compliant
introduction campaign per contact (max 2 touches, unsubscribe mandated) →
replies thread into EMAIL conversations and the agent answers them → the
Phase 38 repeat-touch grant earns consent at first reply → the contact
graduates to the full marketing lane on both channels → funnel attributes
opens, clicks, conversions back to the campaign.

## Verification

- 6 domain tests (validation/degrade, footer+delivery, per-channel cap,
  intro lane incl. closure and self-reported exclusion, unsubscribe token
  flow incl. tamper/cross-family, broadcast channel keys) + 2 server tests
  (HTTP send + public unsubscribe) + 1 pg test (subject roundtrip, replay
  conflict) + 13 hermetic adapter tests.
- `go vet ./...` and `go vet -tags pg ./...`, full go suite, pg suite on a
  real local Postgres (incl. migration 0027), adapter pytest, console
  production build — all green before push.
