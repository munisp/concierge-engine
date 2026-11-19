# Phase 58 — Rider Preference Profiles

The concierge promise is "the car feels like yours before you get in."
Phase 58 turns rider preferences from an untyped key/value bag into a
curated, validated vocabulary that flows to every surface that needs it:
the chauffeur's trip sheet, the partner's offer card, the client
360, and the client's own tracking page.

## 1. A curated vocabulary, not free text

`preferences58.go` defines the house vocabulary. Curated enum keys reject
unknown values with 422 (`ErrValidation`); curated free-text keys are
length-capped; `ride.temperature_c` is an integer in 16–26 °C; `custom.*`
keys stay open for genuinely bespoke notes (200-rune cap); unknown legacy
keys pass through untouched so existing data never breaks validation.

| Key | Values |
| --- | --- |
| `ride.conversation` | quiet / light / chatty |
| `ride.music` | free text (60) — "afrobeats", "classical", "own playlist" |
| `ride.music_volume` | free text (10) |
| `ride.temperature_c` | integer 16–26 |
| `ride.route` | fastest / scenic / no_tolls |
| `ride.seat` | front_passenger / rear_left / rear_right |
| `ride.child_seat` | none / infant / toddler / booster |
| `ride.luggage` | light / standard / heavy |
| `dining.dietary` | none / vegetarian / vegan / halal / kosher / gluten_free |
| `dining.allergens` | free text (200) |
| `access.mobility` | none / assistance / wheelchair |
| `comms.language` | free text (16) — ISO-ish, "en", "yo", "fr" |
| `avi.amenities` | free text (200) — aviation/FBO notes |
| `custom.*` | free text (200 runes) |

Validation lives in `ValidatePreference`, wired into
`ClientService.SavePreference` after key normalization and before consent
defaulting — one choke point for tenant API, track self-serve, and any
future import path. Tested: every enum value round-trips, bad values 422,
temperature bounds enforced both sides, oversized free text rejected,
legacy keys untouched.

## 2. Typed ride profile

`ClientService.RideProfile` assembles the curated ride/dining/access keys
into a typed struct (conversation, music, volume, temperature, route,
seat, child seat, luggage, dietary, allergens, mobility, language) with an
`Empty()` guard so surfaces render nothing instead of zero-value noise.

- `GET /v1/clients/{id}/ride-profile` — tenant-API typed profile
- Console `Clients.tsx` — **RideProfileCard** on the client 360 with
  quick-set buttons for the common combos (quiet + 22 °C + no music, etc.)

## 3. Preferences where the work happens

Three projections of the same data, each scoped to its audience:

- **Chauffeur** — `RidePreferenceMap` (ride comfort subset only). Trip
  views (`TripView.Preferences`) carry the comfort block; the chauffeur
  PWA renders them as chips — conversation, music, temperature, seat,
  child seat, luggage — so the car is set before pickup.
- **Partner** — `PartnerPreferenceMap`, consent-filtered: preferences
  with `consent_scope == "private"` never leave the house.
  `PartnerAssignmentView.Preferences` rides the partner portal payload
  (memoized per request — one preference read per client, not per
  assignment), and the `assignment.partner_offered` webhook payload now
  includes `preferences` so partner systems get the same context as the
  portal.
- **Client self-serve** — `GET /v1/track/{token}/preferences` and
  `PUT /v1/track/{token}/preferences/{key}` behind the existing tracking
  magic link (no login, no app). The Track page gains a **Ride
  preferences** editor — the same validation applies (bad enum → 422),
  and track tokens still can't open trip or partner surfaces (cross-token
  rejection tested).

## 4. Provenance and honesty

No schema migration — preferences were already a table; this phase adds
vocabulary, validation, and projection. No preference is ever fabricated:
surfaces render only what was explicitly set (`Empty()` guards, omitempty
payloads), and the partner feed is strictly consent-scoped. What the
rider told us is what the chauffeur sees — nothing more, nothing
inferred.
