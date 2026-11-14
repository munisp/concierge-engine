package server

import (
	"net/http"
	"strconv"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// Phase 14: growth surface — proactive outreach (consent-gated,
// frequency-capped, idempotent), acquisition funnel, client referral codes.
func (h *handlers) registerPhase8(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/outreach/send", h.outreachSend)
	mux.HandleFunc("GET /v1/outreach/sends", h.outreachSends)
	mux.HandleFunc("GET /v1/outreach/verdict", h.outreachVerdict)
	mux.HandleFunc("GET /v1/attribution/funnel", h.attributionFunnel)
	mux.HandleFunc("GET /v1/clients/{id}/referral-code", h.clientReferralCode)
	mux.HandleFunc("GET /v1/referrals/lookup", h.referralLookup)
}

func (h *handlers) outreachSend(w http.ResponseWriter, r *http.Request) {
	var in domain.SendOutreachInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	o, err := h.svc.Outreach.Send(r.Context(), tenantOf(r), in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, o)
}

func (h *handlers) outreachSends(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	sends, err := h.svc.Outreach.Sends(r.Context(), tenantOf(r), limit)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sends)
}

func (h *handlers) outreachVerdict(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	v, err := h.svc.Outreach.Verdict(r.Context(), tenantOf(r),
		q.Get("client_id"), q.Get("purpose"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *handlers) attributionFunnel(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.Funnel(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rows)
}

func (h *handlers) clientReferralCode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.svc.Store.Clients().Get(r.Context(), tenantOf(r), id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"client_id": id, "code": model.ClientReferralCode(id),
	})
}

// referralLookup resolves a referral code to the referring client (name +
// code) so capture pages can confirm "referred by Ada" before signup.
func (h *handlers) referralLookup(w http.ResponseWriter, r *http.Request) {
	cl, err := h.svc.Store.Clients().FindByReferralCode(r.Context(), tenantOf(r),
		r.URL.Query().Get("code"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"client_id": cl.ID, "full_name": cl.FullName,
		"code": model.ClientReferralCode(cl.ID),
	})
}
