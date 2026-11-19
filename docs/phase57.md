# Phase 57 — Partner PWA + WhatsApp Offers

Phase 56 gave partners the API; Phase 57 gives them the product. Small tour
operators don't install apps and don't have developers — a magic link on
their phone and a WhatsApp message are the whole interface.

## 1. Partner portal (PWA)

`PartnerTokenFor(tenantID, partnerID)` mints an HMAC-signed magic link — the
same stateless token discipline as chauffeur trip links and client tracking,
with a distinct `partner/` payload prefix so tokens are never
cross-accepted: the chauffeur parser rejects partner tokens and vice versa
(tested both directions, plus tamper rejection).

- `GET /v1/portal/{token}` — portal payload: partner identity, assignments
  (offers first), commission settlement totals
- `POST /v1/portal/{token}/respond` — `{assignment_id, action, note}` into
  the Phase 56 respond flow (state machine, 404/409 semantics intact)
- Console page `/partner` — zero-install PWA: pending offers with
  one-tap **Accept / Decline**, assignment history, money summary
  (pending / settled / in flight)

Tokens are stateless and non-expiring; **revocation is operational** —
suspending the partner closes every outstanding link, because the portal
view checks partner status on every request (tested).

## 2. WhatsApp offers

Partners gain a `phone` (E.164) field (migration 0037). On a partner-routed
assignment, `OfferedOnAssign` nudges the partner's WhatsApp with the portal
link — detached from the request path (`context.WithoutCancel` + timeout +
recover; a channel blip never takes the booking API down, failures are
audit-logged). Manual re-nudge: `POST /v1/partners/{id}/notify`.

Honest degrade, always: no phone → `skipped:no-phone`; no channel adapter
→ `skipped:no-adapter`; adapter error → `failed:<detail>`. The response
never claims "sent" without the adapter confirming.

## 3. Console

Partners page: WhatsApp number on the create form, commission rate column,
and per-partner **Copy portal link** / **Send WhatsApp offer** actions in
the integration card.

## Endpoints

| Route | Auth |
|---|---|
| `GET /v1/portal/{token}` | portal token (no tenant header) |
| `POST /v1/portal/{token}/respond` | portal token |
| `GET /v1/partners/{id}/portal-link` | tenant |
| `POST /v1/partners/{id}/notify` | tenant |

`/v1/portal/` joins the token-authenticated prefix family in the tenant
middleware (`/v1/trips/`, `/v1/track/`, …).

## Tests

- domain (3): token round-trip + cross-surface + tamper rejection; portal
  view incl. suspended-partner lockout; WhatsApp degrade honesty.
- server (1): full flow — link fetch → token extract → portal view →
  respond → double-respond 409 → bogus/chauffeur token 403 → notify skip.
- pg integration (1, CI): `partners.phone` round-trip.
