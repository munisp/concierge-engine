# Phase 46: Data Retention & Minimization (GDPR Art. 5(1)(e))

Until Phase 46 the platform kept everything forever: geo stamps, message
bodies, attribution events. That left one GDPR pillar — storage limitation
— open, and location history is the most sensitive data the platform
holds. Phase 46 closes it with a per-tenant retention posture, an hourly
sweep, an on-demand operator run, and an erasure integration.

## What the sweep does

| Category | Action | Default window |
|---|---|---|
| `geostamps` | **DELETE** rows older than the window | 90 days |
| `messages` in CLOSED conversations | **SCRUB** `body` to a provenance marker | 730 days |
| `attribution_events` | **DELETE** rows older than the window | 730 days |

Message **rows** (id, author, sent_at) survive scrubbing — threading and
counts are metadata, not content. Only CLOSED conversations are age-swept;
an active thread keeps its content regardless of age.

**Exempt, deliberately:** quotes, bookings, payments, ledger postings
(accounting retention outranks minimization), the consent evidence ledger
and the audit log (Art. 5(2) accountability).

## Per-tenant posture (`retention_config`)

```json
{ "disabled": false, "geo_stamps_days": 90, "message_bodies_days": 730, "attribution_days": 730 }
```

Per category: `0` = platform default, **negative** = never purge this
category, positive = that many days. `disabled: true` skips the tenant
entirely (the sweep reports it as a no-op). Zero-value tenants get the
defaults — a tenant that never thinks about retention still has a
defensible posture.

Patched via the existing `PUT /v1/tenant` selective-patch field
`retention_config`; editable from the console Settings card, which also
exposes **Run retention sweep now**.

## Surfaces

- **Hourly sweep** inside the existing per-tenant background goroutine
  (`cmd/core-api`), after lifecycle/outreach/sequence/lead sweeps.
- **`POST /v1/retention/run`** (tenant-scoped): runs one sweep on demand,
  returns `{geo_stamps_purged, message_bodies_scrubbed,
  attribution_purged, disabled}`. Idempotent — purged rows are gone,
  scrubbed bodies match the marker and are not re-counted.
- **Audit log**: every sweep writes a `retention.sweep` entry with the
  counts.

## Erasure integration (Art. 17)

`EraseWithEvidence` now also scrubs the client's message bodies
**immediately**, including OPEN conversations the age sweep leaves alone —
erasure says content goes now, not at the next retention window. Advisory
fail-open: erasure itself has already committed, so a scrub failure
surfaces as a `retention.scrub_failed` event rather than an error.

## Deliberate interaction

Attribution purging interacts with lifecycle hygiene by design: engagement
older than the attribution window stops counting as engagement. That is
the correct reading of storage limitation — you may not keep telemetry
"just in case" it feeds a hygiene heuristic.

## Migration

`db/migrations/0032_phase46.sql` — `tenants.retention_config jsonb NOT
NULL DEFAULT '{}'`. No other schema change: purges and scrubs operate on
existing tables.

## Tests

- `domain/phase46_test.go` — defaults sweep old geo/attribution + scrub
  old closed-conversation bodies, fresh and open-conversation content
  survives, idempotency, audit-log entry, Disabled no-op, negative
  category skip, erasure scrubs open + closed conversations.
- `server/phase46_test.go` — run endpoint counts, idempotent re-run,
  cross-tenant isolation, `retention_config` patch round-trip, disabled
  no-op, missing tenant header refused.
- `store/pg_integration_phase46_test.go` — real-Postgres purge/scrub with
  RLS tenant isolation, scrub idempotency, per-client erasure scrub,
  `retention_config` round-trip through Get/List/UpdateConfig.
