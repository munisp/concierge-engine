package store

// Phase 21: in-memory message templates — upsert per (tenant, name), the
// console save button is naturally idempotent.

import (
	"context"
	"sort"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (m *memory) Templates() TemplateStore { return &memTemplates{m} }

type memTemplates struct{ m *memory }

func (s *memTemplates) Upsert(_ context.Context, t *model.MessageTemplate) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	for _, e := range s.m.templates {
		if e.TenantID == t.TenantID && e.Name == t.Name {
			// Same name: update in place, keep id + created_at.
			e.Purpose, e.Body, e.UpdatedAt = t.Purpose, t.Body, t.UpdatedAt
			t.ID, t.CreatedAt = e.ID, e.CreatedAt
			return nil
		}
	}
	cp := *t
	s.m.templates[t.TenantID+"/"+t.ID] = &cp
	return nil
}

func (s *memTemplates) Get(_ context.Context, tenantID, id string) (*model.MessageTemplate, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	t, ok := s.m.templates[tenantID+"/"+id]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *t
	return &cp, nil
}

func (s *memTemplates) GetByName(_ context.Context, tenantID, name string) (*model.MessageTemplate, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	for _, t := range s.m.templates {
		if t.TenantID == tenantID && t.Name == name {
			cp := *t
			return &cp, nil
		}
	}
	return nil, httpx.ErrNotFound
}

func (s *memTemplates) List(_ context.Context, tenantID string) ([]model.MessageTemplate, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	var out []model.MessageTemplate
	for _, t := range s.m.templates {
		if t.TenantID == tenantID {
			out = append(out, *t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *memTemplates) Delete(_ context.Context, tenantID, id string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if _, ok := s.m.templates[tenantID+"/"+id]; !ok {
		return httpx.ErrNotFound
	}
	delete(s.m.templates, tenantID+"/"+id)
	return nil
}

// Phase 27: in-memory reviews — one review per (tenant, assignment),
// publishable subset for the public SEO pages.

func (m *memory) Reviews() ReviewStore { return &memReviews{m} }

type memReviews struct{ m *memory }

func (s *memReviews) Create(_ context.Context, r *model.Review) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	key := r.TenantID + "/" + r.AssignmentID
	if _, ok := s.m.reviews[key]; ok {
		return httpx.ErrConflict
	}
	cp := *r
	s.m.reviews[key] = &cp
	return nil
}

func (s *memReviews) GetByAssignment(_ context.Context, tenantID, assignmentID string) (*model.Review, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	r, ok := s.m.reviews[tenantID+"/"+assignmentID]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *r
	return &cp, nil
}

func (s *memReviews) Consented(_ context.Context, tenantID string) ([]model.Review, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.Review
	for _, r := range s.m.reviews {
		if r.TenantID == tenantID && r.PublishConsent {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (s *memReviews) ListAll(_ context.Context, tenantID string, limit int) ([]model.Review, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.Review
	for _, r := range s.m.reviews {
		if r.TenantID == tenantID {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
