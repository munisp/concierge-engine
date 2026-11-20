package server

// Phase 67: folio aging & dunning. Tenant-admin surface:
//
//	GET  /v1/folios/aging           — derived receivables view (literal wins over {id})
//	POST /v1/folios/{id}/remind     — manual nudge, one touch per 24h
//	POST /v1/dunning/run            — run the sweep on demand (same cadence)

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase67(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/folios/aging", h.folioAging)
	mux.HandleFunc("POST /v1/folios/{id}/remind", h.folioRemind)
	mux.HandleFunc("POST /v1/dunning/run", h.dunningRun)
}

func (h *handlers) folioAging(w http.ResponseWriter, r *http.Request) {
	report, err := h.svc.Folios.Aging(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, report)
}

func (h *handlers) folioRemind(w http.ResponseWriter, r *http.Request) {
	f, err := h.svc.Folios.RemindNow(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, f)
}

func (h *handlers) dunningRun(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Folios.RunDunning(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}
