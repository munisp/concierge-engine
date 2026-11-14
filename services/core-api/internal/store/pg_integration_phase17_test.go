//go:build pg

// Postgres integration tests — Phase 17: quote tax component roundtrip and
// tenant tax/payment config persistence. Same gate as pg_integration_test.go.
package store

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGQuoteTaxAndTenantConfig(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-tax")

	// Tenant config roundtrip.
	tn, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("tenant get: %v", err)
	}
	tn.TaxConfig = model.TaxConfig{Engine: "flat", Country: "DE", VATRateBP: 1900, PricesIncludeTax: false}
	tn.PaymentConfig = model.PaymentConfig{DisablePaystack: true}
	if err := s.Tenants().UpdateConfig(ctx, tn); err != nil {
		t.Fatalf("tenant update: %v", err)
	}
	tn, err = s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("tenant reget: %v", err)
	}
	if tn.TaxConfig.VATRateBP != 1900 || tn.TaxConfig.Country != "DE" || !tn.PaymentConfig.DisablePaystack {
		t.Fatalf("tenant config roundtrip: %+v %+v", tn.TaxConfig, tn.PaymentConfig)
	}

	// Quote with tax component roundtrips.
	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Tax Subject",
		KYCStatus: "none", Consent: map[string]any{}}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatalf("client: %v", err)
	}
	req := &model.ServiceRequest{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		State: "NEW", Version: 1, IdempotencyKey: "pg-tax-r1",
		Items:    []model.ServiceItem{{ID: newTestUUID(), TenantID: tenant, Vertical: "CHAUFFEUR_POINT_TO_POINT"}},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := s.Requests().Create(ctx, req); err != nil {
		t.Fatalf("request: %v", err)
	}
	q := &model.Quote{ID: newTestUUID(), TenantID: tenant, RequestID: req.ID,
		Version: 1, Origin: "INSTANT", TotalMinor: 11900, DepositMinor: 3570,
		Currency: "EUR", TaxMinor: 1900, TaxEngine: "flat",
		Lines: []model.QuoteLine{{Description: "base", AmountMinor: 10000},
			{Description: "VAT 19.0% (DE)", AmountMinor: 1900}},
		ExpiresAt: time.Now().Add(time.Hour).UTC(), CreatedAt: time.Now().UTC()}
	if err := s.Quotes().Create(ctx, q); err != nil {
		t.Fatalf("quote create: %v", err)
	}
	got, err := s.Quotes().Get(ctx, tenant, q.ID)
	if err != nil {
		t.Fatalf("quote get: %v", err)
	}
	if got.TaxMinor != 1900 || got.TaxEngine != "flat" || len(got.Lines) != 2 {
		t.Fatalf("tax roundtrip: %+v", got)
	}
}
