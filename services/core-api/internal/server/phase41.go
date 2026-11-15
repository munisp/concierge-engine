package server

// Phase 41: email list hygiene — the tenant suppression list (hard bounce /
// complaint / manual) and the operator recovery path.
//
//	POST   /v1/email/suppressions          suppress an address (idempotent:
//	                                       replay returns 200 + original row)
//	GET    /v1/email/suppressions          list suppressed addresses
//	DELETE /v1/email/suppressions/{email}  operator recovery — remove an address

import (
	"net/http"
	"strings"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase41(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/email/suppressions", h.emailSuppress)
	mux.HandleFunc("GET /v1/email/suppressions", h.emailSuppressionList)
	mux.HandleFunc("DELETE /v1/email/suppressions/{email}", h.emailUnsuppress)
}

func (h *handlers) emailSuppress(w http.ResponseWriter, r *http.Request) {
	var in domain.SuppressInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	row, created, err := h.svc.Outreach.Suppress(r.Context(), tenantOf(r), in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if !created {
		httpx.WriteJSON(w, http.StatusOK, row)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, row)
}

func (h *handlers) emailSuppressionList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.Outreach.Suppressions(r.Context(), tenantOf(r), 500)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"suppressions": rows})
}

func (h *handlers) emailUnsuppress(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PathValue("email"))
	if err := h.svc.Outreach.Unsuppress(r.Context(), tenantOf(r), email); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"unsuppressed": email})
}
