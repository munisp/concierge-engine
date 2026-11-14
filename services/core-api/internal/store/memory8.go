package store

// Phase 18: in-memory client rewards — accrual, balance, oldest-first
// redemption. Keys are tenant-scoped composites (same convention as the
// other memory stores).

import (
	"context"
	"sort"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type memRewards struct{ m *memory }

func (s *memRewards) Accrue(_ context.Context, r *model.ClientReward) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	// Idempotent per (tenant, client, source booking) — a replayed
	// conversion never double-credits — and per (tenant, client, promo
	// code): one redemption per client per promo (Phase 20).
	for _, e := range s.m.rewards {
		if e.TenantID == r.TenantID && e.ClientID == r.ClientID &&
			(e.SourceBookingID != "" && e.SourceBookingID == r.SourceBookingID ||
				r.PromoCode != "" && e.PromoCode == r.PromoCode) {
			return httpx.ErrConflict
		}
	}
	cp := *r
	s.m.rewards[r.TenantID+"/"+r.ID] = &cp
	return nil
}

// sweepExpiredLocked marks lapsed ACCRUED rewards EXPIRED. Callers hold the
// write lock. Expiry is lazy (on access) — no background goroutine in the
// in-memory engine.
func (s *memRewards) sweepExpiredLocked(tenantID, clientID string, now time.Time) {
	for _, e := range s.m.rewards {
		if e.TenantID == tenantID && e.ClientID == clientID &&
			e.State == model.RewardAccrued && !e.ExpiresAt.IsZero() && e.ExpiresAt.Before(now) {
			e.State = model.RewardExpired
		}
	}
}

func (s *memRewards) Balance(_ context.Context, tenantID, clientID string) (int64, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	s.sweepExpiredLocked(tenantID, clientID, time.Now().UTC())
	var bal int64
	for _, e := range s.m.rewards {
		if e.TenantID == tenantID && e.ClientID == clientID && e.State == model.RewardAccrued {
			bal += e.AmountMinor
		}
	}
	return bal, nil
}

func (s *memRewards) Redeem(_ context.Context, tenantID, clientID, intentID string, amount int64) (int64, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	s.sweepExpiredLocked(tenantID, clientID, time.Now().UTC())
	var accrued []*model.ClientReward
	for _, e := range s.m.rewards {
		if e.TenantID == tenantID && e.ClientID == clientID && e.State == model.RewardAccrued {
			accrued = append(accrued, e)
		}
	}
	// Oldest first — rewards expire conceptually FIFO.
	sort.Slice(accrued, func(i, j int) bool { return accrued[i].CreatedAt.Before(accrued[j].CreatedAt) })
	var covered int64
	for _, e := range accrued {
		if covered >= amount {
			break
		}
		e.State = model.RewardRedeemed
		e.RedeemedIntentID = intentID
		e.RedeemedAt = time.Now().UTC()
		covered += e.AmountMinor
	}
	return covered, nil
}

func (s *memRewards) List(_ context.Context, tenantID, clientID string) ([]model.ClientReward, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	s.sweepExpiredLocked(tenantID, clientID, time.Now().UTC())
	var out []model.ClientReward
	for _, e := range s.m.rewards {
		if e.TenantID == tenantID && e.ClientID == clientID {
			out = append(out, *e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *memRewards) RefundRedemption(_ context.Context, tenantID, intentID string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	for _, e := range s.m.rewards {
		if e.TenantID == tenantID && e.RedeemedIntentID == intentID && e.State == model.RewardRedeemed {
			e.State = model.RewardAccrued
			e.RedeemedIntentID = ""
			e.RedeemedAt = time.Time{}
		}
	}
	return nil
}
