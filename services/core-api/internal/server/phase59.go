package server

// Phase 59: signature ride soundscapes.
//
//	GET  /v1/track/{token}/soundscape          rider's track view (track token)
//	GET  /v1/track/{token}/soundscape/audio    stream the rendered audio (track token)
//	GET  /v1/bookings/{id}/soundscape          operator view (tenant API)
//	POST /v1/bookings/{id}/soundscape          regenerate after a preference change (tenant API)
//
// The track token IS the credential; degraded rows name their curated
// fallback and ai_composed is false — nothing renders as composed when it
// was not.

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase59(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/track/{token}/soundscape", h.trackSoundscape)
	mux.HandleFunc("GET /v1/track/{token}/soundscape/audio", h.trackSoundscapeAudio)
	mux.HandleFunc("GET /v1/bookings/{id}/soundscape", h.bookingSoundscape)
	mux.HandleFunc("GET /v1/bookings/{id}/soundscape/audio", h.bookingSoundscapeAudio)
	mux.HandleFunc("POST /v1/bookings/{id}/soundscape", h.regenSoundscape)
}

// trackBooking resolves a track token to (tenantID, bookingID).
func (h *handlers) trackBooking(r *http.Request) (string, string, error) {
	tid, assignmentID, err := h.svc.Trips.ParseClientToken(r.PathValue("token"))
	if err != nil {
		return "", "", err
	}
	assignments, err := h.svc.Store.Dispatch().ListAssignments(r.Context(), tid)
	if err != nil {
		return "", "", err
	}
	for _, a := range assignments {
		if a.ID == assignmentID {
			return tid, a.BookingID, nil
		}
	}
	return "", "", httpx.ErrNotFound
}

// soundscapeJSON is the view with the token-scoped audio URL filled in.
func (h *handlers) soundscapeJSON(w http.ResponseWriter, status int, v any, audioPath string) {
	httpx.WriteJSON(w, status, map[string]interface{}{
		"soundscape": v,
		"audio_url":  audioPath,
	})
}

func (h *handlers) trackSoundscape(w http.ResponseWriter, r *http.Request) {
	tid, bookingID, err := h.trackBooking(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	v, err := h.svc.Music.View(r.Context(), tid, bookingID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	audioURL := ""
	if v.HasAudio {
		audioURL = "/v1/track/" + r.PathValue("token") + "/soundscape/audio"
	}
	h.soundscapeJSON(w, http.StatusOK, v, audioURL)
}

func (h *handlers) trackSoundscapeAudio(w http.ResponseWriter, r *http.Request) {
	tid, bookingID, err := h.trackBooking(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	md, err := h.svc.Music.Audio(r.Context(), tid, bookingID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", md.ContentType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(md.Data)
}

func (h *handlers) bookingSoundscape(w http.ResponseWriter, r *http.Request) {
	tid := tenantOf(r)
	v, err := h.svc.Music.View(r.Context(), tid, r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	audioURL := ""
	if v.HasAudio {
		audioURL = "/v1/bookings/" + r.PathValue("id") + "/soundscape/audio"
	}
	h.soundscapeJSON(w, http.StatusOK, v, audioURL)
}

func (h *handlers) bookingSoundscapeAudio(w http.ResponseWriter, r *http.Request) {
	tid := tenantOf(r)
	md, err := h.svc.Music.Audio(r.Context(), tid, r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", md.ContentType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(md.Data)
}

// regenSoundscape re-renders after a preference change (operator action).
func (h *handlers) regenSoundscape(w http.ResponseWriter, r *http.Request) {
	tid := tenantOf(r)
	sc, err := h.svc.Music.Regenerate(r.Context(), tid, r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]interface{}{"soundscape": sc})
}
