# Phase 63 — Demand Channels

Every request on the platform now answers one question first-class:
**where did this demand come from?** A direct client (the most lucrative
channel — zero commission), a hotel concierge desk, a travel agency,
another transport company farming overflow in, or a tour operator. The
source rides on the request, is denormalized onto the booking at confirm,
drives commission, and feeds a per-channel margin report.

## 1. The channel model

| Channel | `source_kind` | How demand enters | Commission |
|---|---|---|---|
| Direct client | `direct` | site, WhatsApp, console, repeat | none — the margin benchmark |
| Hotel desk | `hotel` | partner book-on-behalf | partner's `commission_pct` |
| Travel agency | `travel_agency` | partner book-on-behalf | partner's `commission_pct` |
| Transport company | `transport_company` | farm-in overflow, book-on-behalf | partner's `commission_pct` |
| Tour operator | `tour_operator` | partner book-on-behalf (legacy default) | partner's `commission_pct` |

`partners.kind` (migration 0042) classifies the channel with a closed
vocabulary; legacy partners default to `tour_operator`. Requests and
bookings carry `source_kind` + `source_partner_id` (CHECK-constrained,
`ON DELETE SET NULL` — a removed partner never orphans the demand record).

## 2. Robust workflows into the existing pipeline

Sourcing is **attribution, not a parallel pipeline** — that is the
robustness property. A sourced request is created by
`ChannelService.CreateSourcedRequest`, which:

1. validates the partner (SUSPENDED → 403; a partner key scopes tenant +
   partner, so one hotel can never source into another's name);
2. resolves the guest through the same progressive-registration path as
   any capture (`ResolveV2` — repeat guests merge onto their existing
   client, jurisdiction-aware consent defaults apply);
3. creates a **standard** service request — the state machine, instant
   quote engine, KYC gate, SLA timers, dispatch, tracking, soundscapes,
   ride modes and journey orchestration all apply unchanged;
4. emits `channel.request_sourced` with full provenance.

Idempotency keys are namespaced per partner (`src:{partner}:{key}`) so
two desks can never collide, and replays return the original request.

## 3. Money: sourcing commission, no double pay

- At booking confirm, the sourcing partner accrues a PENDING payout at
  their commission rate (idempotency `src-commission:{booking}:{partner}`)
  — it flows through the Phase 48 settlement batching like any other
  payout. Failure is audit-logged and never blocks the booking.
- A partner who **sources and fulfils** the same booking is paid once:
  fulfilment accrual (Phase 56) skips when a sourcing payout exists, and
  the merge is audit-logged. The tenant never pays the same rate twice
  on one booking.
- Direct bookings accrue nothing — and the report makes that visible.

## 4. The margin report

`GET /v1/analytics/sources?days=N` aggregates confirmed bookings in the
window per channel: bookings, gross captured value, commission, net.
Rules inherited from Phase 40 honesty: single-currency rows only — mixed
currencies are flagged and totals **withheld**, never blended. Rows sort
by gross descending; the response note states plainly that direct demand
carries no channel commission.

Console: the Partners page gains a channel-kind classifier on every row,
kind on registration, and a **Demand channels** margin card. The partner
portal (magic-link PWA) shows each partner the guests they referred and
those requests' live states.

## 5. Endpoints

| Method | Path | Purpose |
|---|---|---|
| POST | `/v1/partner/requests` | book-on-behalf (partner key) |
| GET | `/v1/partner/requests` | partner's sourced-demand feed |
| PUT | `/v1/partners/{id}/kind` | classify the channel (admin) |
| GET | `/v1/analytics/sources?days=N` | per-channel margin report |

## 6. Honesty ledger

- Sourcing changes nothing about fulfilment: a sourced booking is quoted,
  gated, dispatched and tracked by the same code as a direct one.
- Commission math is per-currency or withheld; no blended ratios.
- The portal's guest feed is best-effort and never locks a partner out
  of their offers/money if the listing fails.
- A removed partner leaves demand records intact (`SET NULL`), labeled
  by `source_kind` alone.
