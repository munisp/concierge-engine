// Package server wires the module services to the API surface.
// API-first (Master Spec §1): everything the UI does is an API call.
package server

import (
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
	"concierge-engine/services/core-api/internal/tenantctx"
)

func New(svc *domain.Services, optFns ...Option) http.Handler {
	opts := (&Options{}).withDefaults()
	for _, fn := range optFns {
		fn(&opts)
	}
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
	// Signed Stripe webhook: tenant is taken from event metadata (tenant-middleware exempt).
	mux.HandleFunc("POST /v1/payments/stripe/webhook", h.stripeWebhook)
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

	// chauffeur app v1: magic-link trip view (token-authed, no tenant header)
	mux.HandleFunc("GET /v1/trips/{token}", h.getTrip)
	mux.HandleFunc("POST /v1/trips/{token}/status", h.tripAdvance)
	mux.HandleFunc("POST /v1/trips/{token}/geostamps", h.tripGeoStamp)
	mux.HandleFunc("GET /v1/dispatch/assignments/{id}/chauffeur-link", h.chauffeurLink)

	// partner network (Phase 2)
	mux.HandleFunc("POST /v1/partners", h.createPartner)
	mux.HandleFunc("GET /v1/partners", h.listPartners)
	mux.HandleFunc("PUT /v1/partners/{id}/status", h.setPartnerStatus)
	mux.HandleFunc("GET /v1/partners/metrics", h.partnerMetrics)

	// corporate accounts & consolidated invoicing (Phase 2)
	mux.HandleFunc("POST /v1/corporate/accounts", h.createCorporateAccount)
	mux.HandleFunc("GET /v1/corporate/accounts", h.listCorporateAccounts)
	mux.HandleFunc("POST /v1/corporate/accounts/{id}/invoices", h.generateInvoice)
	mux.HandleFunc("GET /v1/corporate/accounts/{id}/invoices", h.listInvoices)
	mux.HandleFunc("POST /v1/corporate/invoices/{id}/mark-paid", h.markInvoicePaid)

	// partner payouts via Mojaloop saga (Phase 2, Master Spec section 11.3)
	mux.HandleFunc("POST /v1/payouts", h.createPayout)
	mux.HandleFunc("GET /v1/payouts", h.listPayouts)
	mux.HandleFunc("GET /v1/payouts/{id}", h.getPayout)
	mux.HandleFunc("POST /v1/payouts/{id}/quote", h.quotePayout)
	mux.HandleFunc("POST /v1/payouts/{id}/execute", h.executePayout)
	mux.HandleFunc("POST /v1/payouts/{id}/compensate", h.compensatePayout)
	mux.HandleFunc("POST /v1/payouts/callbacks/mojaloop", h.payoutCallback)

	// sensitive-read audit log (Master Spec section 13)
	mux.HandleFunc("GET /v1/audit", h.listAudit)

	// charter supply adapters (Phase 2: Avinode; mock fallback in dev)
	mux.HandleFunc("GET /v1/supply/charter/search", h.charterSearch)

	// platform: outbox feed for the lakehouse bronze landing (Phase 2)
	mux.HandleFunc("GET /v1/platform/outbox/unpublished", h.outboxUnpublished)
	mux.HandleFunc("POST /v1/platform/outbox/published", h.outboxMarkPublished)

	// Phase 3: public API keys, webhooks marketplace, referrals, GDS, instant-on
	h.registerPhase3(mux)

	// Phase 4: case management (CRM stays the identity spine; cases are the work)
	h.registerPhase4(mux)
	h.registerPhase6(mux)
	h.registerPhase7(mux)
	h.registerPhase8(mux)
	h.registerPhase9(mux)
	h.registerPhase10(mux)
	h.registerPhase11(mux)
	h.registerPhase12(mux)
	h.registerPhase13(mux)
	h.registerPhase14(mux)
	h.registerPhase15(mux)
	h.registerPhase16(mux)
	h.registerSite(mux)
	h.registerStudio(mux)
	h.registerMedia(mux)
	h.registerPhase17(mux)
	h.registerPhase35(mux)
	h.registerPhase36(mux)
	h.registerPhase37(mux)
	h.registerPhase39(mux)
	h.registerPhase40(mux)
	h.registerPhase41(mux)
	h.registerPhase43(mux)
	h.registerPhase44(mux)
	h.registerPhase45(mux)
	h.registerPhase46(mux)
	h.registerPhase47(mux)
	h.registerPhase48(mux)
	h.registerPhase49(mux)
	h.registerPhase53(mux)
	h.registerPhase56(mux)
	h.registerPhase57(mux)
	h.registerPhase55(mux)

	// API-key auth: "Bearer cek_..." resolves tenant + scopes (Phase 3 public API).
	tenantctx.SetKeyAuthenticator(svc.ApiKeys.AuthenticateTenant)

	// Phase 28: custom-domain resolution sits outermost so host-rewritten
	// paths route through the mux (and tenantctx) exactly like native ones.
	// Phase 50: security headers wrap everything; the rate limiter runs
	// outside tenantctx so auth-rejected floods also burn their budget
	// (bucket from X-Tenant-ID / bearer hash / source IP).
	return securityHeaders(h.hostRewrite(rateLimit(tenantctx.Middleware(mux), opts)))
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
	// Selective patch semantics (Phase 24): only fields present in the body
	// are applied. Decoding into model.Tenant and copying wholesale would
	// wipe every config the caller didn't send — a Settings page editing the
	// operator roster must not zero the tenant's tax engine.
	var patch struct {
		Brand             *map[string]any          `json:"brand"`
		PricingRules      *model.PricingRules      `json:"pricing_rules"`
		SLAConfig         *model.SLAConfig         `json:"sla_config"`
		TaxConfig         *model.TaxConfig         `json:"tax_config"`
		PaymentConfig     *model.PaymentConfig     `json:"payment_config"`
		RewardProgram     *model.RewardProgram     `json:"reward_program"`
		OutreachConfig    *model.OutreachConfig    `json:"outreach_config"`
		OperatorConfig    *model.OperatorConfig    `json:"operator_config"`
		DispatchConfig    *model.DispatchConfig    `json:"dispatch_config"`
		LifecycleConfig   *model.LifecycleConfig   `json:"lifecycle_config"`
		AcquisitionConfig *model.AcquisitionConfig `json:"acquisition_config"`
		OnboardingConfig  *model.OnboardingConfig  `json:"onboarding_config"`
		SiteConfig        *model.SiteConfig        `json:"site_config"`
		RetentionConfig   *model.RetentionConfig   `json:"retention_config"`
		KycConfig         *model.KycConfig         `json:"kyc_config"`
	}
	if err := httpx.Decode(r, &patch); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if patch.Brand != nil {
		t.Brand = *patch.Brand
	}
	if patch.PricingRules != nil {
		t.PricingRules = *patch.PricingRules
	}
	if patch.SLAConfig != nil {
		t.SLAConfig = *patch.SLAConfig
	}
	if patch.TaxConfig != nil {
		t.TaxConfig = *patch.TaxConfig
	}
	if patch.PaymentConfig != nil {
		t.PaymentConfig = *patch.PaymentConfig
	}
	if patch.RewardProgram != nil {
		t.RewardProgram = *patch.RewardProgram
	}
	if patch.OutreachConfig != nil {
		t.OutreachConfig = *patch.OutreachConfig
	}
	if patch.OperatorConfig != nil {
		t.OperatorConfig = *patch.OperatorConfig
	}
	if patch.DispatchConfig != nil {
		t.DispatchConfig = *patch.DispatchConfig
	}
	if patch.LifecycleConfig != nil {
		t.LifecycleConfig = *patch.LifecycleConfig
	}
	if patch.AcquisitionConfig != nil {
		t.AcquisitionConfig = *patch.AcquisitionConfig
	}
	if patch.OnboardingConfig != nil {
		t.OnboardingConfig = *patch.OnboardingConfig
	}
	if patch.SiteConfig != nil {
		// normalize domains at the edge: lowercase, strip scheme/www/port
		sc := *patch.SiteConfig
		for i, d := range sc.CustomDomains {
			d = strings.ToLower(strings.TrimSpace(d))
			d = strings.TrimPrefix(d, "https://")
			d = strings.TrimPrefix(d, "http://")
			d = strings.TrimPrefix(d, "www.")
			d = strings.TrimSuffix(d, "/")
			sc.CustomDomains[i] = d
		}
		// Phase 32: validate theme + design fields on this write path too —
		// the studio endpoint validated, but this wholesale patch bypassed it.
		if err := domain.ValidateSiteConfig(&sc); err != nil {
			httpx.WriteError(w, err)
			return
		}
		t.SiteConfig = sc
	}
	if patch.RetentionConfig != nil {
		t.RetentionConfig = *patch.RetentionConfig
	}
	if patch.KycConfig != nil {
		t.KycConfig = *patch.KycConfig
	}
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
	var body domain.ResolveInput
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	res, err := h.svc.Clients.ResolveV2(r.Context(), tenantOf(r), body)
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
		ClientID       string              `json:"client_id"`
		ConversationID string              `json:"conversation_id"`
		Items          []model.ServiceItem `json:"items"`
		IdempotencyKey string              `json:"idempotency_key"`
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
	var in domain.CreatePaymentLinkInput
	if err := httpx.Decode(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	intent, err := h.svc.Payments.CreatePaymentLinkV2(r.Context(), tenantOf(r), in)
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

// stripeWebhook ingests signed Stripe events. The signature is verified
// against the raw body before anything is parsed; the tenant is routed from
// the metadata CreateCheckoutSession attached, so a forged tenant header can
// never redirect settlement.
func (h *handlers) stripeWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	if !domain.VerifyStripeSignature(r.Header.Get("Stripe-Signature"), payload,
		h.svc.Stripe.WebhookSecret(), time.Now()) {
		httpx.WriteError(w, httpx.ErrForbidden)
		return
	}
	ev, err := domain.ParseStripeEvent(payload)
	if err != nil || ev.TenantID == "" {
		httpx.WriteError(w, httpx.ErrValidation)
		return
	}
	switch ev.Type {
	case "checkout.session.completed":
		if err := h.svc.Payments.HandlePSPWebhook(r.Context(), ev.TenantID, ev.IntentID,
			"checkout.session.completed", ev.SessionID); err != nil {
			httpx.WriteError(w, err)
			return
		}
	default:
		// Unknown event kinds are acknowledged, not processed (forward-compat).
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
	// Phase 35 (A7): Referer auto-attribution — when the caller supplied no
	// channel, classify the HTTP Referer so organic social/search arrivals
	// on the capture page still attribute, without any client-side tagging.
	if e.Channel == "" {
		ch, camp := refererAttribution(r.Header.Get("Referer"))
		e.Channel = ch
		if e.Campaign == "" {
			e.Campaign = camp
		}
	}
	h.svc.Events.Track(r.Context(), &e)
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "tracked"})
}

// ------------------------------------------------------------- chauffeur trips (v1)
func (h *handlers) getTrip(w http.ResponseWriter, r *http.Request) {
	view, err := h.svc.Trips.View(r.Context(), r.PathValue("token"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *handlers) tripAdvance(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.Trips.Advance(r.Context(), r.PathValue("token"), body.Status); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": body.Status})
}

func (h *handlers) tripGeoStamp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind     string  `json:"kind"`
		Lat      float64 `json:"lat"`
		Lng      float64 `json:"lng"`
		Accuracy float64 `json:"accuracy_m"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.Trips.Stamp(r.Context(), r.PathValue("token"), body.Kind, body.Lat, body.Lng, body.Accuracy); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"status": "stamped"})
}

func (h *handlers) chauffeurLink(w http.ResponseWriter, r *http.Request) {
	token := h.svc.Trips.TokenFor(tenantOf(r), r.PathValue("id"))
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"token": token,
		"url":   "/chauffeur?token=" + token, // served by web/console (PWA)
	})
}

// ------------------------------------------------------------- partners (Phase 2)
func (h *handlers) createPartner(w http.ResponseWriter, r *http.Request) {
	var p model.Partner
	if err := httpx.Decode(r, &p); err != nil {
		httpx.WriteError(w, err)
		return
	}
	p.TenantID = tenantOf(r)
	created, err := h.svc.Partners.Create(r.Context(), &p)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, created)
}

func (h *handlers) listPartners(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Partners.List(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) setPartnerStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	p, err := h.svc.Partners.SetStatus(r.Context(), tenantOf(r), r.PathValue("id"), body.Status)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *handlers) partnerMetrics(w http.ResponseWriter, r *http.Request) {
	m, err := h.svc.Partners.Metrics(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, m)
}

// ------------------------------------------------------------- corporate (Phase 2)
func (h *handlers) createCorporateAccount(w http.ResponseWriter, r *http.Request) {
	var a model.CorporateAccount
	if err := httpx.Decode(r, &a); err != nil {
		httpx.WriteError(w, err)
		return
	}
	a.TenantID = tenantOf(r)
	created, err := h.svc.Corporate.CreateAccount(r.Context(), &a)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, created)
}

func (h *handlers) listCorporateAccounts(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Corporate.ListAccounts(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) generateInvoice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PeriodStart    time.Time `json:"period_start"`
		PeriodEnd      time.Time `json:"period_end"`
		IdempotencyKey string    `json:"idempotency_key"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	inv, err := h.svc.Corporate.GenerateInvoice(r.Context(), tenantOf(r), r.PathValue("id"),
		body.PeriodStart, body.PeriodEnd, body.IdempotencyKey)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, inv)
}

func (h *handlers) listInvoices(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Corporate.ListInvoices(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) markInvoicePaid(w http.ResponseWriter, r *http.Request) {
	inv, err := h.svc.Corporate.MarkInvoicePaid(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, inv)
}

// ------------------------------------------------------------- payouts (Phase 2)
func (h *handlers) createPayout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PartnerID      string `json:"partner_id"`
		BookingID      string `json:"booking_id"`
		AmountMinor    int64  `json:"amount_minor"`
		Currency       string `json:"currency"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	p, err := h.svc.Payouts.Create(r.Context(), tenantOf(r), body.PartnerID, body.BookingID,
		body.AmountMinor, body.Currency, body.IdempotencyKey)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, p)
}

func (h *handlers) getPayout(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Payouts.Get(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *handlers) listPayouts(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Payouts.List(r.Context(), tenantOf(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *handlers) quotePayout(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Payouts.Quote(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *handlers) executePayout(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Payouts.Execute(r.Context(), tenantOf(r), r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *handlers) compensatePayout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	p, err := h.svc.Payouts.Compensate(r.Context(), tenantOf(r), r.PathValue("id"), body.Reason)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

// payoutCallback: Mojaloop adapter notifications (FSPIOP fulfil/abort/timeout,
// normalized by the Rust adapter). Idempotent via absorbing saga states.
//
// Phase 48: when CE_PAYOUT_CALLBACK_SECRET is configured the adapter must
// present it as X-Callback-Secret — an unauthenticated callback endpoint is
// a "settle anything" primitive. Routing: the transfer id decides which saga
// owns the transfer — single payout first, settlement batch on miss.
func (h *handlers) payoutCallback(w http.ResponseWriter, r *http.Request) {
	if secret := h.svc.PayoutCallbackSecret; secret != "" {
		got := r.Header.Get("X-Callback-Secret")
		if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
			httpx.WriteError(w, httpx.ErrForbidden)
			return
		}
	}
	var body struct {
		TransferID string `json:"transfer_id"`
		EventID    string `json:"event_id"`
		Kind       string `json:"kind"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	p, err := h.svc.Payouts.HandleCallback(r.Context(), tenantOf(r), body.TransferID, body.Kind)
	if err == nil {
		httpx.WriteJSON(w, http.StatusOK, p)
		return
	}
	if !errors.Is(err, httpx.ErrNotFound) {
		httpx.WriteError(w, err)
		return
	}
	// Not a single payout — try the batch saga.
	b, berr := h.svc.Settlements.HandleBatchCallback(r.Context(), tenantOf(r), body.TransferID, body.Kind)
	if berr != nil {
		httpx.WriteError(w, berr)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, b)
}

// ------------------------------------------------------------- audit (Spec section 13)
func (h *handlers) listAudit(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Audit.List(r.Context(), tenantOf(r), 200)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// ------------------------------------------------------------- charter supply (Phase 2)
func (h *handlers) charterSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pax, _ := strconv.Atoi(q.Get("passengers"))
	if pax <= 0 {
		pax = 2
	}
	date := time.Now()
	if d := q.Get("date"); d != "" {
		if parsed, err := time.Parse("2006-01-02", d); err == nil {
			date = parsed
		}
	}
	options, err := h.svc.Supply.SearchCharter(r.Context(), tenantOf(r),
		q.Get("from"), q.Get("to"), date, pax)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, options)
}

// ------------------------------------------------------------- platform outbox feed
func (h *handlers) outboxUnpublished(w http.ResponseWriter, r *http.Request) {
	events, err := h.svc.Store.Outbox().Unpublished(r.Context(), 500)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, events)
}

func (h *handlers) outboxMarkPublished(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.Store.Outbox().MarkPublished(r.Context(), body.IDs); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "published"})
}
