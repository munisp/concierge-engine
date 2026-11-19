package store

// Phase 59: in-memory soundscape store. Keyed tenantID/id with a separate
// idempotency index ("soundscape:{booking}") so generation never
// double-renders; copies in and out, matching every other memory store.

import (
	"context"
	"sort"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (m *memory) Soundscapes() SoundscapeStore { return &memSoundscapes{m} }

type memSoundscapes struct{ m *memory }

func (s *memSoundscapes) Create(_ context.Context, sc *model.Soundscape) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if sc.IdempotencyKey != "" {
		if _, ok := s.m.soundIdem[sc.TenantID+"/"+sc.IdempotencyKey]; ok {
			return httpx.ErrConflict
		}
	}
	cp := *sc
	s.m.soundscapes[sc.TenantID+"/"+sc.ID] = &cp
	if sc.IdempotencyKey != "" {
		s.m.soundIdem[sc.TenantID+"/"+sc.IdempotencyKey] = sc.ID
	}
	return nil
}

func (s *memSoundscapes) Get(_ context.Context, tenantID, id string) (*model.Soundscape, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	sc, ok := s.m.soundscapes[tenantID+"/"+id]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *sc
	return &cp, nil
}

func (s *memSoundscapes) ForBooking(_ context.Context, tenantID, bookingID string) (*model.Soundscape, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var best *model.Soundscape
	for _, sc := range s.m.soundscapes {
		if sc.TenantID == tenantID && sc.BookingID == bookingID &&
			(best == nil || sc.CreatedAt.After(best.CreatedAt)) {
			best = sc
		}
	}
	if best == nil {
		return nil, httpx.ErrNotFound
	}
	cp := *best
	return &cp, nil
}

func (s *memSoundscapes) Update(_ context.Context, sc *model.Soundscape) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if _, ok := s.m.soundscapes[sc.TenantID+"/"+sc.ID]; !ok {
		return httpx.ErrNotFound
	}
	cp := *sc
	s.m.soundscapes[sc.TenantID+"/"+sc.ID] = &cp
	return nil
}

func (s *memSoundscapes) ListForClient(_ context.Context, tenantID, clientID string, limit int) ([]model.Soundscape, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.Soundscape
	for _, sc := range s.m.soundscapes {
		if sc.TenantID == tenantID && sc.ClientID == clientID {
			out = append(out, *sc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
