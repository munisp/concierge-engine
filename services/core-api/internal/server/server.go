// Package server wires the module services to the API surface.
// API-first (Master Spec §1): everything the UI does is an API call.
package server

import (
	"net/http"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
	"concierge-engine/services/core-api/internal/tenantctx"
)

func New(svc *domain.Services) http.Handler {
	mux := http.NewServeMux()
	h := &handlers{svc: svc}

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// platform admin (tenant onboarding)
	mux.HandleFunc("POST /v1/platform/tenants", h.createTenant)
	mux.HandleFunc("GET /v1/platform/tenants", h.listTenants)

	// tenancy & config
	mux.HandleFunc("GET /v1/tenant", h.getTenant)
	mux.HandleFunc("PUT /v1/tenant", h.updateTenant)
	mux.HandleFunc("PUT /v1/tenant/autonomy", h.setAutonomy)

	// identity, CRM & preferences (M6)
	mux.HandleFunc("POST /v1/clients/resolve", h.resolveClient)
	mux.HandleFunc("GET /v1/clients/{id}", h.getClient)
	mux.HandleFunc("PUT /v1/clients/{id}/preferences/{key}", h.savePreference)
	mux.HandleFunc("GET /v1/clients/{id}/preferences", h.listPreferences)
	mux.HandleFunc("DELETE /v1/clients/{id}/preferences", h.deletePreferences) // DSAR

	// catalog & booking (M3)
	mux.HandleFunc("POST /v1/catalog", h.addCatalogService)
	mux.HandleFunc("GET /v1/catalog", h.listCatalog)
	mux.HandleFunc("POST /v1/requests", h.createRequest)
	mux.HandleFunc("GET /v1/requests/{id}", h.getRequest)
	mux.HandleFunc("PATCH /v1/requests/{id}", h.amendRequest)
	mux.HandleFunc("POST /v1/requests/{id}/quote", h.quoteRequest)
	mux.HandleFunc("POST /v1/requests/{id}/transition", h.transitionRequest)
	mux.HandleFunc("GET /v1/requests/{id}/quote/latest", h.latestQuote)
	mux.HandleFunc("POST /v1/quotes/{id}/confirm", h.confirmQuote)

	// payments (M5)
	mux.HandleFunc("POST /v1/payments/links", h.createPaymentLink)
	mux.HandleFunc("GET /v1/payments/intents/{id}", h.getIntent)
	mux.HandleFunc("POST /v1/payments/webhooks/{psp}", h.pspWebhook)
	mux.HandleFunc("GET /v1/payments/postings", h.listPostings)

	// dispatch (M4)
	mux.HandleFunc("POST /v1/dispatch/fleet", h.addFleetResource)
	mux.HandleFunc("GET /v1/dispatch/fleet", h.listFleet)
	mux.HandleFunc("POST /v1/dispatch/crew", h.addCrewMember)
	mux.HandleFunc("GET /v1/dispatch/crew", h.listCrew)
	mux.HandleFunc("POST /v1/dispatch/assignments", h.assign)
	mux.HandleFunc("GET /v1/dispatch/assignments", h.listAssignments)
	mux.HandleFunc("POST /v1/dispatch/assignments/{id}/status", h.updateAssignmentStatus)
	mux.HandleFunc("POST /v1/dispatch/geostamps", h.addGeoStamp)
	mux.HandleFunc("GET /v1/dispatch/stream", h.dispatchStream) // SSE board feed

	// conversations (M2)
	mux.HandleFunc("POST /v1/conversations", h.openConversation)
	mux.HandleFunc("POST /v1/conversations/{id}/messages", h.appendMessage)
	mux.HandleFunc("GET /v1/conversations/{id}/timeline", h.timeline)

	// HITL handoff queues (AI spec §5)
	mux.HandleFunc("POST /v1/handoffs", h.openHandoff)
	mux.HandleFunc("GET /v1/handoffs", h.listOpenHandoffs)
	mux.HandleFunc("POST /v1/handoffs/{id}/resolve", h.resolveHandoff)

	// attribution (M1)
	mux.HandleFunc("POST /v1/attribution", h.trackAttribution)

	return tenantctx.Middleware(mux)
}

type handlers struct{ svc *domain.Services }

func tenantOf(r *http.Request) string { return tenantctx.FromContext(r.Context()) }

// ------------------------------------------------------------- tenants
func (h *handlers) createTenant(w http.ResponseWriter, r *http.Request) {
	var t model.Tenant
	if err := httpx.Decode(r, &t); err != nil {
		httpx.WriteError(w, err)
		return
	}
	created, err := h.svc.Tenants.Create(r.Context(), &t)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, created)
}

func (h *handlers) listTenants(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Tenants.List(r.Context())
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) getTenant(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.Tenants.Get(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, t)
}

func (h *handlers) updateTenant(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.Tenants.Get(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	var patch model.Tenant
	if err := httpx.Decode(r, &patch); err != nil {
		httpx.WriteError(w, err)
		return
	}
	t.Brand = patch.Brand
	t.PricingRules = patch.PricingRules
	t.SLAConfig = patch.SLAConfig
	updated, err := h.svc.Tenants.UpdateConfig(r.Context(), t)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, updated)
}

func (h *handlers) setAutonomy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dial        int    `json:"dial"`
		EvidenceRef string `json:"evidence_ref"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.Tenants.SetAutonomyDial(r.Context(), tenantOf(r), body.Dial, body.EvidenceRef); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"dial": body.Dial})
}

// ------------------------------------------------------------- clients
func (h *handlers) resolveClient(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phone string `json:"phone"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	res, err := h.svc.Clients.Resolve(r.Context(), tenantOf(r), body.Phone, body.Email, body.Name)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *handlers) getClient(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.Clients.Get(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *handlers) savePreference(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Value        string `json:"value"`
		ConsentScope string `json:"consent_scope"`
		SourceQuote  string `json:"source_quote"`
		RecordedBy   string `json:"recorded_by"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	p, err := h.svc.Clients.SavePreference(r.Context(), tenantOf(r), r.PathValue("id"),
		r.PathValue("key"), body.Value, body.ConsentScope, body.SourceQuote, body.RecordedBy)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *handlers) listPreferences(w http.ResponseWriter, r *http.Request) {
	prefs, err := h.svc.Clients.Preferences(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, prefs)
}

func (h *handlers) deletePreferences(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Clients.DeletePreferences(r.Context(), tenantOf(r), r.PathValue("id")); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------- catalog
func (h *handlers) addCatalogService(w http.ResponseWriter, r *http.Request) {
	var c model.CatalogService
	if err := httpx.Decode(r, &c); err != nil {
		httpx.WriteError(w, err)
		return
	}
	c.TenantID = tenantOf(r)
	created, err := h.svc.Catalog.Add(r.Context(), &c)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, created)
}

func (h *handlers) listCatalog(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Catalog.List(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// ------------------------------------------------------------- booking
func (h *handlers) createRequest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientID       string             `json:"client_id"`
		ConversationID string             `json:"conversation_id"`
		Items          []model.ServiceItem `json:"items"`
		IdempotencyKey string             `json:"idempotency_key"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	sr := &model.ServiceRequest{
		TenantID: tenantOf(r), ClientID: body.ClientID, ConversationID: body.ConversationID,
		Items: body.Items, IdempotencyKey: body.IdempotencyKey,
	}
	created, err := h.svc.Booking.CreateRequest(r.Context(), sr)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, created)
}

func (h *handlers) getRequest(w http.ResponseWriter, r *http.Request) {
	sr, err := h.svc.Booking.GetRequest(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sr)
}

func (h *handlers) amendRequest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items          []model.ServiceItem `json:"items"`
		IdempotencyKey string              `json:"idempotency_key"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	sr, err := h.svc.Booking.AmendRequest(r.Context(), tenantOf(r), r.PathValue("id"), body.Items, body.IdempotencyKey)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sr)
}

func (h *handlers) quoteRequest(w http.ResponseWriter, r *http.Request) {
	q, instant, err := h.svc.Booking.QuoteRequest(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if !instant {
		httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
			"origin": "HUMAN", "message": "queued for human quote within SLA",
		})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, q)
}

func (h *handlers) transitionRequest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To model.RequestState `json:"to"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	sr, err := h.svc.Booking.Transition(r.Context(), tenantOf(r), r.PathValue("id"), body.To)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sr)
}

func (h *handlers) latestQuote(w http.ResponseWriter, r *http.Request) {
	q, err := h.svc.Booking.LatestQuote(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, q)
}

func (h *handlers) confirmQuote(w http.ResponseWriter, r *http.Request) {
	b, err := h.svc.Booking.Confirm(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, b)
}

// ------------------------------------------------------------- payments
func (h *handlers) createPaymentLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		QuoteID        string `json:"quote_id"`
		Mode           string `json:"mode"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	intent, err := h.svc.Payments.CreatePaymentLink(r.Context(), tenantOf(r), body.QuoteID, body.Mode, "api", body.IdempotencyKey)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, intent)
}

func (h *handlers) getIntent(w http.ResponseWriter, r *http.Request) {
	intent, err := h.svc.Payments.GetIntent(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, intent)
}

// pspWebhook receives PSP callbacks. Signature verification is configured per
// PSP adapter at the edge; the handler is tenant-scoped via X-Tenant-ID.
func (h *handlers) pspWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IntentID string `json:"intent_id"`
		Event    string `json:"event"`
		PSPRef   string `json:"psp_ref"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.Payments.HandlePSPWebhook(r.Context(), tenantOf(r), body.IntentID, body.Event, body.PSPRef); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "processed"})
}

func (h *handlers) listPostings(w http.ResponseWriter, r *http.Request) {
	postings, err := h.svc.Payments.Postings(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, postings)
}

// ------------------------------------------------------------- dispatch
func (h *handlers) addFleetResource(w http.ResponseWriter, r *http.Request) {
	var f model.FleetResource
	if err := httpx.Decode(r, &f); err != nil {
		httpx.WriteError(w, err)
		return
	}
	f.TenantID = tenantOf(r)
	created, err := h.svc.Dispatch.AddFleetResource(r.Context(), &f)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, created)
}

func (h *handlers) listFleet(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Dispatch.ListFleet(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) addCrewMember(w http.ResponseWriter, r *http.Request) {
	var c model.CrewMember
	if err := httpx.Decode(r, &c); err != nil {
		httpx.WriteError(w, err)
		return
	}
	c.TenantID = tenantOf(r)
	created, err := h.svc.Dispatch.AddCrewMember(r.Context(), &c)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, created)
}

func (h *handlers) listCrew(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Dispatch.ListCrew(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) assign(w http.ResponseWriter, r *http.Request) {
	var a model.Assignment
	if err := httpx.Decode(r, &a); err != nil {
		httpx.WriteError(w, err)
		return
	}
	a.TenantID = tenantOf(r)
	created, err := h.svc.Dispatch.Assign(r.Context(), &a)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, created)
}

func (h *handlers) listAssignments(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Dispatch.ListAssignments(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) updateAssignmentStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.Dispatch.UpdateStatus(r.Context(), tenantOf(r), r.PathValue("id"), body.Status); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": body.Status})
}

func (h *handlers) addGeoStamp(w http.ResponseWriter, r *http.Request) {
	var g model.GeoStamp
	if err := httpx.Decode(r, &g); err != nil {
		httpx.WriteError(w, err)
		return
	}
	g.TenantID = tenantOf(r)
	created, err := h.svc.Dispatch.GeoStamp(r.Context(), &g)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, created)
}

func (h *handlers) dispatchStream(w http.ResponseWriter, r *http.Request) {
	h.svc.Dispatch.Hub().ServeHTTP(w, r, tenantOf(r))
}

// ------------------------------------------------------------- conversations
func (h *handlers) openConversation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientID    string `json:"client_id"`
		Channel     string `json:"channel"`
		ExternalRef string `json:"external_ref"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	c, err := h.svc.Conversations.Open(r.Context(), tenantOf(r), body.ClientID, body.Channel, body.ExternalRef)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, c)
}

func (h *handlers) appendMessage(w http.ResponseWriter, r *http.Request) {
	var m model.Message
	if err := httpx.Decode(r, &m); err != nil {
		httpx.WriteError(w, err)
		return
	}
	m.TenantID = tenantOf(r)
	m.ConversationID = r.PathValue("id")
	created, err := h.svc.Conversations.AppendMessage(r.Context(), &m)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, created)
}

func (h *handlers) timeline(w http.ResponseWriter, r *http.Request) {
	msgs, err := h.svc.Conversations.Timeline(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, msgs)
}

// ------------------------------------------------------------- handoffs
func (h *handlers) openHandoff(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConversationID string         `json:"conversation_id"`
		Trigger        string         `json:"trigger"`
		Reason         string         `json:"reason"`
		PendingAction  map[string]any `json:"pending_action"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	item, err := h.svc.Handoffs.Open(r.Context(), tenantOf(r), body.ConversationID, body.Trigger, body.Reason, body.PendingAction)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, item)
}

func (h *handlers) listOpenHandoffs(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Handoffs.ListOpen(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) resolveHandoff(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Resolution string `json:"resolution"`
		Owner      string `json:"owner"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	item, err := h.svc.Handoffs.Resolve(r.Context(), tenantOf(r), r.PathValue("id"), body.Resolution, body.Owner)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, item)
}

// ------------------------------------------------------------- attribution
func (h *handlers) trackAttribution(w http.ResponseWriter, r *http.Request) {
	var e model.AttributionEvent
	if err := httpx.Decode(r, &e); err != nil {
		httpx.WriteError(w, err)
		return
	}
	e.TenantID = tenantOf(r)
	h.svc.Events.Track(r.Context(), &e)
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "tracked"})
}
