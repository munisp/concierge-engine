package server

// Phase 19: reward sharing program surface — client reward balance +
// history for the console growth board and client-facing surfaces.

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase11(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/clients/{id}/rewards", h.clientRewards)
}

func (h *handlers) clientRewards(w http.ResponseWriter, r *http.Request) {
	summary, err := h.svc.RewardSummary(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, summary)
}
