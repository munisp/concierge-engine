package server

// Phase 63: demand channels. Two surfaces:
//
//   - /v1/partner/requests — partner-key authenticated (like the rest of
//     /v1/partner/*): a hotel desk, travel agency or transport partner
//     books on behalf of a guest; the request enters the standard
//     quoting → booking → dispatch pipeline with the source attached, and
//     the partner can list the demand they have brought.
//   - Tenant-admin: PUT /v1/partners/{id}/kind classifies the channel;
//     GET /v1/analytics/sources reports per-channel margin (gross captured,
//     commission, net) so the operator sees exactly what each channel
//     costs — and what direct demand saves.

import (
	"net/http"
	"strconv"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase63(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/partner/requests", h.partnerRequestCreate)
	mux.HandleFunc("GET /v1/partner/requests", h.partnerRequestList)
	mux.HandleFunc("PUT /v1/partners/{id}/kind", h.partnerKindSet)
	mux.HandleFunc("GET /v1/analytics/sources", h.analyticsSources)
}

func (h *handlers) partnerRequestCreate(w http.ResponseWriter, r *http.Request) {
	tid, pid, err := partnerOf(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var in domain.SourcedRequestInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	created, err := h.svc.Channels.CreateSourcedRequest(r.Context(), tid, pid, &in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, created)
}

func (h *handlers) partnerRequestList(w http.ResponseWriter, r *http.Request) {
	tid, pid, err := partnerOf(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := h.svc.Channels.SourcedRequests(r.Context(), tid, pid, limit)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) partnerKindSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind string `json:"kind"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	p, err := h.svc.Channels.SetPartnerKind(r.Context(), tenantOf(r), r.PathValue("id"), body.Kind)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *handlers) analyticsSources(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	rep, err := h.svc.Channels.SourceReport(r.Context(), tenantOf(r), days)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
}
