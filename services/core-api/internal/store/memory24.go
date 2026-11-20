package store

// Phase 66: in-memory bill-to-desk folio store — one folio per
// (tenant, partner, period, currency); accrual upserts the OPEN folio.

import (
	"context"
	"sort"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (m *memory) Folios() FolioStore { return &memFolios{m} }

type memFolios struct{ m *memory }

func folioKey(tenantID, partnerID, period, currency string) string {
	return tenantID + "/" + partnerID + "/" + period + "/" + currency
}

func copyFolio(f *model.Folio) model.Folio {
	cp := *f
	cp.Lines = append([]model.FolioLine(nil), f.Lines...)
	return cp
}

func (s *memFolios) AccrueLine(_ context.Context, f *model.Folio, line model.FolioLine) (bool, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	key := folioKey(f.TenantID, f.PartnerID, f.Period, f.Currency)
	existing, ok := s.m.folios[key]
	if !ok {
		cp := copyFolio(f)
		cp.Lines = []model.FolioLine{line}
		cp.TotalMinor = line.AmountMinor
		s.m.folios[key] = &cp
		return true, nil
	}
	for _, l := range existing.Lines {
		if l.BookingID == line.BookingID {
			return false, nil // idempotent: one booking bills once
		}
	}
	if existing.State != model.FolioOpen {
		return false, httpx.ErrConflict // never accrue onto an issued/settled folio
	}
	existing.Lines = append(existing.Lines, line)
	existing.TotalMinor += line.AmountMinor
	return false, nil
}

func (s *memFolios) ListForPartner(_ context.Context, tenantID, partnerID string) ([]model.Folio, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	out := []model.Folio{}
	for _, f := range s.m.folios {
		if f.TenantID == tenantID && f.PartnerID == partnerID {
			out = append(out, copyFolio(f))
		}
	}
	sort.Slice(out, func(i, j int) bool { // newest period first, then currency
		if out[i].Period != out[j].Period {
			return out[i].Period > out[j].Period
		}
		return out[i].Currency < out[j].Currency
	})
	return out, nil
}

func (s *memFolios) Get(_ context.Context, tenantID, id string) (*model.Folio, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	for _, f := range s.m.folios {
		if f.TenantID == tenantID && f.ID == id {
			cp := copyFolio(f)
			return &cp, nil
		}
	}
	return nil, httpx.ErrNotFound
}

func (s *memFolios) Update(_ context.Context, f *model.Folio) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	key := folioKey(f.TenantID, f.PartnerID, f.Period, f.Currency)
	existing, ok := s.m.folios[key]
	if !ok || existing.ID != f.ID {
		return httpx.ErrNotFound
	}
	cp := copyFolio(f)
	if cp.Lines == nil {
		cp.Lines = existing.Lines // state transitions never drop the lines
	}
	s.m.folios[key] = &cp
	return nil
}
