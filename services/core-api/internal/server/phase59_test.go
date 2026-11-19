package server_test

// Phase 59: signature ride soundscapes — track-token delivery, honest
// degrade, tenant regenerate, cross-token rejection.

import (
	"context"
	"io"
	"net/http"
	"testing"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/model"
)

func TestSoundscapeEndpoints59(t *testing.T) {
	env := newEnv(t)

	// Seed a client with a music preference and a confirmed booking.
	var res struct {
		Client struct {
			ID string `json:"id"`
		} `json:"client"`
	}
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"phone": "+2348088888888", "name": "Sound Rider"}, http.StatusOK, &res)
	clientID := res.Client.ID
	env.do("PUT", "/v1/clients/"+clientID+"/preferences/ride.music", env.tenantID,
		map[string]string{"value": "highlife", "recorded_by": "test"}, http.StatusOK, nil)

	bookingID := seedBookingForClient58(t, env, clientID)
	assignmentID := assignFleet58(t, env, bookingID)
	trackToken := env.svc.Trips.ClientTokenFor(env.tenantID, assignmentID)

	// Degraded (no provider configured in the test env): honest fallback.
	if _, err := env.svc.Music.Generate(t.Context(), env.tenantID, bookingID); err != nil {
		t.Fatal(err)
	}
	var view struct {
		Soundscape struct {
			Status     string `json:"status"`
			AiComposed bool   `json:"ai_composed"`
			FallbackID string `json:"fallback_id"`
			Genre      string `json:"genre"`
		} `json:"soundscape"`
		AudioURL string `json:"audio_url"`
	}
	env.doBearer("GET", "/v1/track/"+trackToken+"/soundscape", "", nil, http.StatusOK, &view)
	if view.Soundscape.Status != model.SoundscapeDegraded || view.Soundscape.AiComposed {
		t.Fatalf("degraded view: %+v", view.Soundscape)
	}
	if view.Soundscape.FallbackID != "curated:highlife-classics" {
		t.Fatalf("fallback: %q", view.Soundscape.FallbackID)
	}
	if view.Soundscape.Genre != "highlife" {
		t.Fatalf("genre: %q", view.Soundscape.Genre)
	}
	if view.AudioURL != "" {
		t.Fatalf("degraded rows expose no audio: %q", view.AudioURL)
	}
	// Audio endpoint on a degraded row → 404, never fake bytes.
	env.doBearer("GET", "/v1/track/"+trackToken+"/soundscape/audio", "", nil, http.StatusNotFound, nil)

	// Rendered: swap in a fake provider, regenerate via the tenant API.
	env.svc.Music.SetProvider(fakeProvider59{})
	var regen struct {
		Soundscape struct {
			Status   string `json:"status"`
			Provider string `json:"provider"`
		} `json:"soundscape"`
	}
	env.do("POST", "/v1/bookings/"+bookingID+"/soundscape", env.tenantID, nil, http.StatusAccepted, &regen)
	if regen.Soundscape.Status != model.SoundscapeRendered || regen.Soundscape.Provider != "faketone" {
		t.Fatalf("regen: %+v", regen.Soundscape)
	}

	// Track view now carries audio; the audio endpoint streams it.
	view = struct {
		Soundscape struct {
			Status     string `json:"status"`
			AiComposed bool   `json:"ai_composed"`
			FallbackID string `json:"fallback_id"`
			Genre      string `json:"genre"`
		} `json:"soundscape"`
		AudioURL string `json:"audio_url"`
	}{}
	env.doBearer("GET", "/v1/track/"+trackToken+"/soundscape", "", nil, http.StatusOK, &view)
	if view.Soundscape.Status != model.SoundscapeRendered || !view.Soundscape.AiComposed {
		t.Fatalf("rendered view: %+v", view.Soundscape)
	}
	if view.AudioURL == "" {
		t.Fatal("rendered row must expose the audio URL")
	}
	resp, err := http.Get(env.server.URL + "/v1/track/" + trackToken + "/soundscape/audio")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("audio stream: %d", resp.StatusCode)
	}
	audioBody, _ := io.ReadAll(resp.Body)
	if len(audioBody) == 0 || string(audioBody[:3]) != "ID3" {
		t.Fatalf("audio stream body: %q", audioBody)
	}

	// Operator view mirrors the same provenance.
	var opView struct {
		Soundscape struct {
			Status   string `json:"status"`
			Provider string `json:"provider"`
		} `json:"soundscape"`
	}
	env.do("GET", "/v1/bookings/"+bookingID+"/soundscape", env.tenantID, nil, http.StatusOK, &opView)
	if opView.Soundscape.Provider != "faketone" {
		t.Fatalf("operator view: %+v", opView.Soundscape)
	}

	// Cross-token rejection: a trip token never reads the rider's soundscape.
	tripToken := env.svc.Trips.TokenFor(env.tenantID, assignmentID)
	env.doBearer("GET", "/v1/track/"+tripToken+"/soundscape", "", nil, http.StatusForbidden, nil)
}

// fakeProvider59 renders deterministic bytes through the real render path.
type fakeProvider59 struct{}

func (fakeProvider59) Name() string { return "faketone" }
func (fakeProvider59) Render(_ context.Context, _ domain.CompositionSpec) (*domain.RenderResult, error) {
	return &domain.RenderResult{
		Audio: []byte("ID3-server-test-audio"), ContentType: "audio/mpeg",
		Title: "Server Test Track", Duration: 180,
	}, nil
}
