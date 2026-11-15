// Phase 37: bulk lead import — greenfield bootstrap.
//
//	POST /v1/leads/import   {leads: [CaptureLeadInput, ...]} → import receipt
//
// Rows are advisory: invalid rows are rejected with per-row reasons in a
// 200 receipt, not a failed request. Batches over the domain cap and empty
// batches are 422.
package server

import (
	"net/http"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase37(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/leads/import", h.importLeads)
}

type importLeadsInput struct {
	Leads []domain.CaptureLeadInput `json:"leads"`
}

func (h *handlers) importLeads(w http.ResponseWriter, r *http.Request) {
	var in importLeadsInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	res, err := h.svc.Leads.Import(r.Context(), tenantOf(r), in.Leads)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}
