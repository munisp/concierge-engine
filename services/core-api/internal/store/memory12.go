package store

// Phase 36: in-memory leads — pipeline prospects keyed tenantID/id, with
// an idempotency-key index mirroring the pg partial UNIQUE. List returns
// copies in created_at DESC order (newest first), matching pg.

import (
	"context"
	"sort"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (m *memory) Leads() LeadStore { return &memLeads{m} }

type memLeads struct{ m *memory }

func (s *memLeads) Create(_ context.Context, l *model.Lead) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if l.IdempotencyKey != "" {
		k := l.TenantID + "/" + l.IdempotencyKey
		if _, ok := s.m.leadIdem[k]; ok {
			return httpx.ErrConflict
		}
		s.m.leadIdem[k] = l.ID
	}
	cp := *l
	s.m.leads[l.TenantID+"/"+l.ID] = &cp
	return nil
}

func (s *memLeads) Get(_ context.Context, tenantID, id string) (*model.Lead, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	l, ok := s.m.leads[tenantID+"/"+id]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *l
	return &cp, nil
}

func (s *memLeads) Update(_ context.Context, l *model.Lead) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	k := l.TenantID + "/" + l.ID
	if _, ok := s.m.leads[k]; !ok {
		return httpx.ErrNotFound
	}
	cp := *l
	s.m.leads[k] = &cp
	return nil
}

func (s *memLeads) List(_ context.Context, tenantID string, limit int) ([]model.Lead, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.Lead
	for _, l := range s.m.leads {
		if l.TenantID == tenantID {
			out = append(out, *l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
