# Phase 16 — EU/US-First Readiness

Go-to-market correction: the platform launches in **Europe and the USA before
Nigeria**. Phase 16 hardens what that ordering demands: market payment rails
(Stripe local methods, not card-only), GDPR/CCPA data-subject rights, and
regional consent defaults at registration. Paystack remains for the later
Africa wave.

## Payments — market rails (Stripe, EU/US-first)

- `stripeChannelsFor(currency)` maps launch-market currencies to the Checkout
  `payment_method_types` that convert locally:
  - **EUR**: card, SEPA debit, iDEAL, Bancontact, giropay, Sofort, Klarna
  - **USD**: card, ACH (us_bank_account), Cash App, Klarna, Link
  - **GBP**: card, Bacs debit, Klarna · **CHF**: card, TWINT, Klarna
  - **CAD**: card, ACSS debit · **AUD**: card, BECS debit
  - **SEK/NOK/DKK**: card, Klarna · **PLN**: card, BLIK, Przelewy24
  - unknown currencies: card (universally supported)
- `CreateCheckoutSession` forwards the rails; SCA/3-D Secure 2 for EU cards is
  enforced by Stripe Checkout itself (hosted page — PCI SAQ-A unchanged).
- `GET /v1/payments/methods?currency=EUR` now answers with the real local
  rails per currency instead of a static list.
- PSP routing unchanged in shape: NGN/GHS/KES/ZAR → Paystack when configured;
  everything else → Stripe; sandbox fallback otherwise.

## Data rights (GDPR Art. 15/17, CCPA)

- **Access** — `GET /v1/clients/{id}/export`: one machine-readable JSON
  document with the client record, decrypted preference memory, requests,
  bookings, cases, conversations, attribution events and outreach history.
  Audit-logged with section counts; emits `dsar.exported`.
- **Erasure** — `POST /v1/clients/{id}/erase`: GDPR Art. 17 with the
  accounting carve-out. Identity is scrubbed to a pseudonymous tombstone
  (`Erased client` / `erased-<id>@erased.invalid` / no phone / no locale),
  preference memory is deleted (DSAR cascade), all consent revoked, and
  `erased_at` is set. Bookings, payment intents and the ledger stay attached
  to the pseudonymous id — erasure removes identity, not accounting truth.
  Idempotent; emits `client.erased`.
- **Write-surface gating** — an erased client refuses further processing
  everywhere: `UpdateProfile`, `SetConsent`, `SavePreference` return 409;
  outreach verdict blocks **all** purposes (sends recorded `held` with
  reason `client erased (GDPR/CCPA)` — the refusal is auditable); merging
  INTO an erased record is refused (an erased loser can still be merged
  into a live winner).
- Re-registration after erasure works correctly: tombstoned identifiers never
  match, so the same phone resolves as a brand-new client.
- Migration `0011_client_erasure.sql`: `clients.erased_at timestamptz NULL`
  + partial index.

## Regional consent defaults

`ConsentDefaults(phone)` at registration (progressive, `resolve_client`):
- EU-27 + EEA + UK + Switzerland calling codes → **opt-in** regime:
  `marketing: false`, `service/data_processing: true`.
- US/Canada (+1) → **opt-out** regime: `marketing: true`.
- Unknown/empty → conservative opt-in (`marketing: false`).

Jurisdiction is derived from the phone country calling code — the most
reliable signal available at WhatsApp/capture registration. Documented as a
heuristic; operators correct per-client via `PUT /v1/clients/{id}/consent`.

## Registration context

- `POST /v1/clients/resolve` accepts `locale`/`timezone`; new clients store
  them, existing clients are backfilled when empty.
- The capture page sends `navigator.language` and the IANA timezone
  automatically — Stripe checkout opens localized on first contact.

## Console

Clients 360 page: **Data rights** section (export JSON download, erase with
explicit confirm), erased banner, all edit surfaces remain available but the
API refuses writes on erased records.

## Verification

- `go build` + `go vet` (both tag sets), full Go suite green — 5 new Phase 16
  domain tests (consent matrix, export, erasure gating incl. outreach refusal
  and re-registration, merge-into-erased refusal, market rails, methods
  discovery).
- pg integration: `TestPGClientErasureRoundtrip` (NULL roundtrip, tombstone
  persistence, booking retention through the carve-out).
- whatsapp-adapter 50 passed, analytics 16 passed, console production build.
