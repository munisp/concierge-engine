# Phase 36 — Lead generation engine

First-class pre-client prospects in four kinds — **person, corporate,
partner, event** — with a pipeline, deterministic scoring, speed-to-lead
SLA alerts, and conversion into the platform's real records. The engine is
a thin CRM layer *on top of* the acquisition and outreach spine, never a
parallel one.

## Core design: a lead is not a shadow client

The lead is the CRM/pipeline object; the linked **Client** (`ClientID`,
resolved at capture through `Clients.ResolveV2`) is the messaging
identity. All nurture — consent gates, frequency caps, quiet hours, drip
sequences, STOP/START — therefore flows through the Phase 14/35 outreach
engine unchanged. No lead-specific messaging path exists.

## Capture

`POST /v1/leads` (tenant API) and `POST /site/{slug}/leads` (public, no
tenant header) share one domain path, `Leads.Capture`:

| Step | Behavior |
|---|---|
| Validation | kind ∈ person/corporate/partner/event; name + (phone ∨ email); corporate/partner require `company`; event requires one of `event_type` / `event_date` / `venue` |
| Normalization | phone → E.164-ish (digits + leading `+`), email → lowercase |
| Dedup | open lead with same phone/email absorbs the repeat inquiry: backfill empty fields, richer values win (headcount, verticals), re-score. A repeat inquiry is buying signal, not a new row. Kind-specific validation is skipped on dedup — a repeat need not re-state the brief |
| Keyed replay | same `idempotency_key` on the same open lead → `ErrConflict` → HTTP 200 `idempotent_replay` (no mutation). Store-level partial-unique index covers key collisions across different contacts |
| Identity | `Clients.ResolveV2` links/creates the messaging client |
| Consent | `consent_marketing: true` → `SetConsent(marketing, true)` — grant-only, capture never revokes |
| Attribution | `lead_captured` event with channel/campaign feeds the Phase 14 funnel |

Public capture attribution: `utm_medium`/`utm_campaign` query params win,
HTTP Referer classification (A7) fills gaps, active campaign window (A12)
is the last resort. The `website` honeypot field silently accepts-and-
drops bot submissions (201, nothing stored).

## Scoring

Deterministic, capped at 100, with human-readable `score_reasons` (the
console shows WHY a lead ranks):

| Signal | Points |
|---|---|
| kind base: event / corporate / partner / person | 20 / 15 / 10 / 5 |
| phone on file / email on file / company named | +10 / +5 / +5 |
| event: headcount ≥ 20 / ≥ 50 | +10 / +5 |
| event: date within 45 days | +10 |
| event: venue named | +5 |
| partner: verticals overlap the tenant's service catalog | +10 |
| came via referral code | +10 |
| marketing consent granted | +5 |

## Pipeline

`NEW → CONTACTED → QUALIFIED → CONVERTED`, any open state → `LOST`
(reason required). Same-state transitions are idempotent no-ops;
`CONVERTED` is reachable only through `Convert`; terminal states are
locked (409). `GET /v1/leads/pipeline` returns the board: totals, by
status, by kind.

## Conversion per kind

| Kind | Mints |
|---|---|
| person / event | `converted_client_id` = the linked client (ready to book) |
| corporate | `CorporateAccount` (name = company, billing email) + contact client's `OrgID` mirrored — identical to the self-serve corporate capture |
| partner | `Partner` in `PENDING` vetting status with the brief's verticals |

Conversion closes the loop with a `lead_converted` attribution event and
is idempotent (re-convert returns the existing links).

## Speed-to-lead SLA

The hourly sweeper (`main.go`, next to sequences/RunDue) pages the
operator roster — `OperatorConfig.AlertPhones`, falling back to the
platform operator phone — for any `NEW` lead older than
`LifecycleConfig.LeadSLAMinutes` (default 60, negative disables).
`sla_alerted_at` is stamped on alert, making the sweep replay-safe.
Partial index `idx_leads_sla` covers exactly `NEW AND sla_alerted_at IS
NULL`.

## Acquisition/outreach integration

- `SegmentRules.LeadKind` — "only clients linked to an OPEN lead of this
  kind" — composes with every other segment rule (locale, tier, booking
  history, consent). Converted/lost leads close, so the segment empties
  itself as the pipeline works.
- `Sequence.Segment` — a drip sequence may declare a declarative audience;
  nil keeps Phase 35 behavior (all clients). `corp-nurture` over
  `{lead_kind: corporate}` is one config entry.
- Broadcasts target segments, so lead-kind-targeted broadcasts work
  unchanged.

## Store

`leads` table (migration 0025): kind/status CHECK constraints, jsonb
`verticals`/`score_reasons`, nullable event/client/conversion/SLA
columns, RLS (enable + force + tenant policy), partial-unique
`(tenant_id, idempotency_key) WHERE idempotency_key <> ''` — keyed
creates conflict, unkeyed always insert. Indexes: tenant+status+created,
tenant+kind, client link, SLA sweep.

## Endpoints

```
POST /v1/leads                      capture (tenant)
GET  /v1/leads?kind=&status=&limit= list (newest first)
GET  /v1/leads/pipeline             board summary
GET  /v1/leads/{id}                 one lead
POST /v1/leads/{id}/transition      pipeline move
POST /v1/leads/{id}/convert         mint real record
POST /site/{slug}/leads             public capture (utm/referer/honeypot)
```

Console: **Leads** page — pipeline cards, kind/status filters, score
with reasons on hover, capture form with kind-specific fields,
transition/convert actions.

## Verification

- 13 domain tests (capture, validation, dedup, scoring determinism,
  vertical overlap, transitions, all 3 conversion kinds, pipeline, SLA
  sweep incl. disable + replay-safety, segment LeadKind, sequence segment
  gating)
- 7 server tests (API capture/validation, list filters, pipeline,
  transition/convert over the wire, idempotent replay, public capture with
  utm/referer, honeypot)
- 3 pg integration tests (30-column round-trip incl. jsonb + nullables,
  partial-unique idempotency vs unkeyed inserts, update + SLA index)
- Local Postgres 15 + PostGIS with RLS-active app role: full suite green
- Full go suite, console build, all 5 python suites: green
