# Phase 24 — Tenant settings console, selective config patches, voice takeover

Operational-control wave: six tenant config objects were API-only (editable
by curl, invisible to operators), the tenant PUT had a wipe-by-omission
footgun, and the voice channel had no human takeover path.

## Selective patch semantics (`PUT /v1/tenant`)

The handler decoded the body into a full `model.Tenant` and copied **every**
config field wholesale — so a client sending only `{"operator_config": …}`
would zero the tax engine, reward program, dispatch config, and everything
else. That is a data-loss bug the moment a UI edits one card at a time.

Fixed with a pointer-field patch struct: only keys present in the JSON body
are applied; absent keys are untouched. `PUT /v1/tenant {}` is now a no-op,
not a wipe. Covered by `TestTenantConfigSelectivePatch` (tax + dispatch
survive an operator-roster patch; empty patch mutates nothing).

## Console Settings page (`/settings`)

One card per config object, each saving only itself (safe under the new
patch semantics):

- **Operator roster** — alert phones for SLA-failure push (empty = global).
- **Dispatch scheduling** — turnaround buffer + default service duration.
- **Tax** — engine (none/flat/stripe-tax), registration country, rate in
  basis points, prices-include-tax convention.
- **Payment rails** — disable Stripe / disable Paystack toggles.
- **Reward program** — kill-switch, referrer %, referee welcome %, per-
  accrual cap, expiry days.
- **Outreach & SLA** — per-tenant marketing cap, quote SLA seconds.

## Voice: operator takeover + sessions board

- `POST /v1/voice/sessions/{id}/takeover {staff_name}` — a human concierge
  joins the live room: mints a room-scoped staff token (identity
  `staff-<name>`, same token contract as every participant — no privileged
  media path) and records `[human concierge joining: …]` as a SYSTEM
  provenance line on the VOICE thread. 409 on ended sessions, 404
  cross-tenant, 422 without a name, 503 without LiveKit credentials.
- `GET /v1/voice/sessions[?active=1]` — tenant-scoped session list for an
  operator board (newest first).

## Verification

- 1 new server test (selective patch, incl. empty-patch no-op).
- 4 new voice-adapter tests (list scoping, takeover token + provenance,
  ended-session 409, tenant scoping + validation) — 37 total.
- Full suite: Go build/vet both tags + all tests; concierge-agent 26;
  whatsapp-adapter 55+2 skipped; voice-adapter 37; analytics 16+1 skipped;
  console `tsc -b && vite build` clean (51 modules).
