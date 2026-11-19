package model

import "time"

// Phase 59: signature ride soundscapes — an AI-composed track per booking,
// composed from the rider's Phase 58 preference profile (genre, language,
// mood) and rendered by a music provider (or honestly degraded to a
// curated playlist when no provider is configured/reachable).
//
// Provenance discipline: AiComposed is always true for RENDERED rows and
// the provider is recorded verbatim; DEGRADED rows name their fallback —
// nothing is ever presented as composed when it was not.

const (
	SoundscapeRendering = "RENDERING" // accepted, provider call in flight
	SoundscapeRendered  = "RENDERED"  // audio bytes stored (MediaID set)
	SoundscapeDegraded  = "DEGRADED"  // provider unavailable — curated fallback
	SoundscapeFailed    = "FAILED"    // provider errored after retry budget
)

type Soundscape struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	BookingID string `json:"booking_id"`
	ClientID  string `json:"client_id"`
	Status    string `json:"status"` // RENDERING | RENDERED | DEGRADED | FAILED
	// Composition spec actually used — recorded, not implied, so the
	// console and the rider can see exactly what the track was built from.
	Genre    string `json:"genre,omitempty"`
	Language string `json:"language,omitempty"`
	Mood     string `json:"mood,omitempty"` // ambient | chill | upbeat (from ride.conversation)
	TempoBPM int    `json:"tempo_bpm,omitempty"`
	Title    string `json:"title,omitempty"` // LLM/provider-assigned or spec-derived
	Duration int    `json:"duration_seconds,omitempty"`
	// Render provenance.
	Provider   string `json:"provider,omitempty"`    // mubert | beatoven | musicgen | "" (degraded)
	AiComposed bool   `json:"ai_composed"`           // true only when RENDERED by a provider
	Detail     string `json:"detail,omitempty"`      // degrade/failure reason, verbatim
	MediaID    string `json:"media_id,omitempty"`    // rendered audio asset (Media kind=soundscape)
	FallbackID string `json:"fallback_id,omitempty"` // curated playlist key when DEGRADED
	// IdempotencyKey "soundscape:{booking}" — one render per booking;
	// regeneration mints a new row with a fresh key suffix.
	IdempotencyKey string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
