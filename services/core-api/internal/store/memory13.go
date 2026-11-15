package store

// Phase 40: in-memory campaign spend ledger + attribution CountSince (the
// deliverability guard's 7-day window). Spend rows are keyed tenantID/id
// with an idempotency index mirroring the pg partial UNIQUE; List returns
// copies newest-spend-first (spent_on DESC, created_at DESC), matching pg.

import (
	"context"
	"sort"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (m *memory) CampaignSpend() CampaignSpendStore { return &memCampaignSpend{m} }

type memCampaignSpend struct{ m *memory }

func (s *memCampaignSpend) Add(_ context.Context, sp *model.CampaignSpend) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if sp.IdempotencyKey != "" {
		k := sp.TenantID + "/" + sp.IdempotencyKey
		if _, ok := s.m.spendIdem[k]; ok {
			return httpx.ErrConflict
		}
		s.m.spendIdem[k] = sp.ID
	}
	cp := *sp
	s.m.spend[sp.TenantID+"/"+sp.ID] = &cp
	return nil
}

func (s *memCampaignSpend) List(_ context.Context, tenantID, campaign string, limit int) ([]model.CampaignSpend, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.CampaignSpend
	for _, sp := range s.m.spend {
		if sp.TenantID == tenantID && (campaign == "" || sp.Campaign == campaign) {
			out = append(out, *sp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SpentOn != out[j].SpentOn {
			return out[i].SpentOn > out[j].SpentOn
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// CountSince (Phase 40): events of one kind, optionally one channel,
// recorded at or after since. Events with no timestamp (tracked before the
// At field existed) count as ancient — they never enter a 7-day window.
func (s *memAttribution) CountSince(_ context.Context, tenantID, kind, channel string, since time.Time) (int, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	n := 0
	for _, e := range s.m.attribution {
		if e.TenantID != tenantID || e.Kind != kind {
			continue
		}
		if channel != "" && e.Channel != channel {
			continue
		}
		if e.At.IsZero() || e.At.Before(since) {
			continue
		}
		n++
	}
	return n, nil
}
