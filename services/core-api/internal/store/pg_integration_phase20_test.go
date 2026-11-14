//go:build pg

// Postgres integration tests — Phase 20: promo campaign lifecycle (create
// idempotency, case-insensitive lookup, redemption counting), promo reward
// accrual with NULL source_booking_id + once-per-client partial unique, and
// tenant outreach_config roundtrip. Same gate as pg_integration_test.go.
package store

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPGPromoLifecycle(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-promo")

	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Promo Client",
		KYCStatus: "none", Consent: map[string]any{}}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatalf("client: %v", err)
	}

	c := &model.PromoCampaign{ID: newTestUUID(), TenantID: tenant, Code: "summer25",
		CreditMinor: 2500, Currency: "EUR", MaxRedemptions: 10, Active: true,
		CreatedAt: time.Now().UTC()}
	if err := s.Promos().Create(ctx, c); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Idempotent per (tenant, code).
	dup := *c
	dup.ID = newTestUUID()
	if err := s.Promos().Create(ctx, &dup); err != httpx.ErrConflict {
		t.Fatalf("duplicate campaign: want ErrConflict, got %v", err)
	}
	// Case-insensitive lookup.
	got, err := s.Promos().GetByCode(ctx, tenant, "SUMMER25")
	if err != nil || got.Code != "SUMMER25" || got.CreditMinor != 2500 {
		t.Fatalf("get by code: %+v %v", got, err)
	}
	list, err := s.Promos().List(ctx, tenant)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}

	// Promo reward accrual: NULL source_booking, promo_code set.
	rw := &model.ClientReward{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		Kind: "promo_credit", PromoCode: "SUMMER25", AmountMinor: 2500,
		Currency: "EUR", State: model.RewardAccrued, CreatedAt: time.Now().UTC()}
	if err := s.Rewards().Accrue(ctx, rw); err != nil {
		t.Fatalf("promo accrual (NULL source_booking): %v", err)
	}
	// Once per client per code — the partial UNIQUE index.
	dupR := *rw
	dupR.ID = newTestUUID()
	if err := s.Rewards().Accrue(ctx, &dupR); err != httpx.ErrConflict {
		t.Fatalf("promo re-redemption: want ErrConflict, got %v", err)
	}
	n, err := s.Promos().Redemptions(ctx, tenant, "summer25")
	if err != nil || n != 1 {
		t.Fatalf("redemptions: want 1, got %d (%v)", n, err)
	}
	// Balance and history carry the promo credit with its code.
	bal, err := s.Rewards().Balance(ctx, tenant, cl.ID)
	if err != nil || bal != 2500 {
		t.Fatalf("balance: want 2500, got %d (%v)", bal, err)
	}
	history, err := s.Rewards().List(ctx, tenant, cl.ID)
	if err != nil || len(history) != 1 {
		t.Fatalf("history: %+v %v", history, err)
	}
	if history[0].PromoCode != "SUMMER25" || history[0].SourceBookingID != "" {
		t.Fatalf("history roundtrip: %+v", history[0])
	}
}

func TestPGOutreachConfig(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-outreach-cfg")

	tn, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("tenant get: %v", err)
	}
	if tn.OutreachConfig.MarketingCap7d != 0 {
		t.Fatalf("default outreach config must be zero-value: %+v", tn.OutreachConfig)
	}
	tn.OutreachConfig.MarketingCap7d = 5
	if err := s.Tenants().UpdateConfig(ctx, tn); err != nil {
		t.Fatalf("tenant update: %v", err)
	}
	tn, err = s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("tenant reget: %v", err)
	}
	if tn.OutreachConfig.MarketingCap7d != 5 {
		t.Fatalf("outreach_config roundtrip: %+v", tn.OutreachConfig)
	}
}
