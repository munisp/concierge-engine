package store

import (
	"context"
	"strings"

	"concierge-engine/services/core-api/internal/model"
)

// In-memory geo store: mirrors the PostGIS semantics (ring containment) in
// plain Go so tests and dev need no database.
type memGeo struct{ m *memory }

func (g *memGeo) AddArea(ctx context.Context, a *model.ServiceArea) error {
	g.m.mu.Lock()
	defer g.m.mu.Unlock()
	g.m.geoAreas[a.TenantID+"|"+a.ID] = a
	return nil
}

func (g *memGeo) Areas(ctx context.Context, tenantID string) ([]model.ServiceArea, error) {
	g.m.mu.RLock()
	defer g.m.mu.RUnlock()
	out := []model.ServiceArea{}
	for k, a := range g.m.geoAreas {
		if k[:len(tenantID)+1] == tenantID+"|" {
			out = append(out, *a)
		}
	}
	return out, nil
}

// ---------------------------------------------------------- Phase 11 receipts

// In-memory receipts: idempotent per (tenant, external_ref, status) — the
// key IS the dedupe, so channel redelivery replays are free.
type memReceipts struct{ m *memory }

func (s *memReceipts) AddReceipt(ctx context.Context, rc *model.MessageReceipt) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	s.m.receipts[rc.TenantID+"|"+rc.ExternalRef+"|"+rc.Status] = rc
	return nil
}

func (s *memReceipts) Receipts(ctx context.Context, tenantID, externalRef string) ([]model.MessageReceipt, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	prefix := tenantID + "|" + externalRef + "|"
	out := []model.MessageReceipt{}
	for k, rc := range s.m.receipts {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			out = append(out, *rc)
		}
	}
	return out, nil
}

func (s *memReceipts) ReceiptCounts(ctx context.Context, tenantID string) (map[string]int, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	prefix := tenantID + "|"
	out := map[string]int{}
	for k := range s.m.receipts {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			status := k[strings.LastIndex(k, "|")+1:]
			out[status]++
		}
	}
	return out, nil
}
