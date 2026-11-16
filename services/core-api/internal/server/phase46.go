package server

// Phase 46: data retention & minimization — the operator surface. One
// tenant-authed endpoint runs the sweep on demand (the hourly background
// goroutine in cmd/core-api runs it continuously); the tenant's posture
// itself is patched through the existing PUT /v1/tenant retention_config
// field.

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase46(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/retention/run", h.retentionRun)
}

// retentionRun fires one retention sweep for the caller's tenant and
// returns the counts. Idempotent: re-running immediately after a sweep is
// a no-op (purged rows are gone; scrubbed bodies match the marker).
func (h *handlers) retentionRun(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Retention.Run(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}
