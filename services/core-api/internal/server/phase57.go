package server

// Phase 57: partner PWA + WhatsApp offers.
//
// Token-authenticated portal (the HMAC link IS the credential — same
// discipline as chauffeur trip links and client tracking):
//
//	GET  /v1/portal/{token}           portal payload (assignments + money)
//	POST /v1/portal/{token}/respond   {assignment_id, action, note}
//
// Tenant-admin routes (console):
//
//	GET  /v1/partners/{id}/portal-link   shareable portal URL
//	POST /v1/partners/{id}/notify        WhatsApp nudge with the portal link

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase57(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/portal/{token}", h.portalView)
	mux.HandleFunc("POST /v1/portal/{token}/respond", h.portalRespond)
	mux.HandleFunc("GET /v1/partners/{id}/portal-link", h.partnerPortalLink)
	mux.HandleFunc("POST /v1/partners/{id}/notify", h.partnerNotify)
}

func (h *handlers) portalView(w http.ResponseWriter, r *http.Request) {
	tid, pid, err := h.svc.Trips.ParsePartnerToken(r.PathValue("token"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	view, err := h.svc.PartnerOps.PortalView(r.Context(), tid, pid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *handlers) portalRespond(w http.ResponseWriter, r *http.Request) {
	tid, pid, err := h.svc.Trips.ParsePartnerToken(r.PathValue("token"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var body struct {
		AssignmentID string `json:"assignment_id"`
		Action       string `json:"action"` // accept | decline
		Note         string `json:"note,omitempty"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	a, err := h.svc.PartnerOps.Respond(r.Context(), tid, pid, body.AssignmentID, body.Action, body.Note)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, a)
}

func (h *handlers) partnerPortalLink(w http.ResponseWriter, r *http.Request) {
	tid, pid := tenantOf(r), r.PathValue("id")
	if _, err := h.svc.Partners.Get(r.Context(), tid, pid); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"url": h.svc.PartnerOps.PortalURL(tid, pid),
	})
}

func (h *handlers) partnerNotify(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.PartnerOps.NotifyOffer(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}
