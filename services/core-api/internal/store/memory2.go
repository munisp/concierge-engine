// In-memory Phase 2 stores. Same tenant-scoping and idempotency discipline
// as memory.go; the Postgres implementation lives in postgres2.go (-tags pg).
package store

import (
	"context"
	"sort"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// ------------------------------------------------------------- partners
type memPartners struct{ m *memory }

func (s *memPartners) Create(_ context.Context, p *model.Partner) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *p
	s.m.partners[p.TenantID+"/"+p.ID] = &cp
	return nil
}

func (s *memPartners) Get(_ context.Context, tenantID, id string) (*model.Partner, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	p, ok := s.m.partners[tenantID+"/"+id]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (s *memPartners) List(_ context.Context, tenantID string) ([]model.Partner, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.Partner
	for _, p := range s.m.partners {
		if p.TenantID == tenantID {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *memPartners) Update(_ context.Context, p *model.Partner) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	key := p.TenantID + "/" + p.ID
	if _, ok := s.m.partners[key]; !ok {
		return httpx.ErrNotFound
	}
	cp := *p
	s.m.partners[key] = &cp
	return nil
}

// ------------------------------------------------------------- corporate
type memCorporate struct{ m *memory }

func (s *memCorporate) CreateAccount(_ context.Context, a *model.CorporateAccount) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *a
	s.m.corporate[a.TenantID+"/"+a.ID] = &cp
	return nil
}

func (s *memCorporate) GetAccount(_ context.Context, tenantID, id string) (*model.CorporateAccount, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	a, ok := s.m.corporate[tenantID+"/"+id]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *a
	return &cp, nil
}

func (s *memCorporate) ListAccounts(_ context.Context, tenantID string) ([]model.CorporateAccount, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.CorporateAccount
	for _, a := range s.m.corporate {
		if a.TenantID == tenantID {
			out = append(out, *a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *memCorporate) CreateInvoice(_ context.Context, inv *model.CorporateInvoice) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if prevID, dup := s.m.invoiceIdem[inv.TenantID+"/"+inv.IdempotencyKey]; dup {
		cp := *s.m.invoices[inv.TenantID+"/"+prevID]
		*inv = cp // idempotent replay: caller sees the original
		return nil
	}
	cp := *inv
	s.m.invoices[inv.TenantID+"/"+inv.ID] = &cp
	s.m.invoiceIdem[inv.TenantID+"/"+inv.IdempotencyKey] = inv.ID
	return nil
}

func (s *memCorporate) GetInvoice(_ context.Context, tenantID, id string) (*model.CorporateInvoice, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	inv, ok := s.m.invoices[tenantID+"/"+id]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *inv
	return &cp, nil
}

func (s *memCorporate) UpdateInvoiceState(_ context.Context, tenantID, id, state string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	inv, ok := s.m.invoices[tenantID+"/"+id]
	if !ok {
		return httpx.ErrNotFound
	}
	inv.State = state
	return nil
}

func (s *memCorporate) ListInvoices(_ context.Context, tenantID, accountID string) ([]model.CorporateInvoice, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.CorporateInvoice
	for _, inv := range s.m.invoices {
		if inv.TenantID == tenantID && (accountID == "" || inv.AccountID == accountID) {
			out = append(out, *inv)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// ------------------------------------------------------------- payouts
type memPayouts struct{ m *memory }

func (s *memPayouts) Create(_ context.Context, p *model.Payout) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if prevID, dup := s.m.payoutIdem[p.TenantID+"/"+p.IdempotencyKey]; dup {
		cp := *s.m.payouts[p.TenantID+"/"+prevID]
		*p = cp // idempotent replay
		return nil
	}
	cp := *p
	s.m.payouts[p.TenantID+"/"+p.ID] = &cp
	s.m.payoutIdem[p.TenantID+"/"+p.IdempotencyKey] = p.ID
	return nil
}

func (s *memPayouts) Get(_ context.Context, tenantID, id string) (*model.Payout, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	p, ok := s.m.payouts[tenantID+"/"+id]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (s *memPayouts) GetByTransfer(_ context.Context, tenantID, transferID string) (*model.Payout, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	for _, p := range s.m.payouts {
		if p.TenantID == tenantID && p.TransferID == transferID {
			cp := *p
			return &cp, nil
		}
	}
	return nil, httpx.ErrNotFound
}

func (s *memPayouts) Update(_ context.Context, p *model.Payout) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	key := p.TenantID + "/" + p.ID
	if _, ok := s.m.payouts[key]; !ok {
		return httpx.ErrNotFound
	}
	cp := *p
	s.m.payouts[key] = &cp
	return nil
}

func (s *memPayouts) List(_ context.Context, tenantID string) ([]model.Payout, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.Payout
	for _, p := range s.m.payouts {
		if p.TenantID == tenantID {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// ------------------------------------------------------------- audit
type memAudit struct{ m *memory }
func (s *memAudit) Log(_ context.Context, e *model.AuditEntry) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	e.ID = int64(len(s.m.audit) + 1)
	s.m.audit = append(s.m.audit, *e)
	return nil
}

func (s *memAudit) List(_ context.Context, tenantID string, limit int) ([]model.AuditEntry, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.AuditEntry
	for _, e := range s.m.audit {
		if e.TenantID == tenantID {
			out = append(out, e)
		}
	}
	// newest first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// List on memBookings lives here with the other Phase 2 store extensions.
func (s *memBookings) List(_ context.Context, tenantID string) ([]model.Booking, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.Booking
	for _, b := range s.m.bookings {
		if b.TenantID == tenantID {
			out = append(out, *b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}
