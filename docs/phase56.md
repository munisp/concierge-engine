# Phase 56 — Partner Integration Layer

Tour operators and DMCs (Mr Travel, The Little Orange, Younique Tailor Tours)
now integrate **programmatically** — no console access, no tenant-wide
credentials. Four surfaces, all riding existing machinery:

## 1. Partner-scoped API keys

`POST /v1/partners/{id}/keys` mints a `cek_…` key bound to one partner
(scope `partner`, `partner_id` on the key record; secret shown once, hash
persisted, constant-time compare — same discipline as tenant keys).

The tenant middleware enforces the boundary:

- partner key → **only** `/v1/partner/*` — anything else is 403
- partner key without the `partner` scope → 403
- tenant header / tenant-wide key on `/v1/partner/*` → 403 (the partner
  surface is partner-only, so credentials can't be confused)

## 2. Partner portal API

| Endpoint | Purpose |
|---|---|
| `GET /v1/partner/assignments` | the partner's offered/in-flight assignments |
| `POST /v1/partner/assignments/{id}/respond` | `{"action": "accept"\|"decline", "note"}` |
| `GET /v1/partner/settlement` | commission payouts by saga state + totals |

Responses ride the existing trip state machine (`ASSIGNED →
PARTNER_ACCEPTED | PARTNER_DECLINED`), so console, SSE and conflict gating
see partner answers with zero new wiring. `PARTNER_DECLINED` is terminal —
the slot frees for reassignment. A cross-partner respond attempt is
indistinguishable from unknown (404) — partners never learn each other's
assignment IDs. Double response → 409.

## 3. Commission accrual into the payout saga

`Partner.CommissionPct` (0–50%, validated, audit-logged). Assigning a
partner-routed booking accrues `quote.total × pct` as a **PENDING payout**
— the same saga (Phase 2) and settlement batching (Phase 48) that already
pay partners, not a parallel money path. Idempotent per booking+partner
(`commission:{booking}:{partner}`), so re-assigns never double-accrue. A
decline voids the still-pending payout (`FAILED`); money already quoted or
in flight is left for ops to compensate — the saga never silently rewrites
money in motion. Partners without a rate accrue nothing.

## 4. Partner webhook feeds

`POST /v1/partners/{id}/webhooks` creates a subscription with `partner_id`
set. The fan-out filter delivers an event to a partner feed only when the
payload's `partner_id` matches; tenant-wide subscriptions keep receiving
everything. Same signing (`X-Concierge-Signature`, dual-secret rotation),
retry-with-backoff, and schema versioning as tenant webhooks. New catalog
kinds: `assignment.partner_offered`, `assignment.partner_responded`,
`partner.commission_accrued`, `partner.commission_cancelled`.

## 5. Embeddable lead capture

The public `/site/{slug}/leads` capture accepts `?source=partner:{name}` —
only the `partner:` prefix survives (anything else collapses to `site`), so
partners get attribution without being able to claim arbitrary sources. The
console renders a copy-paste `fetch()` snippet per partner
(`GET /v1/partners/{id}/integration` also returns commission, key prefixes
— never secrets — webhook feeds, and the capture URL).

## Console

Partners page gains an **Integration** card: commission editor, key minting
(secret-once banner), webhook feed registration, and the capture snippet.
The partner table shows the commission rate.

## Migration

`db/migrations/0036_phase56.sql`: `partners.commission_pct`,
`api_keys.partner_id`, `webhook_subscriptions.partner_id` (all
`IF NOT EXISTS`, nullable/defaulted — safe on live data).

## Tests

- domain (4): commission math + idempotency + zero-pct skip; respond flow
  (cross-partner 404, bad action 422, double 409, decline voids commission);
  webhook scoping (partner feed gets only its events, tenant feed all);
  key mint/auth + commission validation.
- server (4): full HTTP flow with real partner key incl. cross-surface 403s;
  decline voids pending commission over the wire; admin routes + integration
  summary + secret hygiene; capture attribution (partner vs bare widget).
- pg integration (1, CI `pg` job): `commission_pct` / `partner_id` column
  round-trips.
- tenantctx: partner-key middleware gating.
