package store

import (
	"context"
	"sort"
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

// ---------------------------------------------------- Phase 12 receipt links

// LinkReceipt: external_ref -> case. First link wins — a redelivered or
// double-sent link attempt must never re-point evidence at another case.
func (s *memReceipts) LinkReceipt(ctx context.Context, tenantID, externalRef, caseID string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	key := tenantID + "|" + externalRef
	if _, exists := s.m.receiptLinks[key]; !exists {
		s.m.receiptLinks[key] = caseID
	}
	return nil
}

func (s *memReceipts) LinkedCase(ctx context.Context, tenantID, externalRef string) (string, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	return s.m.receiptLinks[tenantID+"|"+externalRef], nil // absent -> "" (not an error)
}

func (s *memReceipts) ReceiptsByStatus(ctx context.Context, tenantID, status string, limit int) ([]model.MessageReceipt, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	prefix := tenantID + "|"
	out := []model.MessageReceipt{}
	for k, rc := range s.m.receipts {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix && rc.Status == status {
			out = append(out, *rc)
		}
	}
	// newest first (the board shows recent failures, not ancient history)
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *memReceipts) RefsForCase(ctx context.Context, tenantID, caseID string) ([]string, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	prefix := tenantID + "|"
	out := []string{}
	for k, cid := range s.m.receiptLinks {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix && cid == caseID {
			out = append(out, k[len(prefix):])
		}
	}
	sort.Strings(out)
	return out, nil
}
