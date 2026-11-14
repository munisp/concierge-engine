package server

// Phase 20: promo-code campaigns — tenant-managed fixed-credit codes and
// the client redemption endpoint.

import (
	"net/http"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase12(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/promos", h.createPromo)
	mux.HandleFunc("GET /v1/promos", h.listPromos)
	mux.HandleFunc("POST /v1/promos/redeem", h.redeemPromo)
}

func (h *handlers) createPromo(w http.ResponseWriter, r *http.Request) {
	var in domain.CreatePromoInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	c, err := h.svc.CreatePromo(r.Context(), tenantOf(r), in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, c)
}

func (h *handlers) listPromos(w http.ResponseWriter, r *http.Request) {
	promos, err := h.svc.ListPromos(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, promos)
}

func (h *handlers) redeemPromo(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code     string `json:"code"`
		ClientID string `json:"client_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	reward, err := h.svc.RedeemPromo(r.Context(), tenantOf(r), body.Code, body.ClientID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, reward)
}
