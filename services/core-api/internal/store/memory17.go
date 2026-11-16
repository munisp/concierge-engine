package store

// Phase 47: in-memory KYC case store. Keyed tenantID/id with a separate
// idempotency index; the one-open-case-per-(client,level) rule is enforced
// in Create, mirroring the partial unique index in migration 0033.

import (
	"context"
	"sort"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (m *memory) KycCases() KycCaseStore { return &memKycCases{m} }

type memKycCases struct{ m *memory }

func (s *memKycCases) Create(_ context.Context, c *model.KycCase) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if c.IdempotencyKey != "" {
		if _, ok := s.m.kycIdem[c.TenantID+"/"+c.IdempotencyKey]; ok {
			return httpx.ErrConflict
		}
	}
	for _, k := range s.m.kycCases {
		if k.TenantID == c.TenantID && k.ClientID == c.ClientID && k.Level == c.Level &&
			(k.Status == model.KycStatusPending || k.Status == model.KycStatusInReview) {
			return httpx.ErrConflict // one open case per (client, level)
		}
	}
	cp := *c
	s.m.kycCases[c.TenantID+"/"+c.ID] = &cp
	if c.IdempotencyKey != "" {
		s.m.kycIdem[c.TenantID+"/"+c.IdempotencyKey] = c.ID
	}
	return nil
}

func (s *memKycCases) Get(_ context.Context, tenantID, id string) (*model.KycCase, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	k, ok := s.m.kycCases[tenantID+"/"+id]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *k
	return &cp, nil
}

func (s *memKycCases) GetByIdem(_ context.Context, tenantID, key string) (*model.KycCase, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	id, ok := s.m.kycIdem[tenantID+"/"+key]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *s.m.kycCases[tenantID+"/"+id]
	return &cp, nil
}

func (s *memKycCases) OpenFor(_ context.Context, tenantID, clientID, level string) (*model.KycCase, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	for _, k := range s.m.kycCases {
		if k.TenantID == tenantID && k.ClientID == clientID && k.Level == level &&
			(k.Status == model.KycStatusPending || k.Status == model.KycStatusInReview) {
			cp := *k
			return &cp, nil
		}
	}
	return nil, httpx.ErrNotFound
}

func (s *memKycCases) Update(_ context.Context, c *model.KycCase) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	key := c.TenantID + "/" + c.ID
	if _, ok := s.m.kycCases[key]; !ok {
		return httpx.ErrNotFound
	}
	cp := *c
	s.m.kycCases[key] = &cp
	return nil
}

func (s *memKycCases) List(_ context.Context, tenantID, status string, limit int) ([]model.KycCase, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.KycCase
	for _, k := range s.m.kycCases {
		if k.TenantID == tenantID && (status == "" || k.Status == status) {
			out = append(out, *k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *memKycCases) ListForClient(_ context.Context, tenantID, clientID string) ([]model.KycCase, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.KycCase
	for _, k := range s.m.kycCases {
		if k.TenantID == tenantID && k.ClientID == clientID {
			out = append(out, *k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *memKycCases) ExpireBefore(_ context.Context, tenantID string, before time.Time) (int, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	n := 0
	for _, k := range s.m.kycCases {
		if k.TenantID == tenantID && k.CreatedAt.Before(before) &&
			(k.Status == model.KycStatusPending || k.Status == model.KycStatusInReview) {
			k.Status = model.KycStatusExpired
			k.UpdatedAt = time.Now()
			n++
		}
	}
	return n, nil
}

func (s *memKycCases) ScrubDocumentRefsForClient(_ context.Context, tenantID, clientID string) (int, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	n := 0
	for _, k := range s.m.kycCases {
		if k.TenantID == tenantID && k.ClientID == clientID && (k.DocumentType != "" || k.DocumentRef != "") {
			k.DocumentType = ""
			k.DocumentRef = ""
			k.UpdatedAt = time.Now()
			n++
		}
	}
	return n, nil
}
