package server

// Phase 56: partner integration layer. Two surfaces:
//
//   - /v1/partner/* — authenticated by partner-scoped API keys (scope
//     "partner", bound to one partner at mint). The tenantctx middleware
//     rejects these keys everywhere else; tenantHeaderOrKey requests without
//     a partner binding are rejected here. This is the tour operator's
//     programmatic surface: see offered assignments, accept/decline, and
//     reconcile commission payouts.
//   - /v1/partners/{id}/... admin routes (tenant-authed, console): mint
//     partner keys, register partner webhook feeds, set the commission rate,
//     and pull the integration summary the console renders.
//
// The embeddable lead-capture side needs no new endpoint: partners post the
// public /site/{slug}/leads form with source "partner:{name}"; the snippet
// is served by the integration summary below.

import (
	"net/http"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/tenantctx"
)

func (h *handlers) registerPhase56(mux *http.ServeMux) {
	// Partner-authenticated surface.
	mux.HandleFunc("GET /v1/partner/assignments", h.partnerAssignments)
	mux.HandleFunc("POST /v1/partner/assignments/{id}/respond", h.partnerRespond)
	mux.HandleFunc("GET /v1/partner/settlement", h.partnerSettlement)
	// Tenant-admin surface (console).
	mux.HandleFunc("POST /v1/partners/{id}/keys", h.partnerKeyCreate)
	mux.HandleFunc("GET /v1/partners/{id}/keys", h.partnerKeyList)
	mux.HandleFunc("POST /v1/partners/{id}/webhooks", h.partnerWebhookCreate)
	mux.HandleFunc("PUT /v1/partners/{id}/commission", h.partnerCommissionSet)
	mux.HandleFunc("GET /v1/partners/{id}/integration", h.partnerIntegration)
}

// partnerOf requires a partner-scoped key; tenant-header and tenant-wide
// key requests get 403 — the partner surface is partner-only so an operator
// credential can never be confused for a tenant credential.
func partnerOf(r *http.Request) (tenantID, partnerID string, err error) {
	partnerID = tenantctx.PartnerFromContext(r.Context())
	if partnerID == "" {
		return "", "", httpx.ErrForbidden
	}
	return tenantOf(r), partnerID, nil
}

func (h *handlers) partnerAssignments(w http.ResponseWriter, r *http.Request) {
	tid, pid, err := partnerOf(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	list, err := h.svc.PartnerOps.AssignmentsFor(r.Context(), tid, pid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) partnerRespond(w http.ResponseWriter, r *http.Request) {
	tid, pid, err := partnerOf(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var body struct {
		Action string `json:"action"` // accept | decline
		Note   string `json:"note,omitempty"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	a, err := h.svc.PartnerOps.Respond(r.Context(), tid, pid, r.PathValue("id"), body.Action, body.Note)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, a)
}

func (h *handlers) partnerSettlement(w http.ResponseWriter, r *http.Request) {
	tid, pid, err := partnerOf(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	res, err := h.svc.PartnerOps.Settlement(r.Context(), tid, pid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// ---- tenant-admin routes ----

func (h *handlers) partnerKeyCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	k, secret, err := h.svc.ApiKeys.CreatePartnerKey(r.Context(), tenantOf(r), r.PathValue("id"), body.Name)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	// Secret is returned exactly once — the console tells the operator to
	// copy it now; only the hash persists.
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"id": k.ID, "name": k.Name, "prefix": k.Prefix, "secret": secret,
		"partner_id": k.PartnerID, "scopes": k.Scopes,
	})
}

func (h *handlers) partnerKeyList(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("id")
	keys, err := h.svc.ApiKeys.List(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	out := []map[string]any{}
	for _, k := range keys {
		if k.PartnerID != pid {
			continue
		}
		out = append(out, map[string]any{
			"id": k.ID, "name": k.Name, "prefix": k.Prefix,
			"revoked": k.Revoked, "created_at": k.CreatedAt, "last_used_at": k.LastUsedAt,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *handlers) partnerWebhookCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	sub, err := h.svc.Webhooks.CreatePartnerSubscription(r.Context(), tenantOf(r), r.PathValue("id"), body.URL, body.Events)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"id": sub.ID, "url": sub.URL, "events": sub.Events, "secret": sub.Secret,
		"partner_id": sub.PartnerID, "active": sub.Active,
	})
}

func (h *handlers) partnerCommissionSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CommissionPct float64 `json:"commission_pct"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	p, err := h.svc.PartnerOps.SetCommissionPct(r.Context(), tenantOf(r), r.PathValue("id"), body.CommissionPct)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

// partnerIntegration is the console's one-call summary for a partner's
// integration state: commission, keys (prefixes only), webhook feeds, and
// the ready-to-paste lead-capture snippet.
func (h *handlers) partnerIntegration(w http.ResponseWriter, r *http.Request) {
	tid := tenantOf(r)
	pid := r.PathValue("id")
	p, err := h.svc.Partners.Get(r.Context(), tid, pid)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	keys, _ := h.svc.ApiKeys.List(r.Context(), tid)
	keyViews := []map[string]any{}
	for _, k := range keys {
		if k.PartnerID == pid {
			keyViews = append(keyViews, map[string]any{
				"id": k.ID, "name": k.Name, "prefix": k.Prefix, "revoked": k.Revoked,
			})
		}
	}
	subs, _ := h.svc.Webhooks.ListSubscriptions(r.Context(), tid)
	subViews := []map[string]any{}
	for _, s := range subs {
		if s.PartnerID == pid {
			subViews = append(subViews, map[string]any{
				"id": s.ID, "url": s.URL, "events": s.Events, "active": s.Active,
			})
		}
	}
	t, terr := h.svc.Tenants.Get(r.Context(), tid)
	slug := ""
	if terr == nil {
		slug = t.Slug
	}
	src := "partner:" + slugify56(p.Name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"partner": p, "keys": keyViews, "webhooks": subViews,
		"capture_endpoint": "/site/" + slug + "/leads?source=" + src,
		"capture_source":   src,
	})
}

// slugify56 lowercases a partner name into a stable source tag
// ("The Little Orange" → "the-little-orange").
func slugify56(s string) string {
	out := make([]rune, 0, len(s))
	lastDash := true
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			out = append(out, r)
			lastDash = false
		case r >= 'A' && r <= 'Z':
			out = append(out, r+32)
			lastDash = false
		case !lastDash:
			out = append(out, '-')
			lastDash = true
		}
	}
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	return string(out)
}
