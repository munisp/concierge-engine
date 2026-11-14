package server

// Phase 25: client journey tracking (Uber-like monitoring link for the
// passenger) and lifecycle follow-ups (all-year engagement engine).
//
// The track endpoint is token-authenticated like the chauffeur trip links:
// the HMAC token in the URL is the credential, so no tenant header is
// required — and none is trusted. The track-link mint and the follow-ups
// trigger are operator endpoints (tenant header as usual).

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase15(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/track/{token}", h.getTrack)
	mux.HandleFunc("POST /v1/dispatch/assignments/{id}/track-link", h.trackLink)
	mux.HandleFunc("POST /v1/followups/run", h.runFollowUps)
}

func (h *handlers) getTrack(w http.ResponseWriter, r *http.Request) {
	view, err := h.svc.Trips.Track(r.Context(), r.PathValue("token"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *handlers) trackLink(w http.ResponseWriter, r *http.Request) {
	token := h.svc.Trips.ClientTokenFor(tenantOf(r), r.PathValue("id"))
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"token": token,
		"url":   "/track?token=" + token, // served by web/console
	})
}

func (h *handlers) runFollowUps(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.FollowUps.Run(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}
