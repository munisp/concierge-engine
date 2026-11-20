package server

// Phase 64: channel engagement closure — operator reattribution of demand
// that arrived off-channel (email, phone) but was really partner-sourced.
// Guest/partner confirm notifications are domain-side (booking.go), no
// endpoint of their own.

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase64(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/requests/{id}/source", h.requestSourceSet)
}

func (h *handlers) requestSourceSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PartnerID string `json:"partner_id"` // "" returns the request to direct
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	updated, err := h.svc.Channels.AttributeSource(r.Context(), tenantOf(r), r.PathValue("id"), body.PartnerID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, updated)
}
