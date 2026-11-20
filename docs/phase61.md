# Phase 61 — Journey Orchestration (flight-to-curb)

Phases 58–60 personalized the *ride*. Phase 61 orchestrates the *trip*:
flights, hotels, ground transfers and partner legs become first-class
`itinerary_legs`, a flight-watch keeps them current, and a delay on the
flight **cascades automatically** to the airport pickup — the chauffeur
and the rider see the new time without a phone call.

## 1. The gap this closes

GDS attach (Phase 31) recorded where a booking *came from* — attribution.
It did not model the rider's itinerary, watch the flight, or replan the
pickup. That is the difference between "we know the PNR" and end-to-end
trip coordination. Phase 61 is the coordination layer.

## 2. Legs, not notes

`model.ItineraryLeg` (migration 0040): typed legs — `flight`, `hotel`,
`ground`, `partner` — with scheduled and actual times, status
(SCHEDULED / EN_ROUTE / DELAYED / ARRIVED / COMPLETED / CANCELLED),
delay minutes, and a provenance `source` label (`live` / `manual` /
`scheduled` — the platform never invents an ETA).

Two links make legs a *journey*:

- `booking_id` — a ground leg belongs to a platform booking.
- `linked_leg_id` — an arrival-transfer ground leg points at the flight
  leg it meets, so a delay knows where to cascade.

`AddLeg` with a flight number resolves the schedule through the GDS
chain (`GDSPort.FlightStatus`, live-first with mock fallback), uppercases
the number, and auto-titles the leg ("BA75 LHR → LOS"). With no live
provider the leg is still created — labeled `scheduled`, honestly.

## 3. The delay cascade

`ApplyFlightUpdate(tenantID, legID, report)`:

1. Writes the flight leg's new status, delay, and ETA.
2. If the delay *changed*, recomputes every chained ground leg:
   `adjusted pickup = flight ETA + BufferMinutes(client)`.
3. `BufferMinutes` is profile-weighted: base 45, +15 for heavy luggage,
   +15 for mobility assistance, capped at 90 — the same validated
   preference vocabulary from Phase 58, not a hidden knob.
4. Emits `journey.ground_adjusted` and notifies the rider over WhatsApp
   (`journey.rider_notified`; honest degrade when no adapter is
   configured — the adjustment itself is never faked).

Re-applying the same report is a no-op; unlinked legs are never touched.

## 4. The flight watch

`WatchAllTenants(ctx, window)` scans flight legs whose scheduled
departure falls in the window and whose status is still watchable
(partial index `itinerary_legs_watch_idx`), pulls a fresh
`FlightStatusReport`, and runs it through the same cascade. Terminal
legs (ARRIVED / COMPLETED / CANCELLED) leave the watch set. The
operator path `POST /v1/journeys/watch` runs one pass; the same contract
is what a cron/scheduler calls.

## 5. Surfaces

- **Rider tracking page** — a journey timeline card (kind badge, title,
  time, delay, "live flight data" vs "scheduled times" provenance),
  refreshing every 60 s.
- **Client 360 (console)** — journey card with the timeline and an
  add-leg form (flight legs by flight number; hotel/ground/partner by
  title).
- **Chauffeur app** — the trip view carries `journey`: flight number,
  status, delay, landing ETA, and the adjusted pickup line when the
  cascade has run.

## 6. Endpoints

| Method | Path | Purpose |
|---|---|---|
| GET | `/v1/clients/{id}/journey` | timeline, soonest first |
| POST | `/v1/clients/{id}/journey/legs` | add a leg (flight → GDS resolve) |
| POST | `/v1/bookings/{id}/journey/link` | chain booking's ground leg to a flight |
| POST | `/v1/journeys/legs/{id}/flight-update` | apply a status report (cascade) |
| POST | `/v1/journeys/watch` | one flight-watch pass, all tenants |
| GET | `/v1/track/{token}/journey` | rider's timeline (track token) |

## 7. Honesty ledger

- Mock GDS (CI/dev) returns on-time "scheduled" reports — labeled
  `scheduled`, never presented as live.
- Amadeus `/v2/schedule/flights` is wired live-first; a live failure
  falls back to the schedule, and the source label says so.
- Rider notifications degrade to "not configured" events — no fake
  "sent".
- pg integration tests create a real Quote row before the Booking
  (`quote_id uuid NOT NULL`); RLS isolation is asserted cross-tenant.
