//go:build pg

package store

// Postgres integration tests — Phase 65: partner rate cards (upsert keyed
// by tenant+partner+vertical, list, delete) and RLS isolation.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPGRateCards65(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-rc65")
	now := time.Now().UTC().Truncate(time.Second)

	hotel := &model.Partner{ID: newTestUUID(), TenantID: tenant, Name: "PG RC Hotel",
		Verticals: []string{"CHAUFFEUR"}, Status: "VETTED", QualityScore: 8,
		PayoutRail: "MANUAL", CommissionPct: 10, Kind: model.PartnerKindHotel, CreatedAt: now}
	if err := s.Partners().Create(ctx, hotel); err != nil {
		t.Fatal(err)
	}

	// Upsert twice — one row survives with the latest value.
	card := &model.RateCard{TenantID: tenant, PartnerID: hotel.ID,
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Kind: model.RateCardPctDiscount,
		Value: 10, CreatedAt: now, UpdatedAt: now}
	if err := s.RateCards().Upsert(ctx, card); err != nil {
		t.Fatal(err)
	}
	card.Value = 15
	card.UpdatedAt = now.Add(time.Minute)
	if err := s.RateCards().Upsert(ctx, card); err != nil {
		t.Fatal(err)
	}
	// A fixed card with currency round-trips too.
	fixed := &model.RateCard{TenantID: tenant, PartnerID: hotel.ID,
		Vertical: "CHAUFFEUR_HOURLY", Kind: model.RateCardFixedTotal,
		Value: 22000, Currency: "EUR", CreatedAt: now, UpdatedAt: now}
	if err := s.RateCards().Upsert(ctx, fixed); err != nil {
		t.Fatal(err)
	}

	cards, err := s.RateCards().ListForPartner(ctx, tenant, hotel.ID)
	if err != nil || len(cards) != 2 {
		t.Fatalf("list: %+v %v", cards, err)
	}
	if cards[0].Vertical != "CHAUFFEUR_HOURLY" || cards[1].Vertical != "CHAUFFEUR_POINT_TO_POINT" {
		t.Fatalf("list order: %+v", cards)
	}
	if cards[1].Value != 15 || cards[0].Currency != "EUR" {
		t.Fatalf("values: %+v", cards)
	}

	// RLS: another tenant sees nothing and cannot delete.
	other := pgTenant(t, s, "pg-rc65-b")
	if got, err := s.RateCards().ListForPartner(ctx, other, hotel.ID); err != nil || len(got) != 0 {
		t.Fatalf("cross-tenant list: %+v %v", got, err)
	}
	if err := s.RateCards().Delete(ctx, other, hotel.ID, "CHAUFFEUR_POINT_TO_POINT"); err != httpx.ErrNotFound {
		t.Fatalf("cross-tenant delete: %v", err)
	}

	// Delete: real then missing.
	if err := s.RateCards().Delete(ctx, tenant, hotel.ID, "CHAUFFEUR_POINT_TO_POINT"); err != nil {
		t.Fatal(err)
	}
	if err := s.RateCards().Delete(ctx, tenant, hotel.ID, "CHAUFFEUR_POINT_TO_POINT"); err != httpx.ErrNotFound {
		t.Fatalf("delete missing: %v", err)
	}
	if got, _ := s.RateCards().ListForPartner(ctx, tenant, hotel.ID); len(got) != 1 {
		t.Fatalf("after delete: %+v", got)
	}
}
