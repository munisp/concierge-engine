package domain

// Phase 59: signature ride soundscapes — composition spec from the rider
// profile, idempotent generation, honest degrade, rendered provenance.

import (
	"context"
	"errors"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// fakeProvider renders deterministic bytes; failProvider always errors.
type fakeProvider struct{ name string }

func (f fakeProvider) Name() string { return f.name }
func (f fakeProvider) Render(_ context.Context, spec CompositionSpec) (*RenderResult, error) {
	return &RenderResult{
		Audio: []byte("ID3-fake-audio-" + spec.Genre), ContentType: "audio/mpeg",
		Title: spec.Title, Duration: spec.Duration, ExternalRef: "test-ref-1",
	}, nil
}

type failProvider struct{}

func (failProvider) Name() string { return "failtone" }
func (failProvider) Render(_ context.Context, _ CompositionSpec) (*RenderResult, error) {
	return nil, errors.New("boom: render farm on fire")
}

func TestCompositionSpec59(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	res, err := s.Clients.Resolve(ctx, tenantID, "+2348044444444", "", "DJ Rider")
	if err != nil {
		t.Fatal(err)
	}
	cid := res.Client.ID
	save := func(k, v string) {
		t.Helper()
		if _, err := s.Clients.SavePreference(ctx, tenantID, cid, k, v, "service", "", "test"); err != nil {
			t.Fatal(err)
		}
	}
	save("ride.music", "afrobeats")
	save("ride.conversation", "quiet")
	save("comms.language", "yo")

	bookingID := bookAt(t, s, tenantID, cid, "spec59", time.Now().Add(48*time.Hour).Unix(), 2)
	spec, gotClient, err := s.Music.CompositionFor(ctx, tenantID, bookingID)
	if err != nil {
		t.Fatal(err)
	}
	if gotClient != cid {
		t.Fatalf("client: got %s want %s", gotClient, cid)
	}
	if spec.Genre != "afrobeats" || spec.Mood != "ambient" || spec.TempoBPM != 70 {
		t.Fatalf("spec: %+v", spec)
	}
	if spec.Language != "yo" {
		t.Fatalf("language: %q", spec.Language)
	}
	if spec.Prompt == "" || spec.Title == "" {
		t.Fatalf("prompt/title empty: %+v", spec)
	}

	// No preferences → honest house defaults, instrumental (no vocal guessing).
	res2, _ := s.Clients.Resolve(ctx, tenantID, "+2348055555555", "", "No Prefs")
	bookingID2 := bookAt(t, s, tenantID, res2.Client.ID, "spec59b", time.Now().Add(48*time.Hour).Unix(), 2)
	spec2, _, err := s.Music.CompositionFor(ctx, tenantID, bookingID2)
	if err != nil {
		t.Fatal(err)
	}
	if spec2.Genre != "instrumental lounge" || spec2.Language != "" || spec2.Mood != "chill" {
		t.Fatalf("defaults: %+v", spec2)
	}
}

func TestSoundscapeGenerate59(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	res, _ := s.Clients.Resolve(ctx, tenantID, "+2348066666666", "", "Amara Eze")
	cid := res.Client.ID

	// 1. No provider → honest degrade, curated fallback named, never faked.
	bookingID := bookAt(t, s, tenantID, cid, "gen59a", time.Now().Add(48*time.Hour).Unix(), 2)
	sc, err := s.Music.Generate(ctx, tenantID, bookingID)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Status != model.SoundscapeDegraded || sc.AiComposed || sc.MediaID != "" {
		t.Fatalf("degrade: %+v", sc)
	}
	if sc.Detail != "provider:not-configured" || sc.FallbackID == "" {
		t.Fatalf("degrade detail: %+v", sc)
	}
	// Idempotent: second generate returns the same row.
	sc2, err := s.Music.Generate(ctx, tenantID, bookingID)
	if err != nil || sc2.ID != sc.ID {
		t.Fatalf("idempotent: %v %+v", err, sc2)
	}

	// 2. Provider configured → rendered, provenance recorded, audio stored.
	s.Music.provider = fakeProvider{name: "mubert"}
	bookingID2 := bookAt(t, s, tenantID, cid, "gen59b", time.Now().Add(49*time.Hour).Unix(), 2)
	sc3, err := s.Music.Generate(ctx, tenantID, bookingID2)
	if err != nil {
		t.Fatal(err)
	}
	if sc3.Status != model.SoundscapeRendered || !sc3.AiComposed || sc3.Provider != "mubert" {
		t.Fatalf("rendered: %+v", sc3)
	}
	if sc3.MediaID == "" {
		t.Fatal("no media id")
	}
	md, err := s.Music.Audio(ctx, tenantID, bookingID2)
	if err != nil || len(md.Data) == 0 || md.ContentType != "audio/mpeg" {
		t.Fatalf("audio: %v %+v", err, md)
	}

	// 3. Provider error → degrade with verbatim detail, not a fake render.
	s.Music.provider = failProvider{}
	bookingID3 := bookAt(t, s, tenantID, cid, "gen59c", time.Now().Add(50*time.Hour).Unix(), 2)
	sc4, err := s.Music.Generate(ctx, tenantID, bookingID3)
	if err != nil {
		t.Fatal(err)
	}
	if sc4.Status != model.SoundscapeDegraded || sc4.AiComposed || sc4.FallbackID == "" {
		t.Fatalf("provider-error degrade: %+v", sc4)
	}
	if sc4.Detail == "" || sc4.Detail == "provider:not-configured" {
		t.Fatalf("detail: %q", sc4.Detail)
	}
	if _, err := s.Music.Audio(ctx, tenantID, bookingID3); err == nil {
		t.Fatal("degraded row must not serve audio")
	}

	// 4. Regenerate mints a fresh row (preference changed after booking).
	s.Music.provider = fakeProvider{name: "beatoven"}
	sc5, err := s.Music.Regenerate(ctx, tenantID, bookingID3)
	if err != nil {
		t.Fatal(err)
	}
	if sc5.ID == sc4.ID || sc5.Status != model.SoundscapeRendered || sc5.Provider != "beatoven" {
		t.Fatalf("regen: %+v", sc5)
	}
	// View surfaces the newest render.
	v, err := s.Music.View(ctx, tenantID, bookingID3)
	if err != nil || v.Status != model.SoundscapeRendered || !v.HasAudio || !v.AiComposed {
		t.Fatalf("view: %v %+v", err, v)
	}

	// 5. View for a booking with no soundscape → NONE, not an error.
	res3, _ := s.Clients.Resolve(ctx, tenantID, "+2348077777777", "", "Silent Client")
	bookingID4 := bookAt(t, s, tenantID, res3.Client.ID, "gen59d", time.Now().Add(51*time.Hour).Unix(), 2)
	// Note: bookAt → Confirm triggers the detached hook; the row may or may
	// not exist yet depending on the goroutine — both NONE and a real
	// status are acceptable, but it must never error.
	if _, err := s.Music.View(ctx, tenantID, bookingID4); err != nil {
		t.Fatalf("view none: %v", err)
	}
}
