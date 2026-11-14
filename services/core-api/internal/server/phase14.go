package server

// Phase 22: scheduling surface (availability query) and unified client
// threads (cross-channel correspondence view).

import (
	"net/http"
	"strconv"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase14(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/dispatch/availability", h.dispatchAvailability)
	mux.HandleFunc("GET /v1/clients/{id}/conversations", h.clientThreads)
}

func (h *handlers) dispatchAvailability(w http.ResponseWriter, r *http.Request) {
	from, err1 := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	to, err2 := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
	if err1 != nil || err2 != nil {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	avail, err := h.svc.Dispatch.Availability(r.Context(), tenantOf(r), from, to)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, avail)
}

func (h *handlers) clientThreads(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	threads, err := h.svc.Conversations.Threads(r.Context(), tenantOf(r), r.PathValue("id"), limit)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, threads)
}
