package server

// Phase 65: partner rate cards. Tenant-admin CRUD for the negotiated
// per-vertical pricing applied to demand the partner sourced:
//
//	GET    /v1/partners/{id}/rate-cards
//	PUT    /v1/partners/{id}/rate-cards           {vertical, kind, value, currency}
//	DELETE /v1/partners/{id}/rate-cards/{vertical}

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (h *handlers) registerPhase65(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/partners/{id}/rate-cards", h.rateCardList)
	mux.HandleFunc("PUT /v1/partners/{id}/rate-cards", h.rateCardSet)
	mux.HandleFunc("DELETE /v1/partners/{id}/rate-cards/{vertical}", h.rateCardDelete)
}

func (h *handlers) rateCardList(w http.ResponseWriter, r *http.Request) {
	cards, err := h.svc.RateCards.List(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, cards)
}

func (h *handlers) rateCardSet(w http.ResponseWriter, r *http.Request) {
	var in model.RateCard
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	card, err := h.svc.RateCards.Set(r.Context(), tenantOf(r), r.PathValue("id"), &in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, card)
}

func (h *handlers) rateCardDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.RateCards.Delete(r.Context(), tenantOf(r), r.PathValue("id"), r.PathValue("vertical")); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
