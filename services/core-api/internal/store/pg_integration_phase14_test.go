//go:build pg

// Postgres integration tests — Phase 14: outreach sends (idempotency,
// frequency-cap window queries, RLS), attribution list/funnel, client
// referral-code lookup. Same gate as pg_integration_test.go.
package store

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPGOutreachSends(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-outreach")
	other := pgTenant(t, s, "pg-outreach-other")

	c := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Outreach Target",
		Phone: "+971500000100", KYCStatus: "VERIFIED", Consent: map[string]any{"marketing": true}}
	if err := s.Clients().Upsert(ctx, c); err != nil {
		t.Fatalf("client: %v", err)
	}

	now := time.Now().UTC()
	send := &model.OutreachSend{
		ID: newTestUUID(), TenantID: tenant, ClientID: c.ID, Channel: "whatsapp",
		Purpose: model.OutreachMarketing, Body: "offer", ExternalRef: "wamid.pg-1",
		Status: "sent", Detail: "freeform", IdempotencyKey: "pg-out-1", CreatedAt: now,
		Params: []string{"Ada"},
	}
	if err := s.Outreach().AddSend(ctx, send); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Idempotent replay: same (tenant, key) -> ErrConflict.
	dup := *send
	dup.ID = newTestUUID()
	if err := s.Outreach().AddSend(ctx, &dup); err != httpx.ErrConflict {
		t.Fatalf("replay must conflict, got %v", err)
	}

	// Frequency-cap window: recent send is inside, old send outside.
	old := *send
	old.ID = newTestUUID()
	old.IdempotencyKey = "pg-out-old"
	old.CreatedAt = now.Add(-8 * 24 * time.Hour)
	if err := s.Outreach().AddSend(ctx, &old); err != nil {
		t.Fatalf("add old: %v", err)
	}
	window, err := s.Outreach().SendsForClient(ctx, tenant, c.ID, now.Add(-7*24*time.Hour))
	if err != nil || len(window) != 1 || window[0].IdempotencyKey != "pg-out-1" {
		t.Fatalf("window: %+v, %v", window, err)
	}
	if len(window[0].Params) != 1 || window[0].Params[0] != "Ada" {
		t.Fatalf("params roundtrip: %+v", window[0].Params)
	}

	list, err := s.Outreach().ListSends(ctx, tenant, 10)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %+v, %v", list, err)
	}
	if list[0].CreatedAt.Before(list[1].CreatedAt) {
		t.Fatal("list must be newest-first")
	}

	// RLS: the other tenant sees nothing.
	cross, err := s.Outreach().ListSends(ctx, other, 10)
	if err != nil || len(cross) != 0 {
		t.Fatalf("RLS leak: %+v, %v", cross, err)
	}
}

func TestPGAttributionListAndFunnel(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-funnel")
	clientID := newTestUUID()

	s.Attribution().Track(ctx, &model.AttributionEvent{TenantID: tenant, Kind: "page_view",
		Channel: "referral", Campaign: "ref:VIP-ABCDEF12", ClientID: clientID})
	s.Attribution().Track(ctx, &model.AttributionEvent{TenantID: tenant, Kind: "conversation_started",
		Channel: "whatsapp", ClientID: clientID})
	s.Attribution().Track(ctx, &model.AttributionEvent{TenantID: tenant, Kind: "booking",
		Channel: "referral", Campaign: "ref:VIP-ABCDEF12", ClientID: clientID, ValueMinor: 42000})

	list, err := s.Attribution().List(ctx, tenant, clientID, 10)
	if err != nil || len(list) != 3 {
		t.Fatalf("list: %+v, %v", list, err)
	}
	if list[0].Kind != "page_view" {
		t.Fatalf("list must be oldest-first (first touch), got %+v", list[0])
	}

	rows, err := s.Attribution().Funnel(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	var booking *FunnelRow
	for i := range rows {
		if rows[i].Kind == "booking" {
			booking = &rows[i]
		}
	}
	if booking == nil || booking.Count != 1 || booking.ValueMinor != 42000 {
		t.Fatalf("funnel booking cell: %+v (all %+v)", booking, rows)
	}
}

func TestPGFindByReferralCode(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-refcode")

	c := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Referrer",
		Phone: "+971500000101", KYCStatus: "VERIFIED", Consent: map[string]any{}}
	if err := s.Clients().Upsert(ctx, c); err != nil {
		t.Fatalf("client: %v", err)
	}
	code := model.ClientReferralCode(c.ID)
	found, err := s.Clients().FindByReferralCode(ctx, tenant, code)
	if err != nil || found.ID != c.ID {
		t.Fatalf("lookup: %+v, %v", found, err)
	}
	if _, err := s.Clients().FindByReferralCode(ctx, tenant, "VIP-00000000"); err == nil {
		t.Fatal("unknown code must not resolve")
	}
}
