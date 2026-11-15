//go:build pg

// Postgres integration tests — Phase 40: campaign_spend round-trips through
// RLS with partial-UNIQUE idempotency, and the attribution spine's
// CountSince reads the 7-day deliverability window from the `at` column.
// Gate: CE_PG_TEST_DSN. Requires db/migrations/0028_phase40.sql applied.
package store

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPGCampaignSpend40(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-spend")

	sp := &model.CampaignSpend{
		ID: newTestUUID(), TenantID: tenant, Campaign: "lagos-launch",
		Channel: "paid_search", AmountMinor: 100000, Currency: "NGN",
		SpentOn: "2026-11-10", Note: "Google Ads week 1",
		IdempotencyKey: "p40-sp-1", CreatedAt: time.Now().UTC(),
	}
	if err := s.CampaignSpend().Add(ctx, sp); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Keyed replay conflicts (the domain reloads the original).
	dup := *sp
	dup.ID = newTestUUID()
	if err := s.CampaignSpend().Add(ctx, &dup); err != httpx.ErrConflict {
		t.Fatalf("replay: %v", err)
	}
	// Unkeyed entries always insert.
	unkeyed := *sp
	unkeyed.ID = newTestUUID()
	unkeyed.IdempotencyKey = ""
	if err := s.CampaignSpend().Add(ctx, &unkeyed); err != nil {
		t.Fatalf("unkeyed: %v", err)
	}

	rows, err := s.CampaignSpend().List(ctx, tenant, "lagos-launch", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if rows[0].SpentOn != "2026-11-10" || rows[0].Currency != "NGN" || rows[0].AmountMinor != 100000 {
		t.Fatalf("roundtrip: %+v", rows[0])
	}
	// Campaign filter + tenant isolation.
	if rows, _ := s.CampaignSpend().List(ctx, tenant, "other", 10); len(rows) != 0 {
		t.Fatalf("campaign filter: %d rows", len(rows))
	}
	other := pgTenant(t, s, "pg-spend-b")
	if rows, _ := s.CampaignSpend().List(ctx, other, "", 10); len(rows) != 0 {
		t.Fatalf("tenant isolation: %d rows leaked", len(rows))
	}
}

func TestPGAttributionCountSince40(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-countsince")
	now := time.Now().UTC()

	mk := func(kind, channel string, at time.Time) *model.AttributionEvent {
		return &model.AttributionEvent{TenantID: tenant, Kind: kind, Channel: channel, At: at}
	}
	// 3 email sends inside the window, 1 outside it, 1 on whatsapp.
	for i := 0; i < 3; i++ {
		if err := s.Attribution().Track(ctx, mk("outreach_sent", "email", now.Add(-time.Hour))); err != nil {
			t.Fatalf("track: %v", err)
		}
	}
	if err := s.Attribution().Track(ctx, mk("outreach_sent", "email", now.Add(-8*24*time.Hour))); err != nil {
		t.Fatalf("track old: %v", err)
	}
	if err := s.Attribution().Track(ctx, mk("outreach_sent", "whatsapp", now.Add(-time.Hour))); err != nil {
		t.Fatalf("track wa: %v", err)
	}
	if err := s.Attribution().Track(ctx, mk("email_bounced", "email", now.Add(-time.Hour))); err != nil {
		t.Fatalf("track bounce: %v", err)
	}
	// Zero At falls back to now() — inside the window.
	if err := s.Attribution().Track(ctx, &model.AttributionEvent{
		TenantID: tenant, Kind: "email_bounced", Channel: "email"}); err != nil {
		t.Fatalf("track zero-at: %v", err)
	}

	since := now.Add(-7 * 24 * time.Hour)
	if n, err := s.Attribution().CountSince(ctx, tenant, "outreach_sent", "email", since); err != nil || n != 3 {
		t.Fatalf("sent count = %d, want 3 (%v)", n, err)
	}
	if n, err := s.Attribution().CountSince(ctx, tenant, "outreach_sent", "", since); err != nil || n != 4 {
		t.Fatalf("all-channel count = %d, want 4 (%v)", n, err)
	}
	if n, err := s.Attribution().CountSince(ctx, tenant, "email_bounced", "", since); err != nil || n != 2 {
		t.Fatalf("bounce count = %d, want 2 (%v)", n, err)
	}
	// Tenant isolation.
	other := pgTenant(t, s, "pg-countsince-b")
	if n, _ := s.Attribution().CountSince(ctx, other, "outreach_sent", "email", since); n != 0 {
		t.Fatalf("tenant isolation: %d events leaked", n)
	}
}
