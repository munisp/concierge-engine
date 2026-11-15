package server

// Phase 44 routes: the consent evidence ledger. One read surface —
// GET /v1/clients/{id}/consent-history — tenant-scoped like every client
// route. Writes happen inside the domain at each consent-mutation call
// site; there is deliberately no POST/DELETE (evidence is append-only).

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase44(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/clients/{id}/consent-history", h.consentHistory)
}

func (h *handlers) consentHistory(w http.ResponseWriter, r *http.Request) {
	evs, err := h.svc.Clients.ConsentHistory(r.Context(), tenantOf(r), r.PathValue("id"), 200)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, evs)
}
