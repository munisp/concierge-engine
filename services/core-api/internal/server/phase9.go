package server

// Phase 15 routes: CRM-360 (profile update, consent, search, merge, 360
// view), international payments (refund, methods discovery, Paystack
// webhook), integration hardening (event catalog, webhook replay / secret
// rotation / test event).

import (
	"encoding/json"
	"io"
	"net/http"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"
)

func (h *handlers) registerPhase9(mux *http.ServeMux) {
	// CRM-360
	mux.HandleFunc("GET /v1/clients", h.searchClients)
	mux.HandleFunc("PATCH /v1/clients/{id}", h.updateClientProfile)
	mux.HandleFunc("PUT /v1/clients/{id}/consent", h.setClientConsent)
	mux.HandleFunc("GET /v1/clients/{id}/360", h.client360)
	mux.HandleFunc("POST /v1/clients/merge", h.mergeClients)
	// International payments
	mux.HandleFunc("POST /v1/payments/intents/{id}/refund", h.refundIntent)
	mux.HandleFunc("GET /v1/payments/methods", h.paymentMethods)
	mux.HandleFunc("POST /v1/payments/paystack/webhook", h.paystackWebhook)
	// Events & 3rd-party integration
	mux.HandleFunc("GET /v1/events/catalog", h.eventCatalog)
	mux.HandleFunc("POST /v1/webhooks/{id}/rotate-secret", h.rotateWebhookSecret)
	mux.HandleFunc("POST /v1/webhooks/{id}/test", h.testWebhook)
	mux.HandleFunc("POST /v1/webhooks/deliveries/{id}/replay", h.replayWebhookDelivery)
}

// ------------------------------------------------------------- CRM-360

func (h *handlers) searchClients(w http.ResponseWriter, r *http.Request) {
	clients, err := h.svc.Clients.Search(r.Context(), tenantOf(r), r.URL.Query().Get("q"), 25)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, clients)
}

func (h *handlers) updateClientProfile(w http.ResponseWriter, r *http.Request) {
	var in domain.UpdateProfileInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	c, err := h.svc.Clients.UpdateProfile(r.Context(), tenantOf(r), r.PathValue("id"), in)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *handlers) setClientConsent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scope   string `json:"scope"`
		Granted bool   `json:"granted"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	c, err := h.svc.Clients.SetConsent(r.Context(), tenantOf(r), r.PathValue("id"), body.Scope, body.Granted)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *handlers) client360(w http.ResponseWriter, r *http.Request) {
	v, err := h.svc.Clients.View360(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, v)
}

func (h *handlers) mergeClients(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WinnerID string `json:"winner_id"`
		LoserID  string `json:"loser_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.Clients.Merge(r.Context(), tenantOf(r), body.WinnerID, body.LoserID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "merged"})
}

// ------------------------------------------------------------- international payments

func (h *handlers) refundIntent(w http.ResponseWriter, r *http.Request) {
	intent, err := h.svc.Payments.Refund(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, intent)
}

func (h *handlers) paymentMethods(w http.ResponseWriter, r *http.Request) {
	methods, err := h.svc.Payments.PaymentMethods(r.URL.Query().Get("currency"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, methods)
}

// paystackWebhook verifies x-paystack-signature (HMAC-SHA512) and routes on
// the signed payload's metadata — the same trust pattern as Stripe ingress.
func (h *handlers) paystackWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	if !h.svc.Paystack.VerifyWebhook(r.Header.Get("x-paystack-signature"), payload) {
		httpx.WriteError(w, httpx.ErrForbidden)
		return
	}
	var ev struct {
		Event string `json:"event"`
		Data  struct {
			Reference string `json:"reference"`
			Metadata  struct {
				TenantID string `json:"tenant_id"`
			} `json:"metadata"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil || ev.Data.Reference == "" {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	if err := h.svc.Payments.HandlePaystackWebhook(r.Context(),
		ev.Data.Metadata.TenantID, ev.Event, ev.Data.Reference); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "processed"})
}

// ------------------------------------------------------------- events & integrations

func (h *handlers) eventCatalog(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, domain.EventCatalog)
}

func (h *handlers) rotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	sub, err := h.svc.Webhooks.RotateSecret(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"id": sub.ID, "secret": sub.Secret,
		"note": "old secret invalid immediately — update your receiver now",
	})
}

func (h *handlers) testWebhook(w http.ResponseWriter, r *http.Request) {
	d, err := h.svc.Webhooks.TestSubscription(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, d)
}

func (h *handlers) replayWebhookDelivery(w http.ResponseWriter, r *http.Request) {
	d, err := h.svc.Webhooks.ReplayDelivery(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}
