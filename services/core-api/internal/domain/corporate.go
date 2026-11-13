// Corporate accounts (Phase 2): consolidated monthly billing for EA bookers.
// Clients link via Client.OrgID = account ID; completed bookings in the
// period roll up into one invoice settled with payment mode INVOICE
// (net terms, e.g. 30 days) instead of per-trip card charges.
package domain

import (
	"context"
	"fmt"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type CorporateService struct{ s *Services }

func (svc *CorporateService) CreateAccount(ctx context.Context, a *model.CorporateAccount) (*model.CorporateAccount, error) {
	if a.Name == "" {
		return nil, httpx.ErrValidation
	}
	if a.ID == "" {
		a.ID = NewID()
	}
	if a.PaymentTermsDays == 0 {
		a.PaymentTermsDays = 30
	}
	if a.Currency == "" {
		a.Currency = "EUR"
	}
	a.CreatedAt = svc.s.Now()
	if err := svc.s.Store.Corporate().CreateAccount(ctx, a); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, a.TenantID, "corporate", a.ID, "corporate.account_created", nil)
	return a, nil
}

func (svc *CorporateService) ListAccounts(ctx context.Context, tenantID string) ([]model.CorporateAccount, error) {
	return svc.s.Store.Corporate().ListAccounts(ctx, tenantID)
}

// GenerateInvoice rolls up all completed, quoted bookings for the account's
// clients in [periodStart, periodEnd) into one invoice. Idempotent per
// (account, period) via the caller-supplied key.
func (svc *CorporateService) GenerateInvoice(ctx context.Context, tenantID, accountID string, periodStart, periodEnd time.Time, idem string) (*model.CorporateInvoice, error) {
	if idem == "" || periodEnd.Before(periodStart) {
		return nil, httpx.ErrValidation
	}
	account, err := svc.s.Store.Corporate().GetAccount(ctx, tenantID, accountID)
	if err != nil {
		return nil, err
	}
	// Roll up: bookings confirmed (created) inside the period, belonging to
	// the account's clients. (At Phase-2 volume a scan is fine; the lakehouse
	// silver layer takes over reporting at scale.)
	bookings, err := svc.s.Store.Bookings().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var total int64
	seen := map[string]bool{}
	for _, b := range bookings {
		if b.CreatedAt.Before(periodStart) || !b.CreatedAt.Before(periodEnd) {
			continue
		}
		if seen[b.ID] || b.QuoteID == "" {
			continue
		}
		sr, err := svc.s.Store.Requests().Get(ctx, tenantID, b.RequestID)
		if err != nil {
			continue
		}
		c, err := svc.s.Store.Clients().Get(ctx, tenantID, sr.ClientID)
		if err != nil || c.OrgID != accountID {
			continue
		}
		q, err := svc.s.Store.Quotes().Get(ctx, tenantID, b.QuoteID)
		if err != nil {
			continue
		}
		seen[b.ID] = true
		total += q.TotalMinor
	}
	if account.CreditLimitMinor > 0 && total > account.CreditLimitMinor {
		return nil, fmt.Errorf("%w: invoice total exceeds account credit limit", httpx.ErrConflict)
	}
	inv := &model.CorporateInvoice{
		ID: NewID(), TenantID: tenantID, AccountID: accountID,
		PeriodStart: periodStart, PeriodEnd: periodEnd,
		TotalMinor: total, Currency: account.Currency, State: "ISSUED",
		IdempotencyKey: idem, CreatedAt: svc.s.Now(),
	}
	if err := svc.s.Store.Corporate().CreateInvoice(ctx, inv); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "corporate", inv.ID, "corporate.invoice_issued",
		map[string]any{"account_id": accountID, "total_minor": total, "currency": inv.Currency})
	return inv, nil
}

func (svc *CorporateService) ListInvoices(ctx context.Context, tenantID, accountID string) ([]model.CorporateInvoice, error) {
	return svc.s.Store.Corporate().ListInvoices(ctx, tenantID, accountID)
}

// MarkInvoicePaid settles the invoice into the ledger: same split settlement
// as card capture (Spec section 11.3), because money truth must not depend
// on the collection rail.
func (svc *CorporateService) MarkInvoicePaid(ctx context.Context, tenantID, invoiceID string) (*model.CorporateInvoice, error) {
	inv, err := svc.s.Store.Corporate().GetInvoice(ctx, tenantID, invoiceID)
	if err != nil {
		return nil, err
	}
	if inv.State != "ISSUED" {
		return nil, httpx.ErrConflict
	}
	tenant, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	fee := PlatformFeeMinor(tenant.PricingRules, inv.TotalMinor)
	net := inv.TotalMinor - fee
	if err := svc.s.Payments.post(ctx, tenantID, "",
		"CLIENT_CLEARING", "TENANT_REVENUE", net, inv.Currency, "invoice:net:"+inv.ID); err != nil {
		return nil, err
	}
	if err := svc.s.Payments.post(ctx, tenantID, "",
		"CLIENT_CLEARING", "PLATFORM_FEES", fee, inv.Currency, "invoice:fee:"+inv.ID); err != nil {
		return nil, err
	}
	if err := svc.s.Store.Corporate().UpdateInvoiceState(ctx, tenantID, invoiceID, "PAID"); err != nil {
		return nil, err
	}
	inv.State = "PAID"
	svc.s.Events.Emit(ctx, tenantID, "corporate", inv.ID, "corporate.invoice_paid",
		map[string]any{"total_minor": inv.TotalMinor})
	return inv, nil
}
