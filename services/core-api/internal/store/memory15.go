package store

// Phase 44: in-memory consent evidence ledger. Append-only slice on the
// memory aggregate; ListForClient filters + sorts newest-first.

import (
	"context"
	"sort"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func (m *memory) ConsentEvents() ConsentEventStore { return &memConsentEvents{m} }

type memConsentEvents struct{ m *memory }

func (s *memConsentEvents) Record(_ context.Context, e *model.ConsentEvent) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *e
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now()
	}
	s.m.consentEvents = append(s.m.consentEvents, &cp)
	return nil
}

func (s *memConsentEvents) ListForClient(_ context.Context, tenantID, clientID string, limit int) ([]model.ConsentEvent, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.ConsentEvent
	for _, e := range s.m.consentEvents {
		if e.TenantID == tenantID && e.ClientID == clientID {
			out = append(out, *e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
