// Booking service: the ServiceRequest state machine, instant quoting,
// versioned amendments and quote->booking conversion (M3, FR-B1..B6).
package domain

import (
	"context"
	"fmt"
	"math"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type BookingService struct{ s *Services }

// allowedTransitions is the state machine (AI spec §2.2). ESCALATED re-enters
// at any state with full context.
var allowedTransitions = map[model.RequestState][]model.RequestState{
	model.StateNew:             {model.StateCapturing, model.StateEscalated, model.StateCancelled},
	model.StateCapturing:       {model.StateQuoting, model.StateEscalated, model.StateCancelled},
	model.StateQuoting:         {model.StateNegotiating, model.StateAwaitingPayment, model.StateEscalated, model.StateCancelled},
	model.StateNegotiating:     {model.StateQuoting, model.StateAwaitingPayment, model.StateEscalated, model.StateCancelled},
	model.StateAwaitingPayment: {model.StateConfirmed, model.StateEscalated, model.StateCancelled},
	model.StateConfirmed:       {model.StateInService, model.StateEscalated, model.StateCancelled},
	model.StateInService:       {model.StateCompleted, model.StateEscalated},
	model.StateCompleted:       {model.StateFollowUp},
	model.StateFollowUp:        {model.StateCapturing}, // one-tap rebooking
	model.StateEscalated: { // re-enter at any state with full context
		model.StateCapturing, model.StateQuoting, model.StateNegotiating,
		model.StateAwaitingPayment, model.StateConfirmed, model.StateCancelled,
	},
}

func (svc *BookingService) canTransition(from, to model.RequestState) bool {
	for _, s := range allowedTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// CreateRequest creates the atomic demand object (idempotent on key).
func (svc *BookingService) CreateRequest(ctx context.Context, r *model.ServiceRequest) (*model.ServiceRequest, error) {
	if r.ClientID == "" || len(r.Items) == 0 || r.IdempotencyKey == "" {
		return nil, httpx.ErrValidation
	}
	// Phase 63: source vocabulary is closed; a sourced request must name
	// its channel partner, a direct one must not.
	if r.SourceKind == "" {
		r.SourceKind = model.SourceDirect
	}
	if !model.ValidSourceKind(r.SourceKind) {
		return nil, httpx.ErrValidation
	}
	if r.SourceKind == model.SourceDirect && r.SourcePartnerID != "" {
		return nil, httpx.ErrValidation
	}
	if r.SourceKind != model.SourceDirect && r.SourcePartnerID == "" {
		return nil, httpx.ErrValidation
	}
	if r.ID == "" {
		r.ID = NewID()
	}
	r.State = model.StateCapturing
	r.Version = 1
	now := svc.s.Now()
	r.CreatedAt, r.UpdatedAt = now, now
	for i := range r.Items {
		if r.Items[i].ID == "" {
			r.Items[i].ID = NewID()
		}
		r.Items[i].TenantID = r.TenantID
		r.Items[i].RequestID = r.ID
	}
	if err := svc.s.Store.Requests().Create(ctx, r); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, r.TenantID, "service_request", r.ID, "request.created",
		map[string]any{"state": string(r.State), "items": len(r.Items)})
	// Phase 14: inherit first-touch channel/campaign so the funnel attributes
	// demand to the acquisition source, not just the conversation.
	svc.s.TrackInherited(ctx, &model.AttributionEvent{
		TenantID: r.TenantID, Kind: "request_created", ClientID: r.ClientID, RequestID: r.ID,
	})
	return r, nil
}

func (svc *BookingService) GetRequest(ctx context.Context, tenantID, id string) (*model.ServiceRequest, error) {
	return svc.s.Store.Requests().Get(ctx, tenantID, id)
}

// AmendRequest applies a versioned amendment (FR-B3).
func (svc *BookingService) AmendRequest(ctx context.Context, tenantID, id string, items []model.ServiceItem, idem string) (*model.ServiceRequest, error) {
	r, err := svc.s.Store.Requests().Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if r.State == model.StateCompleted || r.State == model.StateCancelled || r.State == model.StateConfirmed {
		return nil, httpx.ErrConflict // confirmed bookings amend through the amendment workflow
	}
	r.Items = items
	for i := range r.Items {
		if r.Items[i].ID == "" {
			r.Items[i].ID = NewID()
		}
		r.Items[i].TenantID = tenantID
		r.Items[i].RequestID = r.ID
	}
	r.Version++
	r.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.Requests().Update(ctx, r); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "service_request", r.ID, "request.amended",
		map[string]any{"version": r.Version})
	return r, nil
}

func (svc *BookingService) Transition(ctx context.Context, tenantID, id string, to model.RequestState) (*model.ServiceRequest, error) {
	r, err := svc.s.Store.Requests().Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if !svc.canTransition(r.State, to) {
		return nil, httpx.ErrConflict
	}
	from := r.State
	r.State = to
	r.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.Requests().Update(ctx, r); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "service_request", r.ID, "request.state_changed",
		map[string]any{"from": string(from), "to": string(to)})
	return r, nil
}

// QuoteRequest runs the instant-quote engine (FR-B2). Shapes that are not
// auto-quotable fall back to human quoting with an SLA timer — the quote SLA
// clock is emitted as an event consumed by the escalation timers.
func (svc *BookingService) QuoteRequest(ctx context.Context, tenantID, requestID string) (*model.Quote, bool, error) {
	tenant, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, false, err
	}
	r, err := svc.s.Store.Requests().Get(ctx, tenantID, requestID)
	if err != nil {
		return nil, false, err
	}
	catalog, err := svc.s.Store.Catalog().List(ctx, tenantID)
	if err != nil {
		return nil, false, err
	}
	lines, total, deposit, currency, instant := BuildQuote(*tenant, catalog, r, svc.s.Now())

	// Phase 17: tax. Applied to the service subtotal per the tenant's tax
	// posture; the deposit base is the grand total (the client pays tax on
	// the deposit too). The client's locale feeds Stripe Tax's country
	// signal; engine provenance is stored on the quote.
	clientLocale := ""
	if r.ClientID != "" {
		if cl, err := svc.s.Store.Clients().Get(ctx, tenantID, r.ClientID); err == nil {
			clientLocale = cl.Locale
		}
	}
	taxLine, taxMinor, taxEngine := svc.applyTax(ctx, tenant, total, currency, clientLocale)
	if taxLine != nil {
		lines = append(lines, *taxLine)
		total += taxMinor
		if tenant.PricingRules.DepositPct > 0 {
			deposit = int64(math.Round(float64(total) * tenant.PricingRules.DepositPct))
		}
	}

	// Value-based routing (AI spec §4.1): requests above the TIER-2 limit
	// escalate to a human owner — AI drafts, human owns.
	if total > tenant.Tier2LimitMinor {
		instant = false
	}
	if !instant {
		svc.s.Events.Emit(ctx, tenantID, "service_request", requestID, "quote.human_required",
			map[string]any{"sla_seconds": tenant.SLAConfig.QuoteSLASeconds})
		return nil, false, nil
	}
	// Advance the state machine to QUOTING before pricing — a fresh request
	// sits in CAPTURING; re-quotes may arrive from NEGOTIATING.
	switch r.State {
	case model.StateNew:
		if _, err := svc.Transition(ctx, tenantID, r.ID, model.StateCapturing); err != nil {
			return nil, false, err
		}
		if _, err := svc.Transition(ctx, tenantID, r.ID, model.StateQuoting); err != nil {
			return nil, false, err
		}
	case model.StateCapturing, model.StateNegotiating:
		if _, err := svc.Transition(ctx, tenantID, r.ID, model.StateQuoting); err != nil {
			return nil, false, err
		}
	}
	q := &model.Quote{
		ID: NewID(), TenantID: tenantID, RequestID: requestID, Version: 1,
		Origin: "INSTANT", TotalMinor: total, DepositMinor: deposit, Currency: currency,
		TaxMinor: taxMinor, TaxEngine: taxEngine,
		Lines: lines, ExpiresAt: QuoteExpiry(tenant.PricingRules, svc.s.Now()), CreatedAt: svc.s.Now(),
	}
	if err := svc.s.Store.Quotes().Create(ctx, q); err != nil {
		return nil, false, err
	}
	if r.State != model.StateAwaitingPayment {
		if _, err := svc.Transition(ctx, tenantID, requestID, model.StateAwaitingPayment); err != nil {
			return nil, false, err
		}
	}
	svc.s.Events.Emit(ctx, tenantID, "quote", q.ID, "quote.created",
		map[string]any{"total_minor": total, "currency": currency, "origin": "INSTANT",
			"tax_minor": taxMinor, "tax_engine": taxEngine})
	return q, true, nil
}

// Confirm converts a paid/approved quote into a booking (one price, one
// payment, one timeline).
func (svc *BookingService) Confirm(ctx context.Context, tenantID, quoteID string) (*model.Booking, error) {
	q, err := svc.s.Store.Quotes().Get(ctx, tenantID, quoteID)
	if err != nil {
		return nil, err
	}
	if svc.s.Now().After(q.ExpiresAt) {
		return nil, httpx.ErrConflict // expired quote
	}
	// Phase 47: KYC gate — high-value quotes require the client to hold
	// the verification level the tenant's KycConfig demands. Fail-CLOSED:
	// a gate that cannot be evaluated blocks the booking, because a
	// compliance control that degrades open is not a control. When the
	// gate fires, an operator work item is auto-opened (advisory) so the
	// verification queue shows the blocked client immediately.
	req, rerr := svc.s.Store.Requests().Get(ctx, tenantID, q.RequestID)
	if rerr == nil && req.ClientID != "" {
		required, ok, gerr := svc.s.Kyc.GateCheck(ctx, tenantID, req.ClientID, q.TotalMinor)
		if gerr != nil {
			return nil, fmt.Errorf("kyc gate: %w", gerr)
		}
		if !ok {
			// Advisory: surface the work item. A pre-existing open case
			// conflicts harmlessly; any failure leaves the gate error
			// unchanged.
			_, _ = svc.s.Kyc.Open(ctx, tenantID, req.ClientID, required, "auto:"+req.ClientID+":"+required)
			return nil, fmt.Errorf("%w: kyc_required: %s identity verification is required before this booking can confirm", httpx.ErrConflict, required)
		}
	}
	b := &model.Booking{
		ID: NewID(), TenantID: tenantID, RequestID: q.RequestID, QuoteID: q.ID,
		State: "CONFIRMED", CreatedAt: svc.s.Now(),
	}
	// Phase 63: the demand channel travels from request onto the booking
	// so margin reporting never joins back.
	if rerr == nil && req.SourceKind != "" {
		b.SourceKind, b.SourcePartnerID = req.SourceKind, req.SourcePartnerID
	} else {
		b.SourceKind = model.SourceDirect
	}
	if err := svc.s.Store.Bookings().Create(ctx, b); err != nil {
		return nil, err
	}
	// Phase 63: the sourcing channel earns its commission at confirm —
	// failure is audit-logged inside, never blocks the booking.
	svc.s.Channels.AccrueSourcingCommission(ctx, tenantID, b)
	if _, err := svc.Transition(ctx, tenantID, q.RequestID, model.StateConfirmed); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "booking", b.ID, "booking.confirmed",
		map[string]any{"quote_id": q.ID, "total_minor": q.TotalMinor})
	// Phase 59: pre-render the rider's signature soundscape off the request
	// path — a provider render takes 30-90s, the confirm response never
	// waits. Honest degrade inside (no provider → curated fallback row).
	svc.s.Music.OnConfirmedDetached(ctx, tenantID, b.ID)
	// Phase 14: referral conversion runs BEFORE the booking is tracked so
	// first-booking detection doesn't count this booking itself; the booking
	// event then inherits first-touch channel/campaign.
	if rerr == nil {
		r := req
		svc.s.ReferralConversion(ctx, tenantID, r.ClientID, b, q.TotalMinor, q.Currency)
		// Phase 35 (A8): fixed first-booking welcome credit — runs after
		// the booking row exists, so first-booking detection counts it.
		svc.s.firstBookingCredit(ctx, tenantID, r.ClientID, b, q.Currency)
		svc.s.TrackInherited(ctx, &model.AttributionEvent{
			TenantID: tenantID, Kind: "booking", ClientID: r.ClientID,
			RequestID: q.RequestID, ValueMinor: q.TotalMinor,
		})
	} else {
		svc.s.Events.Track(ctx, &model.AttributionEvent{
			TenantID: tenantID, Kind: "booking", RequestID: q.RequestID, ValueMinor: q.TotalMinor,
		})
	}
	return b, nil
}

// LatestQuote returns the most recent quote version for a request.
func (svc *BookingService) LatestQuote(ctx context.Context, tenantID, requestID string) (*model.Quote, error) {
	return svc.s.Store.Quotes().LatestForRequest(ctx, tenantID, requestID)
}
