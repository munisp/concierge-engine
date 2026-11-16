package server

// Phase 48: settlement batching & payout-depth endpoints. Batch assembly,
// quote and execution net a partner's pending Mojaloop payouts into one
// scheme transfer; the statement endpoint is the partner-facing
// reconciliation surface; the sweep endpoint lets ops trigger the timeout
// recovery manually (the hourly goroutine also runs it).

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase48(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/settlements/batches", h.batchAssemble)
	mux.HandleFunc("GET /v1/settlements/batches", h.batchList)
	mux.HandleFunc("GET /v1/settlements/batches/{id}", h.batchGet)
	mux.HandleFunc("POST /v1/settlements/batches/{id}/quote", h.batchQuote)
	mux.HandleFunc("POST /v1/settlements/batches/{id}/execute", h.batchExecute)
	mux.HandleFunc("GET /v1/partners/{id}/settlement-statement", h.settlementStatement)
	mux.HandleFunc("POST /v1/payouts/sweep", h.payoutSweep)
}

func (h *handlers) batchAssemble(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PartnerID      string `json:"partner_id"`
		Currency       string `json:"currency"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	b, err := h.svc.Settlements.Assemble(r.Context(), tenantOf(r), body.PartnerID, body.Currency, body.IdempotencyKey)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, b)
}

func (h *handlers) batchList(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Settlements.ListBatches(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) batchGet(w http.ResponseWriter, r *http.Request) {
	b, err := h.svc.Settlements.GetBatch(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *handlers) batchQuote(w http.ResponseWriter, r *http.Request) {
	b, err := h.svc.Settlements.QuoteBatch(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *handlers) batchExecute(w http.ResponseWriter, r *http.Request) {
	b, err := h.svc.Settlements.ExecuteBatch(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, b)
}

func (h *handlers) settlementStatement(w http.ResponseWriter, r *http.Request) {
	st, err := h.svc.Settlements.Statement(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, st)
}

// payoutSweep runs the settlement-timeout recovery on demand (ops button);
// the hourly goroutine in cmd/core-api runs the same sweeps unattended.
func (h *handlers) payoutSweep(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantOf(r)
	payouts, err := h.svc.Payouts.SweepTimeouts(r.Context(), tenantID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	batches, err := h.svc.Settlements.SweepBatchTimeouts(r.Context(), tenantID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]int{
		"payouts_compensated": payouts,
		"batches_compensated": batches,
	})
}
