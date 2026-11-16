package server

// Phase 47: KYC / identity verification — the operator surface. The queue
// endpoints manage the case workflow (open → submit → decide); the gate
// itself fires inside BookingService.Confirm with no endpoint of its own;
// the tenant's thresholds are patched through the existing PUT /v1/tenant
// kyc_config field; the hourly goroutine in cmd/core-api expires stale
// undecided cases.

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase47(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/kyc/cases", h.kycOpen)
	mux.HandleFunc("GET /v1/kyc/cases", h.kycQueue)
	mux.HandleFunc("GET /v1/kyc/cases/{id}", h.kycGet)
	mux.HandleFunc("POST /v1/kyc/cases/{id}/submit", h.kycSubmit)
	mux.HandleFunc("POST /v1/kyc/cases/{id}/decide", h.kycDecide)
	mux.HandleFunc("GET /v1/clients/{id}/kyc", h.kycForClient)
	mux.HandleFunc("POST /v1/kyc/expire", h.kycExpire)
}

func (h *handlers) kycOpen(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientID       string `json:"client_id"`
		Level          string `json:"level"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	kc, err := h.svc.Kyc.Open(r.Context(), tenantOf(r), body.ClientID, body.Level, body.IdempotencyKey)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, kc)
}

func (h *handlers) kycQueue(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Kyc.List(r.Context(), tenantOf(r), r.URL.Query().Get("status"), 200)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) kycGet(w http.ResponseWriter, r *http.Request) {
	kc, err := h.svc.Kyc.Get(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, kc)
}

func (h *handlers) kycSubmit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DocumentType string `json:"document_type"`
		DocumentRef  string `json:"document_ref"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	kc, err := h.svc.Kyc.Submit(r.Context(), tenantOf(r), r.PathValue("id"), body.DocumentType, body.DocumentRef)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, kc)
}

func (h *handlers) kycDecide(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Approve   bool   `json:"approve"`
		Reason    string `json:"reason"`
		DecidedBy string `json:"decided_by"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	kc, err := h.svc.Kyc.Decide(r.Context(), tenantOf(r), r.PathValue("id"), body.Approve, body.Reason, body.DecidedBy)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, kc)
}

func (h *handlers) kycForClient(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Kyc.ListForClient(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// kycExpire runs the stale-case expiry sweep on demand (the hourly
// background goroutine runs it continuously).
func (h *handlers) kycExpire(w http.ResponseWriter, r *http.Request) {
	n, err := h.svc.Kyc.ExpireStale(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"expired": n})
}
