package server

// Phase 16 routes: EU/US-first readiness — GDPR Art. 15/17 and CCPA
// data-subject rights (access export, erasure with accounting carve-out).

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase10(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/clients/{id}/export", h.exportClient)
	mux.HandleFunc("POST /v1/clients/{id}/erase", h.eraseClient)
}

// exportClient answers a data-subject access request with one JSON document
// containing everything the platform holds on the client.
func (h *handlers) exportClient(w http.ResponseWriter, r *http.Request) {
	exp, err := h.svc.Clients.ExportWithEvidence(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="client-export.json"`)
	httpx.WriteJSON(w, http.StatusOK, exp)
}

// eraseClient executes the right to erasure: identity scrubbed to a
// pseudonymous tombstone, preference memory deleted, financial history
// retained. Idempotent.
func (h *handlers) eraseClient(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.Clients.EraseWithEvidence(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}
