package server

// Phase 21: tenant-managed outreach message templates.

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase13(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/outreach/templates", h.upsertTemplate)
	mux.HandleFunc("GET /v1/outreach/templates", h.listTemplates)
	mux.HandleFunc("DELETE /v1/outreach/templates/{id}", h.deleteTemplate)
}

func (h *handlers) upsertTemplate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		Purpose string `json:"purpose"`
		Body    string `json:"body"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	t, err := h.svc.Outreach.UpsertTemplate(r.Context(), tenantOf(r), body.Name, body.Purpose, body.Body)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

func (h *handlers) listTemplates(w http.ResponseWriter, r *http.Request) {
	ts, err := h.svc.Outreach.ListTemplates(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ts)
}

func (h *handlers) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Outreach.DeleteTemplate(r.Context(), tenantOf(r), r.PathValue("id")); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
