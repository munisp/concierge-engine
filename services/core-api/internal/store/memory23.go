package store

// Phase 65: in-memory partner rate-card store — one card per
// (tenant, partner, vertical); upsert is the write path.

import (
	"context"
	"sort"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (m *memory) RateCards() RateCardStore { return &memRateCards{m} }

type memRateCards struct{ m *memory }

func rateCardKey(tenantID, partnerID, vertical string) string {
	return tenantID + "/" + partnerID + "/" + vertical
}

func (s *memRateCards) Upsert(_ context.Context, c *model.RateCard) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	key := rateCardKey(c.TenantID, c.PartnerID, c.Vertical)
	if existing, ok := s.m.rateCards[key]; ok {
		c.CreatedAt = existing.CreatedAt // upsert preserves the original row age
	}
	cp := *c
	s.m.rateCards[key] = &cp
	return nil
}

func (s *memRateCards) ListForPartner(_ context.Context, tenantID, partnerID string) ([]model.RateCard, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	out := []model.RateCard{}
	for _, c := range s.m.rateCards {
		if c.TenantID == tenantID && c.PartnerID == partnerID {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Vertical < out[j].Vertical })
	return out, nil
}

func (s *memRateCards) Delete(_ context.Context, tenantID, partnerID, vertical string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	key := rateCardKey(tenantID, partnerID, vertical)
	if _, ok := s.m.rateCards[key]; !ok {
		return httpx.ErrNotFound
	}
	delete(s.m.rateCards, key)
	return nil
}
