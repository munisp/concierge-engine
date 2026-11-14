package domain

// Phase 19: reward sharing program — two-sided referral credit with
// per-tenant program configuration, accrual caps and optional expiry.
//
// The Phase 18 spine credited only the referrer at a flat 5%. The program
// generalizes it: the referrer earns referrer_pct of the referee's first
// booking value AND the referee earns referee_pct as a welcome credit on
// the same booking — the reward is shared between both sides of the
// referral. Everything is per-tenant (RewardProgram on the tenant record),
// legacy PricingRules.ReferralRewardPct stays honored as the referrer-side
// fallback, and credit never expires unless the tenant opts into expiry.

import (
	"context"
	"math"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// rewardProgram resolves the tenant's effective program configuration.
// Defaults preserve Phase 18 behavior: enabled, referrer 5%, no referee
// credit, uncapped, no expiry.
func (svc *Services) rewardProgram(ctx context.Context, tenantID string) model.RewardProgram {
	prog := model.RewardProgram{}
	t, err := svc.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		prog.ReferrerPct = 0.05
		return prog
	}
	prog = t.RewardProgram
	if prog.ReferrerPct <= 0 {
		// Legacy fallback chain: explicit program pct, then the Phase 18
		// PricingRules override, then the 5% default.
		if t.PricingRules.ReferralRewardPct > 0 {
			prog.ReferrerPct = t.PricingRules.ReferralRewardPct
		} else {
			prog.ReferrerPct = 0.05
		}
	}
	return prog
}

// accrueReward credits one side of a conversion. Returns the accrued amount
// (0 when nothing accrued). Advisory by design: errors are swallowed by the
// caller — a reward failure never blocks a booking confirmation.
func (svc *Services) accrueReward(ctx context.Context, tenantID, clientID, bookingID, kind string,
	valueMinor int64, currency string, pct float64, prog model.RewardProgram) int64 {
	if clientID == "" || pct <= 0 || valueMinor <= 0 {
		return 0
	}
	pct *= boostActive(prog, svc.Now()) // Phase 27: time-boxed referral boost window
	amount := int64(math.Round(float64(valueMinor) * pct))
	if prog.MaxRewardMinor > 0 && amount > prog.MaxRewardMinor {
		amount = prog.MaxRewardMinor
	}
	if amount <= 0 {
		return 0
	}
	reward := &model.ClientReward{
		ID: NewID(), TenantID: tenantID, ClientID: clientID,
		SourceBookingID: bookingID, Kind: kind,
		AmountMinor: amount, Currency: currency,
		State: model.RewardAccrued, CreatedAt: svc.Now(),
	}
	if prog.ExpiryDays > 0 {
		reward.ExpiresAt = svc.Now().Add(time.Duration(prog.ExpiryDays) * 24 * time.Hour)
	}
	if err := svc.Store.Rewards().Accrue(ctx, reward); err != nil {
		return 0 // replayed conversion (ErrConflict) or store failure — never block
	}
	svc.Events.Emit(ctx, tenantID, "client", clientID, "referral.reward_accrued",
		map[string]any{"amount_minor": amount, "kind": kind,
			"source_booking_id": bookingID})
	return amount
}

// RewardSummary is the client-facing view of the program: current redeemable
// balance plus the full reward history (newest first). Balances are
// expiry-aware — lapsed credit is swept to EXPIRED on access.
func (svc *Services) RewardSummary(ctx context.Context, tenantID, clientID string) (map[string]any, error) {
	bal, err := svc.Store.Rewards().Balance(ctx, tenantID, clientID)
	if err != nil {
		return nil, err
	}
	rewards, err := svc.Store.Rewards().List(ctx, tenantID, clientID)
	if err != nil {
		return nil, err
	}
	if rewards == nil {
		rewards = []model.ClientReward{}
	}
	return map[string]any{
		"client_id":     clientID,
		"balance_minor": bal,
		"rewards":       rewards,
	}, nil
}
