// Cross-tenant referral network (Phase 3 — "supply density and network
// effects"). A tenant that cannot serve a request (coverage gap, fleet
// saturation) offers it to an opted-in tenant; the receiving tenant runs the
// normal quote→booking flow, and on confirmation the referral commission
// settles through the ledger. Consent is explicit per tenant
// (AcceptsReferrals, §13 cross-tenant opt-in).
package domain

import (
	"context"
	"math"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type ReferralService struct{ s *Services }

// Offer creates a referral to an opted-in receiving tenant. Idempotent per
// (referring tenant, idempotency key).
func (svc *ReferralService) Offer(ctx context.Context, referringTenantID, receivingTenantID, clientName, clientContact, summary string, estValueMinor int64, currency, idem string) (*model.Referral, error) {
	if referringTenantID == receivingTenantID || clientContact == "" || summary == "" {
		return nil, httpx.ErrValidation
	}
	receiver, err := svc.s.Store.Tenants().Get(ctx, receivingTenantID)
	if err != nil {
		return nil, err
	}
	if !receiver.AcceptsReferrals {
		return nil, httpx.ErrConflict // receiving tenant has not opted in
	}
	referrer, err := svc.s.Store.Tenants().Get(ctx, referringTenantID)
	if err != nil {
		return nil, err
	}
	feePct := referrer.ReferralFeePct
	if feePct <= 0 {
		feePct = 0.10
	}
	now := svc.s.Now()
	r := &model.Referral{
		ID: NewID(), ReferringTenantID: referringTenantID, ReceivingTenantID: receivingTenantID,
		ClientName: clientName, ClientContact: clientContact, RequestSummary: summary,
		EstValueMinor: estValueMinor, Currency: currency, FeePct: feePct,
		State: model.ReferralOffered, IdempotencyKey: idem, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.s.Store.Referrals().Create(ctx, r); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, referringTenantID, "referral", r.ID, "referral.offered",
		map[string]any{"receiving_tenant_id": receivingTenantID, "est_value_minor": estValueMinor})
	svc.s.Events.Emit(ctx, receivingTenantID, "referral", r.ID, "referral.incoming",
		map[string]any{"referring_tenant_id": referringTenantID, "est_value_minor": estValueMinor})
	return r, nil
}

// Accept marks the referral accepted by the receiving tenant and links the
// ServiceRequest they created for it.
func (svc *ReferralService) Accept(ctx context.Context, receivingTenantID, referralID, requestID string) (*model.Referral, error) {
	r, err := svc.s.Store.Referrals().Get(ctx, receivingTenantID, referralID)
	if err != nil {
		return nil, err
	}
	if r.ReceivingTenantID != receivingTenantID {
		return nil, httpx.ErrForbidden // only the receiver accepts
	}
	if r.State != model.ReferralOffered {
		return nil, httpx.ErrConflict
	}
	r.State = model.ReferralAccepted
	r.RequestID = requestID
	r.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.Referrals().Update(ctx, r); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, receivingTenantID, "referral", r.ID, "referral.accepted", nil)
	return r, nil
}

func (svc *ReferralService) Decline(ctx context.Context, receivingTenantID, referralID, reason string) (*model.Referral, error) {
	r, err := svc.s.Store.Referrals().Get(ctx, receivingTenantID, referralID)
	if err != nil {
		return nil, err
	}
	if r.ReceivingTenantID != receivingTenantID {
		return nil, httpx.ErrForbidden
	}
	if r.State != model.ReferralOffered {
		return nil, httpx.ErrConflict
	}
	r.State = model.ReferralDeclined
	r.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.Referrals().Update(ctx, r); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, receivingTenantID, "referral", r.ID, "referral.declined",
		map[string]any{"reason": reason})
	return r, nil
}

// Settle runs on the receiving tenant's confirmed booking: computes the
// commission and posts it (receiving tenant owes the referrer; booked as
// PARTNER_PAYABLE-style clearing: TENANT_REVENUE -> REFERRAL_FEES_PAYABLE).
// Absorbing: an already-SETTLED referral is a replay-safe no-op.
func (svc *ReferralService) Settle(ctx context.Context, receivingTenantID, referralID, bookingID string, bookingTotalMinor int64) (*model.Referral, error) {
	r, err := svc.s.Store.Referrals().Get(ctx, receivingTenantID, referralID)
	if err != nil {
		return nil, err
	}
	if r.ReceivingTenantID != receivingTenantID {
		return nil, httpx.ErrForbidden
	}
	if r.State == model.ReferralSettled {
		return r, nil // absorbing: replay-safe
	}
	if r.State != model.ReferralAccepted && r.State != model.ReferralFulfilled {
		return nil, httpx.ErrConflict
	}
	commission := int64(math.Round(float64(bookingTotalMinor) * r.FeePct))
	if commission > 0 {
		if err := svc.s.Payments.post(ctx, receivingTenantID, bookingID,
			"TENANT_REVENUE", "PARTNER_PAYABLE", commission, r.Currency,
			"referral:commission:"+r.ID); err != nil {
			return nil, err
		}
	}
	r.State = model.ReferralSettled
	r.BookingID = bookingID
	r.CommissionMinor = commission
	r.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.Referrals().Update(ctx, r); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, receivingTenantID, "referral", r.ID, "referral.settled",
		map[string]any{"commission_minor": commission, "booking_id": bookingID})
	svc.s.Events.Emit(ctx, r.ReferringTenantID, "referral", r.ID, "referral.commission_earned",
		map[string]any{"commission_minor": commission})
	return r, nil
}

func (svc *ReferralService) List(ctx context.Context, tenantID string) ([]model.Referral, error) {
	return svc.s.Store.Referrals().List(ctx, tenantID)
}

// ReferralMetrics is the network-effect KPI surface (Phase-3 gate).
type ReferralMetrics struct {
	Offered          int     `json:"offered"`
	Accepted         int     `json:"accepted"`
	Settled          int     `json:"settled"`
	AcceptRate       float64 `json:"accept_rate"`
	CommissionEarned int64   `json:"commission_earned_minor"` // as referrer
	CommissionPaid   int64   `json:"commission_paid_minor"`   // as receiver
}

func (svc *ReferralService) Metrics(ctx context.Context, tenantID string) (*ReferralMetrics, error) {
	list, err := svc.s.Store.Referrals().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	m := &ReferralMetrics{}
	for _, r := range list {
		if r.ReferringTenantID == tenantID {
			m.Offered++
			m.CommissionEarned += r.CommissionMinor
		}
		if r.ReceivingTenantID == tenantID {
			switch r.State {
			case model.ReferralAccepted, model.ReferralFulfilled, model.ReferralSettled:
				m.Accepted++
			}
			if r.State == model.ReferralSettled {
				m.Settled++
				m.CommissionPaid += r.CommissionMinor
			}
		}
	}
	incoming := 0
	for _, r := range list {
		if r.ReceivingTenantID == tenantID && r.State != "" {
			incoming++
		}
	}
	if incoming > 0 {
		m.AcceptRate = float64(m.Accepted) / float64(incoming)
	}
	return m, nil
}
