//go:build pg

package store

// Postgres integration tests — Phase 56: partner integration columns from
// migration 0036 (partners.commission_pct, api_keys.partner_id,
// webhook_subscriptions.partner_id) round-trip through the pg CRUD, and
// partner-scoped rows coexist with tenant-wide rows. Gate: CE_PG_TEST_DSN.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGPartnerIntegration56(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-partner56")

	// Partner with commission rate.
	p := &model.Partner{
		ID: newTestUUID(), TenantID: tenant, Name: "Younique Tailor Tours",
		Verticals: []string{"TOURS"}, Status: "VETTED", PayoutRail: "MANUAL",
		CommissionPct: 12.5, CreatedAt: time.Now().UTC(),
	}
	if err := s.Partners().Create(ctx, p); err != nil {
		t.Fatalf("create partner: %v", err)
	}
	got, err := s.Partners().Get(ctx, tenant, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CommissionPct != 12.5 {
		t.Fatalf("commission_pct round-trip: %v", got.CommissionPct)
	}
	// Update path persists the rate too.
	got.CommissionPct = 15
	if err := s.Partners().Update(ctx, got); err != nil {
		t.Fatalf("update partner: %v", err)
	}
	again, err := s.Partners().Get(ctx, tenant, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.CommissionPct != 15 {
		t.Fatalf("commission_pct update: %v", again.CommissionPct)
	}

	// Partner-scoped API key alongside a tenant-wide key.
	pk := &model.ApiKey{
		ID: newTestUUID(), TenantID: tenant, Name: "younique-sync",
		Prefix: "cek_" + newTestUUID()[:8], SecretHash: "hash56a",
		Scopes: []string{"partner"}, PartnerID: p.ID, CreatedAt: time.Now().UTC(),
	}
	if err := s.ApiKeys().Create(ctx, pk); err != nil {
		t.Fatalf("create partner key: %v", err)
	}
	tk := &model.ApiKey{
		ID: newTestUUID(), TenantID: tenant, Name: "ops",
		Prefix: "cek_" + newTestUUID()[:8], SecretHash: "hash56b",
		Scopes: []string{"read"}, CreatedAt: time.Now().UTC(),
	}
	if err := s.ApiKeys().Create(ctx, tk); err != nil {
		t.Fatalf("create tenant key: %v", err)
	}
	gotKey, err := s.ApiKeys().GetByPrefix(ctx, pk.Prefix)
	if err != nil {
		t.Fatal(err)
	}
	if gotKey.PartnerID != p.ID {
		t.Fatalf("api key partner_id round-trip: %q", gotKey.PartnerID)
	}
	gotTenantKey, err := s.ApiKeys().GetByPrefix(ctx, tk.Prefix)
	if err != nil {
		t.Fatal(err)
	}
	if gotTenantKey.PartnerID != "" {
		t.Fatalf("tenant-wide key must have empty partner_id: %q", gotTenantKey.PartnerID)
	}

	// Partner-scoped webhook subscription via create + update.
	sub := &model.WebhookSubscription{
		ID: newTestUUID(), TenantID: tenant, URL: "https://younique.example.com/hook",
		Events: []string{"assignment.*"}, Secret: "whk_56", Active: true,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.Webhooks().CreateSubscription(ctx, sub); err != nil {
		t.Fatalf("create sub: %v", err)
	}
	sub.PartnerID = p.ID
	if err := s.Webhooks().UpdateSubscription(ctx, sub); err != nil {
		t.Fatalf("update sub: %v", err)
	}
	gotSub, err := s.Webhooks().GetSubscription(ctx, tenant, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotSub.PartnerID != p.ID {
		t.Fatalf("webhook partner_id round-trip: %q", gotSub.PartnerID)
	}
}
