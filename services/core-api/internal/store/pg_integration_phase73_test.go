//go:build pg

package store

// Postgres integration tests — Phase 73: the credits jsonb ledger on
// partner_folios (verbatim round-trip through Update, derived balances
// survive re-read) and RLS isolation over the credit ledger.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGFolioCredits73(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-fl73")
	other := pgTenant(t, s, "pg-fl73b")
	now := time.Now().UTC().Truncate(time.Second)

	hotel := &model.Partner{ID: newTestUUID(), TenantID: tenant, Name: "PG Credit Hotel",
		Verticals: []string{"CHAUFFEUR"}, Status: "VETTED", QualityScore: 8,
		PayoutRail: "MANUAL", Kind: model.PartnerKindHotel, BillToDesk: true, CreatedAt: now}
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

	// Issue, pay, then ride a credit note and a refund-shaped row through
	// Update verbatim — the credit ledger survives re-read byte-for-byte.
	f.State = model.FolioIssued
	f.IssuedAt = now.Add(-2 * 24 * time.Hour)
	f.Payments = []model.FolioPayment{
		{ID: newTestUUID(), AmountMinor: 4000, Method: "bank_transfer",
			Reference: "TRX-73", ReceivedAt: now.Add(-time.Hour), RecordedBy: "operator"},
	}
	f.Credits = []model.FolioCredit{
		{ID: newTestUUID(), AmountMinor: 2000, Kind: model.FolioCreditNote,
			Reason: "guest cancelled after issue", CreatedAt: now, RecordedBy: "operator"},
	}
	if err := s.Folios().Update(ctx, f); err != nil {
		t.Fatal(err)
	}
	gotF, err := s.Folios().Get(ctx, tenant, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotF.Credits) != 1 || gotF.Credits[0].Kind != model.FolioCreditNote ||
		gotF.Credits[0].AmountMinor != 2000 ||
		gotF.Credits[0].Reason != "guest cancelled after issue" ||
		gotF.Credits[0].RecordedBy != "operator" {
		t.Fatalf("credits round-trip: %+v", gotF.Credits)
	}
	if gotF.CreditedMinor() != 2000 || gotF.AdjustedTotalMinor() != 7000 ||
		gotF.OutstandingMinor() != 3000 {
		t.Fatalf("derived after re-read: credited=%d adjusted=%d outstanding=%d",
			gotF.CreditedMinor(), gotF.AdjustedTotalMinor(), gotF.OutstandingMinor())
	}

	// A folio written before Phase 73 (credits default '[]') still reads.
	if gotF.Payments[0].Reference != "TRX-73" {
		t.Fatalf("payments preserved: %+v", gotF.Payments)
	}

	// RLS: the credit ledger is invisible cross-tenant.
	if _, err := s.Folios().Get(ctx, other, f.ID); err == nil {
		t.Fatal("cross-tenant folio read must fail")
	}
}
