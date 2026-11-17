package store

// Phase 31: in-memory media — site imagery keyed tenantID/id. Put copies
// the bytes (callers may reuse the buffer); List strips Data so the console
// picker never drags megabytes through a metadata call.

import (
	"context"
	"sort"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (m *memory) Media() MediaStore { return &memMedia{m} }

type memMedia struct{ m *memory }

func (s *memMedia) Put(_ context.Context, md *model.Media) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *md
	cp.Data = append([]byte(nil), md.Data...)
	s.m.media[md.TenantID+"/"+md.ID] = &cp
	return nil
}

func (s *memMedia) Get(_ context.Context, tenantID, id string) (*model.Media, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	md, ok := s.m.media[tenantID+"/"+id]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *md
	cp.Data = append([]byte(nil), md.Data...)
	return &cp, nil
}

func (s *memMedia) List(_ context.Context, tenantID string) ([]model.Media, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.Media
	for _, md := range s.m.media {
		if md.TenantID == tenantID {
			cp := *md
			cp.Data = nil
			out = append(out, cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *memMedia) Delete(_ context.Context, tenantID, id string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	key := tenantID + "/" + id
	if _, ok := s.m.media[key]; !ok {
		return httpx.ErrNotFound
	}
	delete(s.m.media, key)
	return nil
}

// MarkExternal (Phase 49): flip a legacy inline row to object storage —
// record the key/engine and drop the inline bytes.
func (s *memMedia) MarkExternal(_ context.Context, tenantID, id, storageKey, engine string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	md, ok := s.m.media[tenantID+"/"+id]
	if !ok {
		return httpx.ErrNotFound
	}
	md.StorageKey = storageKey
	md.StorageEngine = engine
	md.SizeBytes = len(md.Data)
	md.Data = nil
	return nil
}
