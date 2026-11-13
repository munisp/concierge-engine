// In-memory Phase 4 store: cases, tasks, notes. Same tenant-scoping and
// idempotency discipline as memory.go/memory3.go; Postgres in postgres4.go.
package store

import (
	"context"
	"sort"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type memCases struct{ m *memory }

func (s *memCases) Create(_ context.Context, c *model.Case) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if c.IdempotencyKey != "" {
		k := c.TenantID + "|" + c.IdempotencyKey
		if _, ok := s.m.caseIdem[k]; ok {
			return httpx.ErrConflict
		}
		s.m.caseIdem[k] = c.ID
	}
	cp := *c
	s.m.cases[c.ID] = &cp
	return nil
}

func (s *memCases) Get(_ context.Context, tenantID, id string) (*model.Case, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	c, ok := s.m.cases[id]
	if !ok || c.TenantID != tenantID {
		return nil, httpx.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (s *memCases) Update(_ context.Context, c *model.Case) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cur, ok := s.m.cases[c.ID]
	if !ok || cur.TenantID != c.TenantID {
		return httpx.ErrNotFound
	}
	cp := *c
	s.m.cases[c.ID] = &cp
	return nil
}

func (s *memCases) List(_ context.Context, tenantID, state, kind string) ([]model.Case, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	out := []model.Case{}
	for _, c := range s.m.cases {
		if c.TenantID != tenantID {
			continue
		}
		if state != "" && c.State != state {
			continue
		}
		if kind != "" && c.Kind != kind {
			continue
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Overdue returns open-state cases whose SLA clock has expired — the SLA
// scanner escalates these (Taylor's promise is operational, not decorative).
func (s *memCases) Overdue(_ context.Context, tenantID string, now time.Time) ([]model.Case, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	out := []model.Case{}
	for _, c := range s.m.cases {
		if c.TenantID != tenantID {
			continue
		}
		switch c.State {
		case model.CaseResolved, model.CaseClosed:
			continue
		}
		if !c.SLADueAt.IsZero() && now.After(c.SLADueAt) {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SLADueAt.Before(out[j].SLADueAt) })
	return out, nil
}

func (s *memCases) AddTask(_ context.Context, t *model.CaseTask) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if _, ok := s.m.cases[t.CaseID]; !ok {
		return httpx.ErrNotFound
	}
	cp := *t
	s.m.caseTasks[t.ID] = &cp
	return nil
}

func (s *memCases) Tasks(_ context.Context, tenantID, caseID string) ([]model.CaseTask, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	out := []model.CaseTask{}
	for _, t := range s.m.caseTasks {
		if t.TenantID == tenantID && t.CaseID == caseID {
			out = append(out, *t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *memCases) UpdateTask(_ context.Context, t *model.CaseTask) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cur, ok := s.m.caseTasks[t.ID]
	if !ok || cur.TenantID != t.TenantID {
		return httpx.ErrNotFound
	}
	cp := *t
	s.m.caseTasks[t.ID] = &cp
	return nil
}

func (s *memCases) AddNote(_ context.Context, n *model.CaseNote) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if _, ok := s.m.cases[n.CaseID]; !ok {
		return httpx.ErrNotFound
	}
	s.m.caseNotes[n.CaseID] = append(s.m.caseNotes[n.CaseID], *n)
	return nil
}

func (s *memCases) Notes(_ context.Context, tenantID, caseID string) ([]model.CaseNote, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	out := []model.CaseNote{}
	for _, n := range s.m.caseNotes[caseID] {
		if n.TenantID == tenantID {
			out = append(out, n)
		}
	}
	return out, nil
}
