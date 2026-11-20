//go:build pg

package store

// Postgres integration tests — Phase 67: the reminders jsonb ledger on
// partner_folios (round-trip through Update/Get), the ListIssued working
// set (state filter + oldest-first), and RLS isolation for the sweep.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGFolioDunning67(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-fl67")
	now := time.Now().UTC().Truncate(time.Second)

	hotel := &model.Partner{ID: newTestUUID(), TenantID: tenant, Name: "PG Dun Hotel",
		Verticals: []string{"CHAUFFEUR"}, Status: "VETTED", QualityScore: 8,
		PayoutRail: "MANUAL", Kind: model.PartnerKindHotel, BillToDesk: true,
		Phone: "+31695550999", CreatedAt: now}
	if err := s.Partners().Create(ctx, hotel); err != nil {
		t.Fatal(err)
	}

	mk := func(period, currency string, amount int64) *model.Folio {
		f := &model.Folio{ID: newTestUUID(), TenantID: tenant, PartnerID: hotel.ID,
			Period: period, Currency: currency, State: model.FolioOpen, CreatedAt: now}
		line := model.FolioLine{BookingID: newTestUUID(), QuoteID: newTestUUID(),
			AmountMinor: amount, CreatedAt: now}
		if _, err := s.Folios().AccrueLine(ctx, f, line); err != nil {
			t.Fatal(err)
		}
		got, err := s.Folios().Get(ctx, tenant, f.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	old := mk("2026-09", "EUR", 9000)
	recent := mk("2026-10", "EUR", 12000)

	// Issue both, oldest first; a reminders row rides through Update.
	old.State = model.FolioIssued
	old.IssuedAt = now.Add(-40 * 24 * time.Hour)
	old.Reminders = []model.FolioReminder{
		{N: 1, Channel: "whatsapp", Status: "sent", CreatedAt: now.Add(-24 * time.Hour)},
		{N: 2, Channel: "whatsapp", Status: "skipped:no-contact", CreatedAt: now.Add(-time.Hour)},
	}
	if err := s.Folios().Update(ctx, old); err != nil {
		t.Fatal(err)
	}
	recent.State = model.FolioIssued
	recent.IssuedAt = now.Add(-2 * 24 * time.Hour)
	if err := s.Folios().Update(ctx, recent); err != nil {
		t.Fatal(err)
	}

	// ListIssued: both, oldest issue first; reminders round-trip verbatim.
	issued, err := s.Folios().ListIssued(ctx, tenant)
	if err != nil || len(issued) != 2 {
		t.Fatalf("list issued: %+v %v", issued, err)
	}
	if issued[0].ID != old.ID || issued[1].ID != recent.ID {
		t.Fatalf("oldest first: %+v", issued)
	}
	if len(issued[0].Reminders) != 2 || issued[0].Reminders[1].Status != "skipped:no-contact" ||
		issued[0].Reminders[0].N != 1 {
		t.Fatalf("reminders round-trip: %+v", issued[0].Reminders)
	}

	// A state-only update (settle) carries the loaded reminders through —
	// and the settled folio leaves the working set.
	if err := s.Folios().Update(ctx, &model.Folio{
		ID: old.ID, TenantID: tenant, PartnerID: hotel.ID, Period: old.Period,
		Currency: old.Currency, State: model.FolioSettled, TotalMinor: old.TotalMinor,
		Lines: old.Lines, Reminders: old.Reminders,
		CreatedAt: old.CreatedAt, IssuedAt: old.IssuedAt, SettledAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Folios().Get(ctx, tenant, old.ID)
	if err != nil || got.State != model.FolioSettled || len(got.Reminders) != 2 {
		t.Fatalf("settled with ledger: %+v %v", got, err)
	}
	issued, err = s.Folios().ListIssued(ctx, tenant)
	if err != nil || len(issued) != 1 || issued[0].ID != recent.ID {
		t.Fatalf("settled leaves working set: %+v %v", issued, err)
	}

	// RLS: another tenant's sweep sees nothing.
	other := pgTenant(t, s, "pg-fl67-b")
	if got, err := s.Folios().ListIssued(ctx, other); err != nil || len(got) != 0 {
		t.Fatalf("cross-tenant list issued: %+v %v", got, err)
	}
}
