// Phase 36: lead engine endpoints.
//
// Tenant API (operator console):
//
//	POST /v1/leads                    capture (api-sourced)
//	GET  /v1/leads?kind=&status=      list, newest first
//	GET  /v1/leads/pipeline           board summary (by status, by kind)
//	GET  /v1/leads/{id}               one lead
//	POST /v1/leads/{id}/transition    move along the pipeline graph
//	POST /v1/leads/{id}/convert       mint the real record for the kind
//
// Public capture surface (no tenant header — slug-scoped):
//
//	POST /site/{slug}/leads           website/widget capture with utm +
//	                                  referer attribution and a honeypot
package server

import (
	"errors"
	"net/http"
	"strconv"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (h *handlers) registerPhase36(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/leads", h.captureLead)
	mux.HandleFunc("GET /v1/leads", h.listLeads)
	mux.HandleFunc("GET /v1/leads/pipeline", h.leadPipeline)
	mux.HandleFunc("GET /v1/leads/{id}", h.getLead)
	mux.HandleFunc("POST /v1/leads/{id}/transition", h.transitionLead)
	mux.HandleFunc("POST /v1/leads/{id}/convert", h.convertLead)
	mux.HandleFunc("POST /site/{slug}/leads", h.siteCaptureLead)
}

// captureLead is the tenant-API capture: imports, partner portals and the
// console's own "add lead" form all enter through here.
func (h *handlers) captureLead(w http.ResponseWriter, r *http.Request) {
	var in domain.CaptureLeadInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	res, err := h.svc.Leads.Capture(r.Context(), tenantOf(r), in)
	if err != nil {
		writeLeadCaptureError(w, err, in.IdempotencyKey)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *handlers) listLeads(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 200
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	leads, err := h.svc.Leads.List(r.Context(), tenantOf(r), q.Get("kind"), q.Get("status"), limit)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"leads": leads})
}

func (h *handlers) getLead(w http.ResponseWriter, r *http.Request) {
	lead, err := h.svc.Leads.Get(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, lead)
}

func (h *handlers) leadPipeline(w http.ResponseWriter, r *http.Request) {
	sum, err := h.svc.Leads.Pipeline(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sum)
}

type transitionLeadInput struct {
	To         string `json:"to"`
	LostReason string `json:"lost_reason,omitempty"`
}

func (h *handlers) transitionLead(w http.ResponseWriter, r *http.Request) {
	var in transitionLeadInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	lead, err := h.svc.Leads.Transition(r.Context(), tenantOf(r), r.PathValue("id"), in.To, in.LostReason)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, lead)
}

func (h *handlers) convertLead(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Leads.Convert(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// siteCaptureLeadInput adds the honeypot to the public payload. The field
// is invisible to humans; bots that fill every input out themselves.
type siteCaptureLeadInput struct {
	domain.CaptureLeadInput
	Website string `json:"website,omitempty"` // honeypot — never rendered for humans
}

// siteCaptureLead is the public front door: the lead form on the tenant's
// site (and any third-party widget) posts here. Attribution resolves from
// utm query params first, the HTTP Referer second, and the active campaign
// window last — never invented. The honeypot silently accepts-and-drops
// bot submissions so spammers get no signal about what was detected.
func (h *handlers) siteCaptureLead(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.Store.Tenants().GetBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var in siteCaptureLeadInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if in.Website != "" {
		httpx.WriteJSON(w, http.StatusCreated, map[string]any{"ok": true})
		return
	}
	in.Source = "site"
	// Phase 38: the public form is the prospect speaking for themselves —
	// the basis is asserted by the surface, never by the caller's payload.
	in.ContactBasis = model.ContactBasisSelfReported
	q := r.URL.Query()
	in.Channel = firstNonEmpty(in.Channel, q.Get("utm_medium"))
	in.Campaign = firstNonEmpty(in.Campaign, q.Get("utm_campaign"))
	if in.Channel == "" || in.Campaign == "" {
		refChannel, refCampaign := refererAttribution(r.Header.Get("Referer"))
		in.Channel = firstNonEmpty(in.Channel, refChannel)
		in.Campaign = firstNonEmpty(in.Campaign, refCampaign)
	}
	if in.Campaign == "" {
		in.Campaign = h.activeCampaign(r, t)
	}
	res, err := h.svc.Leads.Capture(r.Context(), t.ID, in.CaptureLeadInput)
	if err != nil {
		writeLeadCaptureError(w, err, in.IdempotencyKey)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

// writeLeadCaptureError maps the one expected conflict — an idempotency
// replay — to a quiet 200 (the caller's retry succeeded; there is nothing
// new to show), and defers everything else to the standard error mapping.
func writeLeadCaptureError(w http.ResponseWriter, err error, idem string) {
	if errors.Is(err, httpx.ErrConflict) && idem != "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "idempotent_replay": true})
		return
	}
	httpx.WriteError(w, err)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
