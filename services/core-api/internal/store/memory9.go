package store

// Phase 20: in-memory promo campaigns — create (idempotent per code),
// lookup, list, redemption counting via the rewards map.

import (
	"context"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (m *memory) Promos() PromoStore { return &memPromos{m} }

type memPromos struct{ m *memory }

func (s *memPromos) Create(_ context.Context, c *model.PromoCampaign) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	code := strings.ToUpper(c.Code)
	for _, e := range s.m.promos {
		if e.TenantID == c.TenantID && e.Code == code {
			return httpx.ErrConflict
		}
	}
	cp := *c
	cp.Code = code
	s.m.promos[c.TenantID+"/"+code] = &cp
	return nil
}

func (s *memPromos) GetByCode(_ context.Context, tenantID, code string) (*model.PromoCampaign, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	c, ok := s.m.promos[tenantID+"/"+strings.ToUpper(code)]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (s *memPromos) List(_ context.Context, tenantID string) ([]model.PromoCampaign, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.PromoCampaign
	for _, c := range s.m.promos {
		if c.TenantID == tenantID {
			out = append(out, *c)
		}
	}
	return out, nil
}

func (s *memPromos) Redemptions(_ context.Context, tenantID, code string) (int, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	n := 0
	for _, e := range s.m.rewards {
		if e.TenantID == tenantID && e.PromoCode == strings.ToUpper(code) {
			n++
		}
	}
	return n, nil
}
