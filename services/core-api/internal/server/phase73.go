package server

// Phase 73: credit notes & refunds. Tenant-admin surface:
//
//	POST /v1/folios/{id}/credits   {amount_minor, reason}
//
// ISSUED folio → credit note: the partner owes less (over-credit 409;
// a zero balance settles the folio). SETTLED folio → refund: reversal
// ledger postings return the money, cumulative refunds never exceed
// what was collected. OPEN folio → 409 (still accruing).

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase73(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/folios/{id}/credits", h.folioCreditRecord)
}

func (h *handlers) folioCreditRecord(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AmountMinor int64  `json:"amount_minor"`
		Reason      string `json:"reason"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	f, err := h.svc.Folios.RecordCredit(r.Context(), tenantOf(r), r.PathValue("id"),
		body.AmountMinor, body.Reason)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, f)
}
