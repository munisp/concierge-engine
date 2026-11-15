package server

// Phase 40: campaign economics — the spend ledger joins the sends ledger
// and the attribution spine so the console answers cost-per-conversion and
// ROAS per campaign ("fund the next event from evidence").
//
//	POST /v1/outreach/campaigns/{id}/spend   record one spend entry (idempotent)
//	GET  /v1/outreach/campaigns/{id}/spend   spend ledger for one campaign
//	GET  /v1/outreach/campaigns              economics board: every campaign
//	                                         with sends or spend, full funnel

import (
	"net/http"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase40(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/outreach/campaigns/{id}/spend", h.campaignSpendRecord)
	mux.HandleFunc("GET /v1/outreach/campaigns/{id}/spend", h.campaignSpendList)
	mux.HandleFunc("GET /v1/outreach/campaigns", h.campaignsBoard)
}

func (h *handlers) campaignSpendRecord(w http.ResponseWriter, r *http.Request) {
	var in domain.RecordSpendInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	// The path campaign is authoritative — a mismatched body campaign is an
	// operator error, not a second campaign.
	in.Campaign = r.PathValue("id")
	sp, err := h.svc.Campaigns.RecordSpend(r.Context(), tenantOf(r), in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, sp)
}

func (h *handlers) campaignSpendList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.Campaigns.SpendList(r.Context(), tenantOf(r), r.PathValue("id"), 500)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"spend": rows})
}

func (h *handlers) campaignsBoard(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.Campaigns.Campaigns(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"campaigns": rows})
}
