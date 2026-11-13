// Phase 3 API surface (Master Spec §14): public API keys, the webhooks
// marketplace, the cross-tenant referral network, GDS supply (Amadeus NDC
// seed), new-entrant instant-on, and the residency report.
package server

import (
	"net/http"
	"strconv"

	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase3(mux *http.ServeMux) {
	// public API keys (secret returned once at creation)
	mux.HandleFunc("POST /v1/apikeys", h.createApiKey)
	mux.HandleFunc("GET /v1/apikeys", h.listApiKeys)
	mux.HandleFunc("DELETE /v1/apikeys/{id}", h.revokeApiKey)

	// webhooks marketplace
	mux.HandleFunc("POST /v1/webhooks", h.createWebhookSub)
	mux.HandleFunc("GET /v1/webhooks", h.listWebhookSubs)
	mux.HandleFunc("PUT /v1/webhooks/{id}", h.setWebhookActive)
	mux.HandleFunc("GET /v1/webhooks/deliveries", h.listWebhookDeliveries)

	// cross-tenant referral network
	mux.HandleFunc("POST /v1/referrals", h.offerReferral)
	mux.HandleFunc("GET /v1/referrals", h.listReferrals)
	mux.HandleFunc("GET /v1/referrals/metrics", h.referralMetrics)
	mux.HandleFunc("POST /v1/referrals/{id}/accept", h.acceptReferral)
	mux.HandleFunc("POST /v1/referrals/{id}/decline", h.declineReferral)
	mux.HandleFunc("POST /v1/referrals/{id}/settle", h.settleReferral)

	// GDS supply: Amadeus NDC seed (live when credentialed, mock otherwise)
	mux.HandleFunc("GET /v1/gds/flights", h.searchFlights)
	mux.HandleFunc("GET /v1/gds/hotels", h.searchHotels)
	mux.HandleFunc("POST /v1/gds/attach", h.attachOffer)

	// new-entrant instant-on (platform admin) + residency report
	mux.HandleFunc("POST /v1/platform/tenants/instant-on", h.instantOn)
	mux.HandleFunc("GET /v1/tenant/residency", h.residencyReport)
}

// ------------------------------------------------------------- API keys
func (h *handlers) createApiKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	key, secret, err := h.svc.ApiKeys.Create(r.Context(), tenantOf(r), body.Name, body.Scopes)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	// The secret is returned exactly once; only its hash persists.
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"key": key, "secret": secret})
}

func (h *handlers) listApiKeys(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ApiKeys.List(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) revokeApiKey(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.ApiKeys.Revoke(r.Context(), tenantOf(r), r.PathValue("id")); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------- webhooks
func (h *handlers) createWebhookSub(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	sub, err := h.svc.Webhooks.CreateSubscription(r.Context(), tenantOf(r), body.URL, body.Events)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	// Signing secret returned once (list/get responses never include it).
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"subscription": sub, "secret": sub.Secret})
}

func (h *handlers) listWebhookSubs(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Webhooks.ListSubscriptions(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) setWebhookActive(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Active bool `json:"active"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.Webhooks.SetActive(r.Context(), tenantOf(r), r.PathValue("id"), body.Active); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"active": body.Active})
}

func (h *handlers) listWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := h.svc.Webhooks.ListDeliveries(r.Context(), tenantOf(r), limit)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// ------------------------------------------------------------- referrals
func (h *handlers) offerReferral(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ReceivingTenantID string `json:"receiving_tenant_id"`
		ClientName        string `json:"client_name"`
		ClientContact     string `json:"client_contact"`
		RequestSummary    string `json:"request_summary"`
		EstValueMinor     int64  `json:"est_value_minor"`
		Currency          string `json:"currency"`
		IdempotencyKey    string `json:"idempotency_key"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	ref, err := h.svc.Referrals.Offer(r.Context(), tenantOf(r), body.ReceivingTenantID,
		body.ClientName, body.ClientContact, body.RequestSummary,
		body.EstValueMinor, body.Currency, body.IdempotencyKey)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, ref)
}

func (h *handlers) listReferrals(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Referrals.List(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) referralMetrics(w http.ResponseWriter, r *http.Request) {
	m, err := h.svc.Referrals.Metrics(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, m)
}

func (h *handlers) acceptReferral(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RequestID string `json:"request_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	ref, err := h.svc.Referrals.Accept(r.Context(), tenantOf(r), r.PathValue("id"), body.RequestID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ref)
}

func (h *handlers) declineReferral(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	ref, err := h.svc.Referrals.Decline(r.Context(), tenantOf(r), r.PathValue("id"), body.Reason)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ref)
}

func (h *handlers) settleReferral(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BookingID         string `json:"booking_id"`
		BookingTotalMinor int64  `json:"booking_total_minor"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	ref, err := h.svc.Referrals.Settle(r.Context(), tenantOf(r), r.PathValue("id"),
		body.BookingID, body.BookingTotalMinor)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ref)
}

// ------------------------------------------------------------- GDS supply
func (h *handlers) searchFlights(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	adults, _ := strconv.Atoi(q.Get("adults"))
	if adults <= 0 {
		adults = 1
	}
	offers, source, err := h.svc.GDS.SearchFlights(r.Context(), tenantOf(r),
		q.Get("origin"), q.Get("dest"), q.Get("date"), adults)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"offers": offers, "source": source})
}

func (h *handlers) searchHotels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	adults, _ := strconv.Atoi(q.Get("adults"))
	if adults <= 0 {
		adults = 1
	}
	offers, source, err := h.svc.GDS.SearchHotels(r.Context(), tenantOf(r),
		q.Get("city"), q.Get("check_in"), q.Get("check_out"), adults)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"offers": offers, "source": source})
}

func (h *handlers) attachOffer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BookingID  string `json:"booking_id"`
		OfferType  string `json:"offer_type"` // flight | hotel
		OfferRef   string `json:"offer_ref"`
		TotalMinor int64  `json:"total_minor"`
		Currency   string `json:"currency"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.GDS.AttachOffer(r.Context(), tenantOf(r), body.BookingID,
		body.OfferType, body.OfferRef, body.TotalMinor, body.Currency); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"status": "attached"})
}

// ------------------------------------------------------------- instant-on & residency
func (h *handlers) instantOn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Slug     string `json:"slug"`
		Region   string `json:"region"`
		Currency string `json:"currency"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	t, seeded, err := h.svc.InstantOn.Provision(r.Context(), body.Name, body.Slug, body.Region, body.Currency)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"tenant": t, "catalog_seeded": seeded})
}

// residencyReport is the tenant-facing data-residency statement (§13): which
// region their data lives in and what that implies.
func (h *handlers) residencyReport(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.Tenants.Get(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	homes := map[string]map[string]string{
		"EU":  {"primary_region": "eu-west-1 (Netherlands)", "backup_region": "eu-central-1 (Germany)", "framework": "GDPR"},
		"UAE": {"primary_region": "me-central-1 (UAE)", "backup_region": "eu-west-1 (Netherlands)", "framework": "UAE PDPL"},
	}
	home := homes[t.Region]
	if home == nil {
		home = homes["EU"]
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"tenant_id": t.ID, "region": t.Region, "residency": home,
		"data_classes": []string{"clients", "bookings", "payments", "conversations", "audit"},
	})
}
