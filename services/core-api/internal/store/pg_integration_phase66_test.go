//go:build pg

package store

// Postgres integration tests — Phase 66: bill-to-desk folios (upsert
// accrual keyed by tenant+partner+period+currency, booking-once
// idempotency, frozen totals, state transitions) and RLS isolation, plus
// the partners.bill_to_desk column round-trip.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPGFolios66(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-fl66")
	now := time.Now().UTC().Truncate(time.Second)

	hotel := &model.Partner{ID: newTestUUID(), TenantID: tenant, Name: "PG Folio Hotel",
		Verticals: []string{"CHAUFFEUR"}, Status: "VETTED", QualityScore: 8,
		PayoutRail: "MANUAL", Kind: model.PartnerKindHotel, BillToDesk: true, CreatedAt: now}
	if err := s.Partners().Create(ctx, hotel); err != nil {
		t.Fatal(err)
	}
	// bill_to_desk column round-trips through create and update.
	got, err := s.Partners().Get(ctx, tenant, hotel.ID)
	if err != nil || !got.BillToDesk {
		t.Fatalf("bill_to_desk round-trip: %+v %v", got, err)
	}
	got.BillToDesk = false
	if err := s.Partners().Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Partners().Get(ctx, tenant, hotel.ID); got.BillToDesk {
		t.Fatal("bill_to_desk update did not persist")
	}

	// Accrual upserts the OPEN folio; the same booking never bills twice.
	f := &model.Folio{ID: newTestUUID(), TenantID: tenant, PartnerID: hotel.ID,
		Period: "2026-11", Currency: "EUR", State: model.FolioOpen, CreatedAt: now}
	line1 := model.FolioLine{BookingID: newTestUUID(), QuoteID: newTestUUID(),
		GuestName: "Guest One", AmountMinor: 8100, CreatedAt: now}
	created, err := s.Folios().AccrueLine(ctx, f, line1)
	if err != nil || !created {
		t.Fatalf("first accrual creates: %v %v", created, err)
	}
	if _, err := s.Folios().AccrueLine(ctx, f, line1); err != nil {
		t.Fatal(err) // replay: no error, no double line
	}
	line2 := model.FolioLine{BookingID: newTestUUID(), QuoteID: newTestUUID(),
		GuestName: "Guest Two", AmountMinor: 9000, CreatedAt: now}
	if _, err := s.Folios().AccrueLine(ctx, f, line2); err != nil {
		t.Fatal(err)
	}
	// A second currency for the same period is a separate folio — never blended.
	fgbp := &model.Folio{ID: newTestUUID(), TenantID: tenant, PartnerID: hotel.ID,
		Period: "2026-11", Currency: "GBP", State: model.FolioOpen, CreatedAt: now}
	lineG := model.FolioLine{BookingID: newTestUUID(), QuoteID: newTestUUID(),
		AmountMinor: 12000, CreatedAt: now}
	if _, err := s.Folios().AccrueLine(ctx, fgbp, lineG); err != nil {
		t.Fatal(err)
	}

	folios, err := s.Folios().ListForPartner(ctx, tenant, hotel.ID)
	if err != nil || len(folios) != 2 {
		t.Fatalf("list: %+v %v", folios, err)
	}
	if folios[0].Currency != "EUR" || folios[1].Currency != "GBP" {
		t.Fatalf("list order: %+v", folios)
	}
	eur := folios[0]
	if eur.TotalMinor != 17100 || len(eur.Lines) != 2 || eur.State != model.FolioOpen {
		t.Fatalf("eur folio: %+v", eur)
	}
	if eur.Lines[0].GuestName != "Guest One" || eur.Lines[1].AmountMinor != 9000 {
		t.Fatalf("lines: %+v", eur.Lines)
	}

	// Issue freezes; accrual onto an issued folio is a conflict.
	eur.State = model.FolioIssued
	eur.IssuedAt = now.Add(time.Hour)
	if err := s.Folios().Update(ctx, &eur); err != nil {
		t.Fatal(err)
	}
	line3 := model.FolioLine{BookingID: newTestUUID(), QuoteID: newTestUUID(), AmountMinor: 5000, CreatedAt: now}
	if _, err := s.Folios().AccrueLine(ctx, f, line3); err != httpx.ErrConflict {
		t.Fatalf("accrual onto issued folio: %v", err)
	}

	// Settle persists the terminal state.
	eur.State = model.FolioSettled
	eur.SettledAt = now.Add(2 * time.Hour)
	if err := s.Folios().Update(ctx, &eur); err != nil {
		t.Fatal(err)
	}
	got66, err := s.Folios().Get(ctx, tenant, eur.ID)
	if err != nil || got66.State != model.FolioSettled || got66.TotalMinor != 17100 {
		t.Fatalf("get settled: %+v %v", got66, err)
	}

	// RLS: another tenant sees nothing and cannot touch the folio.
	other := pgTenant(t, s, "pg-fl66-b")
	if got, err := s.Folios().ListForPartner(ctx, other, hotel.ID); err != nil || len(got) != 0 {
		t.Fatalf("cross-tenant list: %+v %v", got, err)
	}
	if _, err := s.Folios().Get(ctx, other, eur.ID); err != httpx.ErrNotFound {
		t.Fatalf("cross-tenant get: %v", err)
	}
	cross := eur
	cross.TenantID = other
	if err := s.Folios().Update(ctx, &cross); err != httpx.ErrNotFound {
		t.Fatalf("cross-tenant update: %v", err)
	}
}
