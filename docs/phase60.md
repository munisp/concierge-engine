# Phase 60 — Ride Modes & Cabin Pre-conditioning

Phase 58 gave riders a vocabulary; Phase 59 composed it into sound.
Phase 60 makes it one tap: a **ride mode** is a named ambiance preset —
Focus, Relax, Family, Celebrate — that expands into a bundle of
preferences through the normal validated path, and a **prep checklist**
that turns the profile into concrete chauffeur actions before pickup.

## 1. Modes compose the vocabulary; they never bypass it

`ApplyMode(tenantID, clientID, mode, recordedBy)`:

1. Writes `ride.mode = {mode}` (new curated enum: focus / relax / family /
   celebrate / custom — anything else is 422).
2. Expands the preset bundle — Focus sets `conversation=quiet`,
   `temperature_c=21`, `route=fastest`, `music=none` — one
   `SavePreference` call per key, so every write is validated,
   consent-scoped, VIP-encrypted, and audit-logged with provenance
   `mode:{mode}`. Expansion order is deterministic.

House presets live in code; tenants override preset **content** via
`tenants.mode_presets` (migration 0039, jsonb). Same-named tenant entries
replace the house content; tenant-invented mode **names** are ignored, so
the validated enum never forks per tenant. `GET /v1/ride-modes` returns
the tenant-effective set.

Entry points: `POST /v1/clients/{id}/ride-mode` (console — mode buttons
on the client 360 ride-profile card) and `POST /v1/track/{token}/ride-mode`
(rider self-serve picker on the tracking page; cross-token rejection
intact).

## 2. Prep tasks — the car is ready before the door opens

`PrepTasks(preferences)` derives the actionable checklist:

| Preference | Chauffeur sees |
|---|---|
| `ride.temperature_c=20` | Set cabin to 20 °C before pickup |
| `ride.child_seat=toddler` | Install toddler child seat |
| `ride.luggage=heavy` | Clear full luggage space |
| `access.mobility=wheelchair` | Wheelchair-accessible setup |
| `ride.music=none` | No music — quiet cabin |
| `ride.music=afrobeats` + volume | Music: afrobeats (low) |
| `ride.conversation=quiet` | Quiet ride — minimal conversation |

Projected onto the chauffeur trip view (`prep_tasks`, rendered as a
checklist in the PWA) and the partner assignment view (same tasks,
derived from the consent-filtered preference set — a tour operator gets
the checklist, never a `private`-scoped preference).

## 3. Mode-aware soundscapes

The Phase 59 composition spec inherits the mode: a mode is the newer,
more explicit signal, so it overrides the conversation-derived mood
(celebrate → upbeat 115 BPM even when conversation says quiet; focus →
ambient 70). An unset genre falls back to the mode's implied genre
before the house default. Explicit silence wins: `ride.music=none` (what
Focus sets) skips rendering entirely — the row records
`rider-prefers:no-music`, the provider is never called, and the Track
page says "Quiet ride, per your preference" instead of pretending a
fallback is a composition.

## 4. Tests

- domain: preset expansion + enum guard + tenant override merge (content
  yes, names no) + prep-task derivation + mode-mood override + the
  focus/no-music skip (a failing provider proves it is never called)
- server: preset catalog, tenant apply, self-serve apply via track
  token, 422 on unknown mode, trip view carries prep tasks, cross-token
  rejection
- pg integration: `mode_presets` round-trip through create/get/by-slug/
  update; nil maps never write SQL NULL (guarded marshal)
