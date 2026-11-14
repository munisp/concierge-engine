# Phase 15 — Registration/CRM-360, international payments, integration hardening

Audit verdict going in: **registration/CRM-360 — weak-moderate** (identity
resolution + consent-scoped encrypted preferences were solid, but consent had
no write API, no profile update, no search, no 360 view, no duplicate merge);
**international payments — weak-moderate** (single-PSP cards-only Stripe;
no Nigeria rails, no currency routing, no refund initiation, no currency
validation); **events/3rd-party — strong-moderate** (signed webhooks with
backoff existed, but parked deliveries were dead ends, no secret rotation,
no test event, no event catalog). This phase closes all of it.

## Registration & CRM-360

- **Consent write path** — `PUT /v1/clients/{id}/consent {scope, granted}`:
  the API surface Phase 14 outreach gating reads. Grant/revoke per scope,
  audit-logged, emits `client.consent_updated`. Verified end to end: consent
  grant un-gates marketing outreach, revoke re-gates it.
- **Profile enrichment** — `PATCH /v1/clients/{id}`: name, email, phone,
  locale (BCP-47), timezone (IANA), loyalty tier, VIP flags. New
  `locale`/`timezone` columns (migration 0010). Locale feeds Stripe Checkout
  localization; timezone feeds display/outreach timing.
- **Client search** — `GET /v1/clients?q=` substring over name/email/phone.
- **Client 360** — `GET /v1/clients/{id}/360`: identity + referral code,
  decrypted preferences, requests, bookings (resolved through requests),
  cases, conversations, first-touch attribution, lifetime booking spend,
  recent outreach. Every section best-effort — one store hiccup degrades a
  section, never the whole view.
- **Duplicate merge** — `POST /v1/clients/merge`: the same human arriving via
  WhatsApp and web capture produces two rows; merge re-points every
  client-scoped record (requests, cases, conversations, preferences,
  attribution, outreach — bookings follow via their request) in one
  transaction, deletes the loser, emits `client.merged`.

## International payments

- **Paystack adapter** (`paystack.go`, stdlib-only): `transaction/initialize`
  with market channels (NGN: card/bank_transfer/ussd; GHS/KES: mobile money),
  HMAC-SHA512 webhook verification, async refund. Disabled without
  `CE_PAYSTACK_KEY`; sandbox degrade mirrors Stripe.
- **PSP routing** — `choosePSP`: explicit `psp` override wins; NGN/GHS/KES/ZAR
  default to Paystack when configured; else Stripe; else the deterministic
  sandbox link. The intent records which rail carried it.
- **Refund initiation** — `POST /v1/payments/intents/{id}/refund`: only
  CAPTURED money refunds; Stripe refunds expand session→payment_intent,
  Paystack refunds by reference; state → REFUND_PENDING, replay-safe, and the
  PSP webhook settles the final state + ledger reversal.
- **Currency hygiene** — ISO-4217 shape validation on link creation;
  zero-decimal table (JPY/KRW/XOF/…) so minor-unit arithmetic stays correct;
  `GET /v1/payments/methods?currency=` answers "which rails can this customer
  actually pay with?" with live/sandbox flags.
- **Localized checkout** — the client profile's locale + email flow into the
  Stripe Checkout Session (`locale`, `customer_email`).

## Events & 3rd-party integration

- **Event catalog** — `GET /v1/events/catalog`: every emitted event kind with
  aggregate, description, payload keys. The integrator contract for webhook
  subscriptions (exact / `prefix.*` / `*` matching unchanged).
- **Delivery replay** — `POST /v1/webhooks/deliveries/{id}/replay`: resets
  attempts/schedule on parked deliveries; the dead-end is gone.
- **Secret rotation** — `POST /v1/webhooks/{id}/rotate-secret`: new secret
  returned once, old one invalid immediately, audit + `webhook.rotated`.
- **Test event** — `POST /v1/webhooks/{id}/test`: enqueues a synthetic
  `webhook.test` delivery for end-to-end endpoint/signature verification.
- Console Integrations page: rotate/test/replay buttons + live catalog.

## Console

New **Clients** page: search → 360 view (profile editor, consent toggles,
first touch + spend, requests/bookings/cases/conversations/preferences/
outreach sections), merge tool.

## Verification

- `go build` / `go vet` clean on both tag variants; full Go suite green
  including 10 new domain tests: profile update + roundtrip, consent↔outreach
  gate loop, search + merge re-point, 360 assembly, PSP routing matrix
  (NGN→paystack / EUR→stripe / explicit override / sandbox degrade), Paystack
  end-to-end (initialize → signed webhook → capture → ledger splits; forged
  signature rejected), refund flow (captured-only, replay-safe), methods
  discovery + currency validation, webhook replay/rotate/test, catalog
  integrity.
- pg integration (`pg_integration_phase15_test.go`, CI pg job): locale
  roundtrip, ILIKE search + RLS isolation, merge re-point + delete,
  bookings-through-requests join, webhook delivery lookup.
- Console `npm run build` — clean.
