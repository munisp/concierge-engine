//go:build pg

package store

// Postgres integration tests — Phase 71: the deliveries jsonb ledger on
// partner_folios (verbatim round-trip), the partners.email billing
// contact round-trip, and RLS isolation over both.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGFolioDeliveries71(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-fl71")
	now := time.Now().UTC().Truncate(time.Second)

	hotel := &model.Partner{ID: newTestUUID(), TenantID: tenant, Name: "PG Statement Hotel",
		Verticals: []string{"CHAUFFEUR"}, Status: "VETTED", QualityScore: 8,
		PayoutRail: "MANUAL", Kind: model.PartnerKindHotel, BillToDesk: true,
		Email: "accounts@pgstatement.example.com", CreatedAt: now}
	if err := s.Partners().Create(ctx, hotel); err != nil {
		t.Fatal(err)
	}

	// Partner email round-trips through create/read and survives update.
	got, err := s.Partners().Get(ctx, tenant, hotel.ID)
	if err != nil || got.Email != "accounts@pgstatement.example.com" {
		t.Fatalf("email round-trip: %+v %v", got, err)
	}
	got.Email = "billing@pgstatement.example.com"
	if err := s.Partners().Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	got, err = s.Partners().Get(ctx, tenant, hotel.ID)
	if err != nil || got.Email != "billing@pgstatement.example.com" {
		t.Fatalf("email update round-trip: %+v %v", got, err)
	}

	f := &model.Folio{ID: newTestUUID(), TenantID: tenant, PartnerID: hotel.ID,
		Period: "2026-11", Currency: "EUR", State: model.FolioOpen, CreatedAt: now}
	line := model.FolioLine{BookingID: newTestUUID(), QuoteID: newTestUUID(),
		AmountMinor: 9000, CreatedAt: now}
	if _, err := s.Folios().AccrueLine(ctx, f, line); err != nil {
		t.Fatal(err)
	}
	f, err = s.Folios().Get(ctx, tenant, f.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Issue, then ride two deliveries (one sent, one honest skip)
	// through Update verbatim.
	f.State = model.FolioIssued
	f.IssuedAt = now.Add(-2 * 24 * time.Hour)
	f.Deliveries = []model.FolioDelivery{
		{ID: newTestUUID(), Kind: "statement", Channel: "whatsapp", Status: "sent",
			CreatedAt: now.Add(-time.Hour)},
		{ID: newTestUUID(), Kind: "statement", Channel: "email", Status: "skipped:no-adapter",
			CreatedAt: now},
	}
	if err := s.Folios().Update(ctx, f); err != nil {
		t.Fatal(err)
	}
	gotF, err := s.Folios().Get(ctx, tenant, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotF.Deliveries) != 2 || gotF.Deliveries[0].Status != "sent" ||
		gotF.Deliveries[1].Status != "skipped:no-adapter" ||
		gotF.Deliveries[1].Channel != "email" {
		t.Fatalf("deliveries round-trip: %+v", gotF.Deliveries)
	}
	if gotF.Deliveries[0].CreatedAt != now.Add(-time.Hour) {
		t.Fatalf("created_at round-trip: %v", gotF.Deliveries[0].CreatedAt)
	}

	// RLS: another tenant cannot see the folio or the partner's email.
	other := pgTenant(t, s, "pg-fl71b")
	if _, err := s.Folios().Get(ctx, other, f.ID); err == nil {
		t.Fatal("cross-tenant folio read must fail")
	}
	if _, err := s.Partners().Get(ctx, other, hotel.ID); err == nil {
		t.Fatal("cross-tenant partner read must fail")
	}
}
