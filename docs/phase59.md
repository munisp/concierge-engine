# Phase 59 — Signature Ride Soundscapes

The concierge promise, extended to sound: every confirmed booking gets a
track composed from the rider's own preference profile — their genre,
their language, their mood — rendered before pickup and waiting on their
tracking page.

## 1. LLM composes, a music model renders

An LLM cannot produce audio; the pipeline splits the work honestly:

1. **Composition** — `MusicService.CompositionFor` builds a recorded spec
   from the Phase 58 ride profile: genre from `ride.music` verbatim
   ("afrobeats", "classical"), vocal language from `comms.language`
   (unset → instrumental, never a guessed language), mood + tempo from
   `ride.conversation` (quiet → ambient 70 BPM, light → chill 90,
   chatty → upbeat 115). No preferences → house defaults, recorded as
   such in the spec.
2. **Rendering** — a `MusicProvider` posts the spec to a render adapter
   (`CE_MUSIC_ADAPTER_URL` + `CE_MUSIC_PROVIDER`: mubert / beatoven /
   self-hosted musicgen) and returns audio bytes, stored via the Media
   lane (kind `soundscape` — OpenDAL when the blob-adapter is wired,
   inline otherwise).

## 2. Trigger and delivery

Generation hooks `booking.confirmed` — detached from the request path
(context.WithoutCancel + goroutine + recover), because a provider render
runs 30–90s and the confirm response never waits for it. One render per
booking, idempotent (`soundscape:{booking}` unique key — replayed events
and double-clicks return the existing row, never a second paid render).

- `GET /v1/track/{token}/soundscape` — rider view + token-scoped audio URL
- `GET /v1/track/{token}/soundscape/audio` — streams the bytes
- `GET /v1/bookings/{id}/soundscape[/audio]` — operator view/preview
- `POST /v1/bookings/{id}/soundscape` — regenerate after a preference
  change (fresh idempotency key; the old row stays as the audit trail)

Console: the Track page plays the track under "Your ride soundtrack";
the Dispatch board gets a per-booking Soundtrack panel with preview +
Regenerate.

## 3. Honest degrade, stored provenance

| Condition | Row says | Rider sees |
|---|---|---|
| No provider configured | DEGRADED, `provider:not-configured` | curated playlist, labeled |
| Provider errors/times out | DEGRADED, `provider-error: …` verbatim | curated playlist, labeled |
| Render succeeds | RENDERED, provider named, `ai_composed: true` | the track + provenance |
| Store fails after render | FAILED, `media-store: …` | — (operator-visible) |

Nothing is ever presented as AI-composed when it was not: `ai_composed`
is a column, the fallback playlist is named (`curated:afrobeats-lounge`,
`curated:highlife-classics`, …), and the degraded row keeps the verbatim
failure detail. Migration 0038 (RLS-scoped, one-render-per-booking unique
index, CHECK-constrained statuses).

## 4. Tests

- domain: spec mapping from preferences (genre/mood/language/defaults),
  idempotent generate, all three degrade/render paths, audio storage,
  regenerate with provenance swap, view projection
- server: track-token view + audio stream, degraded rows expose no audio
  (404, never fake bytes), tenant regenerate, cross-token rejection
  (trip token ≠ track token)
- pg integration: round-trip, idempotency unique-index conflict, update,
  list, RLS isolation (migration 0038)
