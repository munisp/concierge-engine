package store

// Phase 48: in-memory settlement batch store + the payout list helpers the
// batching lane needs (pending pool per partner, batch members).

import (
	"context"
	"sort"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (s *memPayouts) ListPendingForPartner(_ context.Context, tenantID, partnerID string) ([]model.Payout, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.Payout
	for _, p := range s.m.payouts {
		if p.TenantID == tenantID && p.PartnerID == partnerID && p.State == model.PayoutPending && p.BatchID == "" {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *memPayouts) ListByBatch(_ context.Context, tenantID, batchID string) ([]model.Payout, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.Payout
	for _, p := range s.m.payouts {
		if p.TenantID == tenantID && p.BatchID == batchID {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *memory) Settlements() SettlementBatchStore { return &memBatches{m} }

type memBatches struct{ m *memory }

func (s *memBatches) Create(_ context.Context, b *model.SettlementBatch) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if b.IdempotencyKey != "" {
		if _, ok := s.m.batchIdem[b.TenantID+"/"+b.IdempotencyKey]; ok {
			return httpx.ErrConflict
		}
	}
	cp := *b
	s.m.batches[b.TenantID+"/"+b.ID] = &cp
	if b.IdempotencyKey != "" {
		s.m.batchIdem[b.TenantID+"/"+b.IdempotencyKey] = b.ID
	}
	return nil
}

func (s *memBatches) Get(_ context.Context, tenantID, id string) (*model.SettlementBatch, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	b, ok := s.m.batches[tenantID+"/"+id]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *b
	return &cp, nil
}

func (s *memBatches) GetByIdem(_ context.Context, tenantID, key string) (*model.SettlementBatch, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	id, ok := s.m.batchIdem[tenantID+"/"+key]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *s.m.batches[tenantID+"/"+id]
	return &cp, nil
}

func (s *memBatches) GetByTransfer(_ context.Context, tenantID, transferID string) (*model.SettlementBatch, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	for _, b := range s.m.batches {
		if b.TenantID == tenantID && b.TransferID == transferID && transferID != "" {
			cp := *b
			return &cp, nil
		}
	}
	return nil, httpx.ErrNotFound
}

func (s *memBatches) Update(_ context.Context, b *model.SettlementBatch) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	key := b.TenantID + "/" + b.ID
	if _, ok := s.m.batches[key]; !ok {
		return httpx.ErrNotFound
	}
	cp := *b
	s.m.batches[key] = &cp
	return nil
}

func (s *memBatches) List(_ context.Context, tenantID string) ([]model.SettlementBatch, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.SettlementBatch
	for _, b := range s.m.batches {
		if b.TenantID == tenantID {
			out = append(out, *b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}
