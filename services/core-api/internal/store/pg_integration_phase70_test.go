//go:build pg

package store

// Postgres integration tests — Phase 70: the payments jsonb ledger on
// partner_folios (verbatim round-trip through Update/Get), derived
// paid/outstanding, and RLS isolation.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGFolioPayments70(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-fl70")
	now := time.Now().UTC().Truncate(time.Second)

	hotel := &model.Partner{ID: newTestUUID(), TenantID: tenant, Name: "PG Pay Hotel",
		Verticals: []string{"CHAUFFEUR"}, Status: "VETTED", QualityScore: 8,
		PayoutRail: "MANUAL", Kind: model.PartnerKindHotel, BillToDesk: true,
		CreatedAt: now}
	if err := s.Partners().Create(ctx, hotel); err != nil {
		t.Fatal(err)
	}

	f := &model.Folio{ID: newTestUUID(), TenantID: tenant, PartnerID: hotel.ID,
		Period: "2026-11", Currency: "EUR", State: model.FolioOpen, CreatedAt: now}
	line := model.FolioLine{BookingID: newTestUUID(), QuoteID: newTestUUID(),
		AmountMinor: 9000, CreatedAt: now}
	if _, err := s.Folios().AccrueLine(ctx, f, line); err != nil {
		t.Fatal(err)
	}
	f, err := s.Folios().Get(ctx, tenant, f.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Issue, then ride two payments through Update verbatim.
	f.State = model.FolioIssued
	f.IssuedAt = now.Add(-2 * 24 * time.Hour)
	f.Payments = []model.FolioPayment{
		{ID: newTestUUID(), AmountMinor: 4000, Method: "bank_transfer", Reference: "TRX-1",
			ReceivedAt: now.Add(-time.Hour), RecordedBy: "operator"},
		{ID: newTestUUID(), AmountMinor: 5000, Method: "cash",
			ReceivedAt: now, RecordedBy: "operator"},
	}
	if err := s.Folios().Update(ctx, f); err != nil {
		t.Fatal(err)
	}
	got, err := s.Folios().Get(ctx, tenant, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Payments) != 2 || got.Payments[0].Reference != "TRX-1" ||
		got.Payments[1].Method != "cash" || got.PaidMinor() != 9000 ||
		got.OutstandingMinor() != 0 {
		t.Fatalf("payments round-trip: %+v", got.Payments)
	}
	if got.Payments[0].ReceivedAt != now.Add(-time.Hour) {
		t.Fatalf("received_at round-trip: %v", got.Payments[0].ReceivedAt)
	}

	// A folio written without payments reads back as an empty ledger.
	if got.Reminders != nil && len(got.Reminders) > 0 {
		t.Fatalf("unexpected reminders: %+v", got.Reminders)
	}

	// RLS: another tenant cannot see the folio's payment ledger.
	other := pgTenant(t, s, "pg-fl70b")
	if _, err := s.Folios().Get(ctx, other, f.ID); err == nil {
		t.Fatal("cross-tenant folio read must fail")
	}
	issued, err := s.Folios().ListIssued(ctx, other)
	if err != nil || len(issued) != 0 {
		t.Fatalf("cross-tenant ListIssued: %+v %v", issued, err)
	}
}
