package server

// Phase 26: acquisition monetization surface — the pay-for-performance
// invoice (which platform-attributed first bookings carry a fee) and the
// new-client cohort scoreboard (pacing against the tenant's 90-day goal).

import (
	"net/http"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase16(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/billing/acquisition-fees", h.acquisitionInvoice)
	mux.HandleFunc("GET /v1/acquisition/cohort", h.acquisitionCohort)
}

func (h *handlers) acquisitionInvoice(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	// Default window: the current calendar month.
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)
	if v := r.URL.Query().Get("from"); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpx.WriteError(w, httpx.ErrValidation)
			return
		}
		from = parsed
	}
	if v := r.URL.Query().Get("to"); v != "" {
		parsed, err := time.Parse(time.RFC3339, v)
		if err != nil {
			httpx.WriteError(w, httpx.ErrValidation)
			return
		}
		to = parsed
	}
	inv, err := h.svc.AcquisitionInvoice(r.Context(), tenantOf(r), from, to)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, inv)
}

func (h *handlers) acquisitionCohort(w http.ResponseWriter, r *http.Request) {
	rep, err := h.svc.AcquisitionCohort(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
}
