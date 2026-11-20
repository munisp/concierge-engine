package domain

// Phase 59: signature ride soundscapes.
//
// An LLM writes the composition; a music model renders the audio. This
// service owns the composition spec (built from the rider's Phase 58
// preference profile), the provider boundary, and the honesty rules:
//
//   - No provider configured      → DEGRADED, detail "provider:not-configured"
//   - Provider call fails         → DEGRADED, detail "provider-error: ..."
//   - Provider renders            → RENDERED, ai_composed=true, provider named
//
// A degraded row names its curated fallback; nothing is ever presented as
// AI-composed when it was not. Generation is idempotent per booking
// ("soundscape:{booking}") — replayed booking.confirmed events and double
// clicks return the existing row instead of paying for a second render.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// CompositionSpec is the recorded input to the renderer — the console and
// the rider can see exactly what the track was built from.
type CompositionSpec struct {
	// Skip, when non-empty, is the honest reason no render should happen
	// (e.g. "rider-prefers:no-music" — the Focus mode sets ride.music=none).
	// Generate records a DEGRADED row with this detail instead of calling
	// the provider.
	Skip     string `json:"skip,omitempty"`
	Genre    string `json:"genre"`
	Language string `json:"language"` // "" = instrumental (no vocals)
	Mood     string `json:"mood"`     // ambient | chill | upbeat
	TempoBPM int    `json:"tempo_bpm"`
	Duration int    `json:"duration_seconds"`
	Title    string `json:"title"`
	Prompt   string `json:"prompt"` // LLM-style style prompt handed to the provider
}

// RenderResult is what a provider returns: audio bytes plus the metadata
// the row records for provenance.
type RenderResult struct {
	Audio       []byte
	ContentType string
	Title       string
	Duration    int
	ExternalRef string
}

// MusicProvider renders a composition spec into audio. Implementations:
// httpMusicProvider (Mubert / Beatoven / self-hosted MusicGen adapter —
// same sidecar discipline as the WhatsApp and blob adapters).
type MusicProvider interface {
	Name() string
	Render(ctx context.Context, spec CompositionSpec) (*RenderResult, error)
}

type MusicService struct {
	s        *Services
	provider MusicProvider // nil = not configured; every generate degrades honestly
}

// SetProvider swaps the render provider (operator reconfiguration, tests).
func (svc *MusicService) SetProvider(p MusicProvider) { svc.provider = p }

// NewMusicService builds the service; empty providerURL leaves the
// provider nil so deployments without a music contract degrade to curated
// playlists instead of failing.
func NewMusicService(s *Services, providerName, providerURL, providerKey string) *MusicService {
	m := &MusicService{s: s}
	if providerURL != "" {
		name := providerName
		if name == "" {
			name = "http"
		}
		m.provider = &httpMusicProvider{name: name, base: providerURL, key: providerKey}
	}
	return m
}

// moodFromConversation maps the rider's conversation preference to a
// musical mood and tempo. Unset → chill (the house default).
func moodFromConversation(conversation string) (mood string, bpm int) {
	switch conversation {
	case "quiet":
		return "ambient", 70
	case "chatty":
		return "upbeat", 115
	default: // "light" or unset
		return "chill", 90
	}
}

// defaultSoundscapeDuration — a signature track is a vignette, not an
// album; matched to a typical airport-transfer leg.
const defaultSoundscapeDuration = 180

// CompositionFor builds the spec from the booking's rider profile. The
// genre comes from ride.music verbatim (free text — "afrobeats",
// "classical"); language from comms.language with an instrumental default
// (never guess a vocal language); mood from ride.conversation.
func (svc *MusicService) CompositionFor(ctx context.Context, tenantID, bookingID string) (*CompositionSpec, string, error) {
	b, err := svc.s.Store.Bookings().Get(ctx, tenantID, bookingID)
	if err != nil {
		return nil, "", err
	}
	req, err := svc.s.Store.Requests().Get(ctx, tenantID, b.RequestID)
	if err != nil {
		return nil, "", err
	}
	clientID := req.ClientID
	rp, err := svc.s.Clients.RideProfile(ctx, tenantID, clientID)
	if err != nil {
		// Preference read failure must not block generation — compose from
		// house defaults and record the defaults in the spec.
		rp = &RideProfile{}
	}
	spec := &CompositionSpec{Language: strings.TrimSpace(rp.Language)}
	// Phase 60: explicit silence wins — never render against a stated
	// no-music preference.
	if isNoMusic(rp.Music) {
		spec.Skip = "rider-prefers:no-music"
	}
	genre := strings.TrimSpace(rp.Music)
	if genre == "" {
		// Mode-implied genre before the house default.
		if g := modeDefaultGenre(rp.Mode); g != "" {
			genre = g
		} else {
			genre = "instrumental lounge"
		}
	}
	language := spec.Language
	// A mode is the newer, more explicit signal; it overrides the
	// conversation-derived mood when set.
	mood, bpm := moodFromConversation(rp.Conversation)
	if m, b, ok := modeMood(rp.Mode); ok {
		mood, bpm = m, b
	}
	title := "Signature Ride"
	if rp.Music != "" {
		title = "Signature Ride — " + genre
	}
	prompt := fmt.Sprintf("%s, %s mood, %d bpm, %d seconds", genre, mood, bpm, defaultSoundscapeDuration)
	if language != "" {
		prompt += ", vocals in " + language
	} else {
		prompt += ", instrumental, no vocals"
	}
	spec.Genre, spec.Mood, spec.TempoBPM = genre, mood, bpm
	spec.Duration, spec.Title, spec.Prompt = defaultSoundscapeDuration, title, prompt
	return spec, clientID, nil
}

// curatedFallback names the house playlist for a genre — the honest
// stand-in when no provider renders. The id is a stable reference the
// console/Track page resolves to a labeled, human-curated playlist.
func curatedFallback(genre string) string {
	g := strings.ToLower(genre)
	switch {
	case strings.Contains(g, "afro"):
		return "curated:afrobeats-lounge"
	case strings.Contains(g, "jazz"):
		return "curated:jazz-standards"
	case strings.Contains(g, "classical"):
		return "curated:classical-focus"
	case strings.Contains(g, "highlife"):
		return "curated:highlife-classics"
	case strings.Contains(g, "hip") || strings.Contains(g, "rap"):
		return "curated:hiphop-instrumental"
	default:
		return "curated:house-lounge"
	}
}

// Generate renders (or honestly degrades) the soundscape for a booking.
// Idempotent: an existing row for the booking is returned as-is.
func (svc *MusicService) Generate(ctx context.Context, tenantID, bookingID string) (*model.Soundscape, error) {
	if existing, err := svc.s.Store.Soundscapes().ForBooking(ctx, tenantID, bookingID); err == nil {
		return existing, nil // one render per booking
	}
	spec, clientID, err := svc.CompositionFor(ctx, tenantID, bookingID)
	if err != nil {
		return nil, err
	}
	now := svc.s.Now()
	sc := &model.Soundscape{
		ID: NewID(), TenantID: tenantID, BookingID: bookingID, ClientID: clientID,
		Status: model.SoundscapeRendering,
		Genre:  spec.Genre, Language: spec.Language, Mood: spec.Mood,
		TempoBPM: spec.TempoBPM, Title: spec.Title, Duration: spec.Duration,
		IdempotencyKey: "soundscape:" + bookingID,
		CreatedAt:      now, UpdatedAt: now,
	}
	if err := svc.s.Store.Soundscapes().Create(ctx, sc); err != nil {
		if err == httpx.ErrConflict {
			// Concurrent generate lost the race — return the winner's row.
			return svc.s.Store.Soundscapes().ForBooking(ctx, tenantID, bookingID)
		}
		return nil, err
	}
	return svc.render(ctx, sc, spec)
}

// render is the shared tail of Generate/Regenerate: provider call,
// media store, honest status transition.
func (svc *MusicService) render(ctx context.Context, sc *model.Soundscape, spec *CompositionSpec) (*model.Soundscape, error) {
	sc.UpdatedAt = svc.s.Now()
	if spec.Skip != "" {
		sc.Status = model.SoundscapeDegraded
		sc.Detail = spec.Skip
		sc.AiComposed = false
		return sc, svc.s.Store.Soundscapes().Update(ctx, sc)
	}
	if svc.provider == nil {
		sc.Status = model.SoundscapeDegraded
		sc.Detail = "provider:not-configured"
		sc.FallbackID = curatedFallback(sc.Genre)
		sc.AiComposed = false
		return sc, svc.s.Store.Soundscapes().Update(ctx, sc)
	}
	res, err := svc.provider.Render(ctx, *spec)
	if err != nil || res == nil || len(res.Audio) == 0 {
		sc.Status = model.SoundscapeDegraded
		sc.Detail = "provider-error: " + errString(err)
		if err == nil {
			sc.Detail = "provider-error: empty render"
		}
		sc.FallbackID = curatedFallback(sc.Genre)
		sc.AiComposed = false
		return sc, svc.s.Store.Soundscapes().Update(ctx, sc)
	}
	ct := res.ContentType
	if ct == "" {
		ct = "audio/mpeg"
	}
	md := &model.Media{
		ID: NewID(), TenantID: sc.TenantID, Kind: "soundscape",
		ContentType: ct, Data: res.Audio, SizeBytes: len(res.Audio),
		CreatedAt: svc.s.Now(),
	}
	if err := svc.s.Store.Media().Put(ctx, md); err != nil {
		sc.Status = model.SoundscapeFailed
		sc.Detail = "media-store: " + err.Error()
		sc.AiComposed = false
		return sc, svc.s.Store.Soundscapes().Update(ctx, sc)
	}
	sc.Status = model.SoundscapeRendered
	sc.Provider = svc.provider.Name()
	sc.AiComposed = true
	sc.MediaID = md.ID
	if res.Title != "" {
		sc.Title = res.Title
	}
	if res.Duration > 0 {
		sc.Duration = res.Duration
	}
	sc.Detail = ""
	if res.ExternalRef != "" {
		sc.Detail = "ref:" + res.ExternalRef
	}
	return sc, svc.s.Store.Soundscapes().Update(ctx, sc)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Regenerate forces a fresh render (operator action — e.g. the rider
// changed their music preference after booking). The idempotency key gets
// a timestamp suffix so the one-render-per-booking rule stays honest
// while the new render proceeds.
func (svc *MusicService) Regenerate(ctx context.Context, tenantID, bookingID string) (*model.Soundscape, error) {
	spec, clientID, err := svc.CompositionFor(ctx, tenantID, bookingID)
	if err != nil {
		return nil, err
	}
	now := svc.s.Now()
	sc := &model.Soundscape{
		ID: NewID(), TenantID: tenantID, BookingID: bookingID, ClientID: clientID,
		Status: model.SoundscapeRendering,
		Genre:  spec.Genre, Language: spec.Language, Mood: spec.Mood,
		TempoBPM: spec.TempoBPM, Title: spec.Title, Duration: spec.Duration,
		IdempotencyKey: fmt.Sprintf("soundscape:%s:%d", bookingID, now.UnixNano()),
		CreatedAt:      now, UpdatedAt: now,
	}
	if err := svc.s.Store.Soundscapes().Create(ctx, sc); err != nil {
		return nil, err
	}
	return svc.render(ctx, sc, spec)
}

// OnConfirmedDetached is the booking.confirmed hook: generation runs off
// the request path (a provider render is 30–90s; the booking confirm
// response never waits for it).
func (svc *MusicService) OnConfirmedDetached(ctx context.Context, tenantID, bookingID string) {
	ctx = context.WithoutCancel(ctx)
	go func() {
		defer func() { _ = recover() }()
		_, _ = svc.Generate(ctx, tenantID, bookingID)
	}()
}

// SoundscapeView is the client/operator-facing projection. AudioURL is
// filled by the server (it owns the token); the domain reports only
// whether audio exists.
type SoundscapeView struct {
	Status     string `json:"status"` // RENDERING | RENDERED | DEGRADED | FAILED | NONE
	Title      string `json:"title,omitempty"`
	Genre      string `json:"genre,omitempty"`
	Mood       string `json:"mood,omitempty"`
	Duration   int    `json:"duration_seconds,omitempty"`
	Provider   string `json:"provider,omitempty"`
	AiComposed bool   `json:"ai_composed"`
	Detail     string `json:"detail,omitempty"`
	FallbackID string `json:"fallback_id,omitempty"`
	HasAudio   bool   `json:"has_audio"`
}

func (svc *MusicService) View(ctx context.Context, tenantID, bookingID string) (*SoundscapeView, error) {
	sc, err := svc.s.Store.Soundscapes().ForBooking(ctx, tenantID, bookingID)
	if err == httpx.ErrNotFound {
		return &SoundscapeView{Status: "NONE"}, nil // composing hasn't started
	}
	if err != nil {
		return nil, err
	}
	return &SoundscapeView{
		Status: sc.Status, Title: sc.Title, Genre: sc.Genre, Mood: sc.Mood,
		Duration: sc.Duration, Provider: sc.Provider, AiComposed: sc.AiComposed,
		Detail: sc.Detail, FallbackID: sc.FallbackID, HasAudio: sc.MediaID != "",
	}, nil
}

// Audio returns the rendered bytes for streaming, or ErrNotFound when the
// row has no audio (degraded/none — the caller renders the fallback card).
func (svc *MusicService) Audio(ctx context.Context, tenantID, bookingID string) (*model.Media, error) {
	sc, err := svc.s.Store.Soundscapes().ForBooking(ctx, tenantID, bookingID)
	if err != nil {
		return nil, err
	}
	if sc.MediaID == "" || sc.Status != model.SoundscapeRendered {
		return nil, httpx.ErrNotFound
	}
	return svc.s.Store.Media().Get(ctx, tenantID, sc.MediaID)
}

// --- httpMusicProvider: Mubert / Beatoven / self-hosted MusicGen adapter.

type httpMusicProvider struct {
	name string
	base string
	key  string
	hc   *http.Client
}

func (p *httpMusicProvider) Name() string { return p.name }

// Render posts the spec to {base}/v1/render and reads back either inline
// base64 audio or an audio_url to fetch. Any non-2xx, timeout, or empty
// body is an error — the caller degrades honestly, never fakes a render.
func (p *httpMusicProvider) Render(ctx context.Context, spec CompositionSpec) (*RenderResult, error) {
	body, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(p.base, "/")+"/v1/render", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.key != "" {
		req.Header.Set("Authorization", "Bearer "+p.key)
	}
	hc := p.hc
	if hc == nil {
		hc = &http.Client{Timeout: 120 * time.Second} // renders run 30–90s
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("music provider unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("music provider returned %d", resp.StatusCode)
	}
	var rr struct {
		AudioB64    string `json:"audio_base64"`
		AudioURL    string `json:"audio_url"`
		ContentType string `json:"content_type"`
		Title       string `json:"title"`
		Duration    int    `json:"duration_seconds"`
		ExternalRef string `json:"external_ref"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&rr); err != nil {
		return nil, fmt.Errorf("music provider response: %w", err)
	}
	res := &RenderResult{ContentType: rr.ContentType, Title: rr.Title,
		Duration: rr.Duration, ExternalRef: rr.ExternalRef}
	switch {
	case rr.AudioB64 != "":
		res.Audio, err = base64.StdEncoding.DecodeString(rr.AudioB64)
		if err != nil {
			return nil, fmt.Errorf("music provider audio decode: %w", err)
		}
	case rr.AudioURL != "":
		aud, err := hc.Get(rr.AudioURL) //nolint:bodyclose // closed below
		if err != nil {
			return nil, fmt.Errorf("music provider audio fetch: %w", err)
		}
		defer aud.Body.Close()
		res.Audio, err = io.ReadAll(io.LimitReader(aud.Body, 64<<20))
		if err != nil {
			return nil, fmt.Errorf("music provider audio read: %w", err)
		}
	default:
		return nil, fmt.Errorf("music provider returned no audio")
	}
	return res, nil
}
