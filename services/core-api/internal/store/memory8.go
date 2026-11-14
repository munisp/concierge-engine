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
	// Idempotent per (tenant, client, source booking): a replayed conversion
	// never double-credits the referrer.
	for _, e := range s.m.rewards {
		if e.TenantID == r.TenantID && e.ClientID == r.ClientID &&
			e.SourceBookingID == r.SourceBookingID {
			return httpx.ErrConflict
		}
	}
	cp := *r
	s.m.rewards[r.TenantID+"/"+r.ID] = &cp
	return nil
}

func (s *memRewards) Balance(_ context.Context, tenantID, clientID string) (int64, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
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
