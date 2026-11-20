package server

// Phase 66: partner bill-to-desk folios. Tenant-admin surface:
//
//	PUT  /v1/partners/{id}/bill-to-desk        {enabled}
//	GET  /v1/partners/{id}/folios
//	POST /v1/partners/{id}/folios/{period}/{currency}/issue
//	GET  /v1/folios/{id}
//	POST /v1/folios/{id}/settle

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase66(mux *http.ServeMux) {
	mux.HandleFunc("PUT /v1/partners/{id}/bill-to-desk", h.billToDeskSet)
	mux.HandleFunc("GET /v1/partners/{id}/folios", h.folioList)
	mux.HandleFunc("POST /v1/partners/{id}/folios/{period}/{currency}/issue", h.folioIssue)
	mux.HandleFunc("GET /v1/folios/{id}", h.folioGet)
	mux.HandleFunc("POST /v1/folios/{id}/settle", h.folioSettle)
}

func (h *handlers) billToDeskSet(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	p, err := h.svc.Folios.SetBillToDesk(r.Context(), tenantOf(r), r.PathValue("id"), in.Enabled)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *handlers) folioList(w http.ResponseWriter, r *http.Request) {
	folios, err := h.svc.Folios.List(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, folios)
}

func (h *handlers) folioIssue(w http.ResponseWriter, r *http.Request) {
	f, err := h.svc.Folios.Issue(r.Context(), tenantOf(r), r.PathValue("id"), r.PathValue("period"), r.PathValue("currency"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, f)
}

func (h *handlers) folioGet(w http.ResponseWriter, r *http.Request) {
	f, err := h.svc.Folios.Get(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, f)
}

func (h *handlers) folioSettle(w http.ResponseWriter, r *http.Request) {
	f, err := h.svc.Folios.Settle(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, f)
}
