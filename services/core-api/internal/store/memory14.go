package store

// Phase 41: in-memory email suppressions — keyed tenantID/email (the
// idempotency unit itself; re-suppressing the same address conflicts and
// the domain returns the existing row). List returns newest first.

import (
	"context"
	"sort"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (m *memory) EmailSuppressions() EmailSuppressionStore { return &memEmailSuppressions{m} }

type memEmailSuppressions struct{ m *memory }

func (s *memEmailSuppressions) Add(_ context.Context, sup *model.EmailSuppression) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	k := sup.TenantID + "/" + sup.Email
	if _, ok := s.m.suppressions[k]; ok {
		return httpx.ErrConflict
	}
	cp := *sup
	s.m.suppressions[k] = &cp
	return nil
}

func (s *memEmailSuppressions) Exists(_ context.Context, tenantID, email string) (bool, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	_, ok := s.m.suppressions[tenantID+"/"+email]
	return ok, nil
}

func (s *memEmailSuppressions) List(_ context.Context, tenantID string, limit int) ([]model.EmailSuppression, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.EmailSuppression
	for _, sup := range s.m.suppressions {
		if sup.TenantID == tenantID {
			out = append(out, *sup)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *memEmailSuppressions) Delete(_ context.Context, tenantID, email string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	k := tenantID + "/" + email
	if _, ok := s.m.suppressions[k]; !ok {
		return httpx.ErrNotFound
	}
	delete(s.m.suppressions, k)
	return nil
}
