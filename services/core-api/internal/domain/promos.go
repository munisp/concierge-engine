package domain

// Phase 20: promo-code campaigns — fixed-credit codes that extend the
// reward program beyond referrals. A client redeems a code once; the credit
// lands in the same ClientReward ledger as referral rewards, so it redeems
// against payment links with the Phase 18 compensation-posting mechanics
// and refund safety.
//
// Codes are case-insensitive (stored uppercase). Idempotency is structural:
// the (tenant, client, promo_code) uniqueness makes replays and double
// redemption safe.

import (
	"context"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type CreatePromoInput struct {
	Code           string `json:"code"`
	CreditMinor    int64  `json:"credit_minor"`
	Currency       string `json:"currency"`
	MaxRedemptions int    `json:"max_redemptions,omitempty"`
	ExpiryDays     int    `json:"expiry_days,omitempty"` // campaign validity; 0 = no expiry
	// Phase 27 acquisition flags: Gift = one-recipient voucher a client
	// hands to a non-client (forces max_redemptions 1 when unset);
	// SwitchOffer = competitor-switch campaign (redemption alerts the
	// operator roster — a high-intent signal worth a personal response).
	Gift        bool `json:"gift,omitempty"`
	SwitchOffer bool `json:"switch_offer,omitempty"`
}

// CreatePromo registers a campaign. Idempotent per (tenant, code) —
// re-creating an existing code returns it unchanged.
func (svc *Services) CreatePromo(ctx context.Context, tenantID string, in CreatePromoInput) (*model.PromoCampaign, error) {
	code := strings.ToUpper(strings.TrimSpace(in.Code))
	if len(code) < 3 || len(code) > 32 {
		return nil, httpx.ErrValidation
	}
	if in.CreditMinor <= 0 {
		return nil, httpx.ErrValidation
	}
	if err := validateCurrency(in.Currency); err != nil {
		return nil, err
	}
	maxRed := in.MaxRedemptions
	if in.Gift && maxRed == 0 {
		maxRed = 1 // a gift voucher has exactly one recipient
	}
	c := &model.PromoCampaign{
		ID: NewID(), TenantID: tenantID, Code: code,
		CreditMinor: in.CreditMinor, Currency: strings.ToUpper(in.Currency),
		MaxRedemptions: maxRed, Active: true, CreatedAt: svc.Now(),
		Gift: in.Gift, SwitchOffer: in.SwitchOffer,
	}
	if in.ExpiryDays > 0 {
		c.ExpiresAt = svc.Now().Add(time.Duration(in.ExpiryDays) * 24 * time.Hour)
	}
	if err := svc.Store.Promos().Create(ctx, c); err != nil {
		if err == httpx.ErrConflict {
			return svc.Store.Promos().GetByCode(ctx, tenantID, code) // replay-safe
		}
		return nil, err
	}
	svc.Events.Emit(ctx, tenantID, "promo", c.ID, "promo.created",
		map[string]any{"code": c.Code, "credit_minor": c.CreditMinor, "currency": c.Currency})
	return c, nil
}

func (svc *Services) ListPromos(ctx context.Context, tenantID string) ([]model.PromoCampaign, error) {
	promos, err := svc.Store.Promos().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if promos == nil {
		promos = []model.PromoCampaign{}
	}
	return promos, nil
}

// RedeemPromo credits a client under a campaign code. One redemption per
// client per code (structural). Returns the accrued reward.
func (svc *Services) RedeemPromo(ctx context.Context, tenantID, code, clientID string) (*model.ClientReward, error) {
	if strings.TrimSpace(code) == "" || clientID == "" {
		return nil, httpx.ErrValidation
	}
	c, err := svc.Store.Promos().GetByCode(ctx, tenantID, code)
	if err != nil {
		return nil, err // unknown code
	}
	if !c.Active {
		return nil, httpx.ErrConflict
	}
	if !c.ExpiresAt.IsZero() && c.ExpiresAt.Before(svc.Now()) {
		return nil, httpx.ErrConflict // campaign expired
	}
	if c.MaxRedemptions > 0 {
		n, err := svc.Store.Promos().Redemptions(ctx, tenantID, c.Code)
		if err != nil {
			return nil, err
		}
		if n >= c.MaxRedemptions {
			return nil, httpx.ErrConflict // exhausted
		}
	}
	reward := &model.ClientReward{
		ID: NewID(), TenantID: tenantID, ClientID: clientID,
		Kind: "promo_credit", PromoCode: c.Code,
		AmountMinor: c.CreditMinor, Currency: c.Currency,
		State: model.RewardAccrued, CreatedAt: svc.Now(),
		// The credit lives and dies with the campaign's validity window.
		ExpiresAt: c.ExpiresAt,
	}
	if err := svc.Store.Rewards().Accrue(ctx, reward); err != nil {
		return nil, err // ErrConflict = already redeemed by this client
	}
	svc.Events.Emit(ctx, tenantID, "client", clientID, "promo.redeemed",
		map[string]any{"code": c.Code, "amount_minor": c.CreditMinor, "currency": c.Currency})
	svc.redeemSideEffects(ctx, tenantID, clientID, c) // Phase 27: gift attribution + switch alerts
	return reward, nil
}
