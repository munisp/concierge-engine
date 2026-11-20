package server

// Phase 70: partial payments against issued folios. Tenant-admin surface:
//
//	POST /v1/folios/{id}/payments   {amount_minor, method, reference}
//
// ISSUED-only, amount must be positive and never exceed the outstanding
// balance (409); each payment posts its proportional net/fee split and
// the folio settles itself when the balance reaches zero.

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase70(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/folios/{id}/payments", h.folioPaymentRecord)
}

func (h *handlers) folioPaymentRecord(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AmountMinor int64  `json:"amount_minor"`
		Method      string `json:"method"`    // bank_transfer | cash | card | other
		Reference   string `json:"reference"` // bank ref, receipt no. — optional
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	f, err := h.svc.Folios.RecordPayment(r.Context(), tenantOf(r), r.PathValue("id"),
		body.AmountMinor, body.Method, body.Reference)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, f)
}
