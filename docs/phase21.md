# Phase 21 — Templates, Operator Rosters, Reactions, Rotation Windows, Schema Versions

Five follow-ons from the Phase 14/15 waves, closed end to end.

## Outreach message templates (`domain/outreach.go`)

Tenants manage reusable message bodies (`POST/GET/DELETE
/v1/outreach/templates`), keyed by `(tenant, name)` — re-saving a name
updates in place, so the console save button is naturally idempotent.

- **Server-side rendering**: `{{client_name}}` / `{{tenant_name}}`
  substituted at send time; the recorded send carries the **rendered** text
  (provenance — the audit trail shows what was actually sent, not a template
  reference). Unknown placeholders are left visible, never silently dropped.
- **Purpose guard**: a template's purpose (`service` / `marketing` / `any`)
  is enforced at send — a marketing template cannot be sent as "service" to
  dodge the consent and frequency-cap gates.
- Console growth board: template manager card (create/list/delete) and a
  template picker in the composer that renders server-side.

## Per-tenant operator rosters

`Tenant.OperatorConfig.AlertPhones` (migration 0016, patched via
`PUT /v1/tenant`) overrides the platform-global `CE_OPERATOR_PHONE` for
SLA-relevant failure alerts (Phase 13 reply-failure push). Every roster
phone gets the alert; empty roster = global fallback. The receipt path stays
advisory — alert failures never block evidence recording.

## Message reactions (WhatsApp adapter)

Emoji reactions are normalized on **both** providers — Meta `type:
"reaction"` and Infobip `REACTION` (incl. UNREACT) — into bracketed
provenance lines (`[reaction: 👍]`, `[reaction removed]`) on the
conversation timeline. Reactions **skip case intake and the agent turn**: an
emoji acknowledgement is conversation context, not a request — it never
reaches triage and can never open a junk case.

## Webhook dual-secret rotation window

`POST /v1/webhooks/{id}/rotate-secret` no longer invalidates the old secret
instantly. For `CE_WEBHOOK_ROTATION_WINDOW_HOURS` (default 24) after
rotation, every delivery is signed with **both** secrets —
`X-Concierge-Signature: sha256=<new>,sha256=<prev>` — and receivers accept
any matching signature while they cut over (Stripe-style). After the window,
only the new secret signs. Latent pg bug fixed in passing:
`UpdateSubscription` never persisted secret columns, silently dropping every
rotation on Postgres.

## Event schema versioning

Webhook envelopes now stamp `"schema_version"` alongside `type`, sourced
from a registry (`CatalogVersion`) so a payload-breaking change bumps the
version in one place. `GET /v1/events/catalog` exposes `version` per kind
(all v1 today). Receivers pin parsing to a version instead of guessing
payload shape from the kind.

## Verification

- 5 new domain tests: template CRUD/upsert-in-place/render/validation,
  template send rendering + marketing-as-service refusal, roster alert
  fan-out + global fallback, dual-secret signing through the rotation window
  (1 → 2 → 1 signatures), catalog/envelope version parity.
- 4 new pg integration tests: template store + RLS, operator_config
  roundtrip, rotation-state persistence (the regression), and the
  `pgTenants.List` full-column scan fix (selected 14, scanned 10 — runtime
  failure on first call).
- 5 new adapter tests: Meta react/unreact, Infobip react/unreact, and the
  intake/agent skip-path proven by captured HTTP calls.
- `go build/vet` both tags, `go test ./...`, adapter 55, agent 26,
  analytics 16, console build — all green.
