# Phase 45 — client self-service consent center

GDPR Art. 7(3): *"the data subject shall have the right to withdraw his or
her consent at any time … It shall be as easy to withdraw as to give
consent."*

Phase 44 built the consent evidence ledger — the platform can now prove
consent to a regulator. Phase 45 hands the same ledger to the client: one
public page where they see every scope, the evidence trail behind it, and
flip what they want with one tap. Withdrawal is no longer scattered across
a STOP keyword, an unsub link and an ESP complaint — it has a front door.

## The consent/ token family

Same HMAC capability-token discipline as `track/`, `unsub/`, `reopt/`:

- `TripService.ConsentTokenFor(tenantID, clientID)` mints,
  `ParseConsentToken` verifies and **rejects every other family** — an
  unsubscribe or tracking link can never open the consent center, and a
  consent token opens nothing else.
- `tenantctx` exempts `/v1/consent/` — the token is the credential; the
  client's phone browser never carries a tenant header.

## Endpoints

| Route | Auth | Purpose |
| --- | --- | --- |
| `GET /v1/consent/{token}` | public (token) | SSR page: current scopes, evidence timeline, receipt link |
| `POST /v1/consent/{token}/set` | public (token) | one-tap scope flip (plain HTML form, no JS), 303 back to the page |
| `GET /v1/consent/{token}/receipt` | public (token) | downloadable JSON consent receipt (attachment) |
| `GET /v1/clients/{id}/consent-link` | tenant | console mints the link to paste into a conversation |

## Design decisions

- **The `service` scope is locked.** Booking confirmations and driver
  updates are the contract the client entered, not a marketing preference.
  The page shows it granted and locked with a plain-language explanation
  instead of offering a toggle that would silently break the service
  relationship. The copy points at erasure for ending the relationship.
- **Scope existence is validated against the client's own consent map** —
  a valid token cannot pollute the map with invented scopes, and locked or
  unknown scopes return 422.
- **Every self-service flip lands in the Phase 44 ledger** with source
  `self_service` and detail `self-service consent page`. Change-only
  recording is inherited from `SetConsentSourced`, so a double-click adds
  no row.
- **Erased clients get a read-only tombstone view**: the page explains the
  data is gone, shows the surviving (pseudonymous) evidence trail — the
  `erased` marker key is filtered out of scope rendering — and refuses
  further changes (409 via the erasure gate).
- **The consent receipt** (`ConsentReceipt`) is the Kantara-style proof
  artifact: receipt id, issued-at, controller (tenant) name, pseudonymous
  subject, current scopes, full history. JSON so a client or regulator can
  machine-check it.
- **Marketing email footers now carry both doors**: the mandated
  unsubscribe link *and* `Manage your preferences:` → the consent center
  (recorded in the send body like every footer — provenance shows exactly
  what was sent).

## Files

- `internal/model/model12.go` — `ConsentSourceSelfService` (+ valid set)
- `internal/domain/consent45.go` — token family, `ConsentCenterByToken`,
  `ConsentSetByToken`, `ConsentReceiptByToken`
- `internal/domain/outreach.go` — footer gains the manage-preferences link
- `internal/tenantctx/tenantctx.go` — `/v1/consent/` exemption
- `internal/server/phase45.go` — the four routes (SSR page, form toggle,
  receipt download, console mint)
- `web/console/src/pages/Clients.tsx` — "Copy client self-service link"

## Tests

- **5 domain**: token round-trip + cross-family rejection; view (locked
  service first, labels, genesis rows) + set/replay/locked/unknown/wrong-
  family; erased-client tombstone view + 409; receipt envelope + state +
  history; footer carries both links.
- **1 server**: end-to-end over HTTP — mint (tenant-scoped, cross-tenant
  404), public page render (no tenant header), locked-scope 422, toggle →
  303 → ledger row → page reflects state, receipt attachment, garbage +
  cross-family token 403.

No migration — the ledger (0031) already stores everything the page
needs.
