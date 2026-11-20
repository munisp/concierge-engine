//go:build pg

package store

// Postgres integration tests — Phase 64: booking SetSource (reattribute
// and clear), request Update persisting source columns, and RLS isolation
// of the reattribution write.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPGChannelReattribution64(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-chan64")
	now := time.Now().UTC().Truncate(time.Second)

	hotel := &model.Partner{ID: newTestUUID(), TenantID: tenant, Name: "PG Hotel 64",
		Verticals: []string{"CHAUFFEUR"}, Status: "VETTED", QualityScore: 8,
		PayoutRail: "MANUAL", CommissionPct: 10, Kind: model.PartnerKindHotel, CreatedAt: now}
	if err := s.Partners().Create(ctx, hotel); err != nil {
		t.Fatal(err)
	}
	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "PG Guest 64",
		Phone: "+316607200064", KYCStatus: "VERIFIED"}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatal(err)
	}

	// Direct request + confirmed booking.
	req := &model.ServiceRequest{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		State: model.StateConfirmed, Version: 2, IdempotencyKey: "req-pg64-a",
		CreatedAt: now, UpdatedAt: now,
		Items: []model.ServiceItem{{ID: newTestUUID(), TenantID: tenant,
			Vertical: "chauffeur", PickupAddress: "Hotel", PickupAt: now.Unix(), Passengers: 1}}}
	if err := s.Requests().Create(ctx, req); err != nil {
		t.Fatal(err)
	}
	q := &model.Quote{ID: newTestUUID(), TenantID: tenant, RequestID: req.ID,
		Version: 1, Origin: "INSTANT", TotalMinor: 9000, Currency: "EUR",
		ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := s.Quotes().Create(ctx, q); err != nil {
		t.Fatal(err)
	}
	b := &model.Booking{ID: newTestUUID(), TenantID: tenant, RequestID: req.ID,
		QuoteID: q.ID, State: "CONFIRMED", CreatedAt: now}
	if err := s.Bookings().Create(ctx, b); err != nil {
		t.Fatal(err)
	}

	// Request Update must persist the new source columns (Phase 63 patched
	// the UPDATE to carry them).
	req.SourceKind = model.SourceHotel
	req.SourcePartnerID = hotel.ID
	if err := s.Requests().Update(ctx, req); err != nil {
		t.Fatalf("request update with source: %v", err)
	}
	gotReq, err := s.Requests().Get(ctx, tenant, req.ID)
	if err != nil || gotReq.SourceKind != model.SourceHotel || gotReq.SourcePartnerID != hotel.ID {
		t.Fatalf("request source persisted: %+v %v", gotReq, err)
	}

	// Booking SetSource: attribute, then clear back to direct.
	if err := s.Bookings().SetSource(ctx, tenant, b.ID, model.SourceHotel, hotel.ID); err != nil {
		t.Fatalf("set source: %v", err)
	}
	gotB, _ := s.Bookings().Get(ctx, tenant, b.ID)
	if gotB.SourceKind != model.SourceHotel || gotB.SourcePartnerID != hotel.ID {
		t.Fatalf("booking source set: %+v", gotB)
	}
	if err := s.Bookings().SetSource(ctx, tenant, b.ID, model.SourceDirect, ""); err != nil {
		t.Fatalf("clear source: %v", err)
	}
	gotB2, _ := s.Bookings().Get(ctx, tenant, b.ID)
	if gotB2.SourceKind != model.SourceDirect || gotB2.SourcePartnerID != "" {
		t.Fatalf("booking source cleared: %+v", gotB2)
	}

	// RLS: another tenant cannot reattribute this tenant's booking.
	other := pgTenant(t, s, "pg-chan64-b")
	if err := s.Bookings().SetSource(ctx, other, b.ID, model.SourceHotel, hotel.ID); err != httpx.ErrNotFound {
		t.Fatalf("cross-tenant SetSource: %v", err)
	}
	gotB3, _ := s.Bookings().Get(ctx, tenant, b.ID)
	if gotB3.SourceKind != model.SourceDirect {
		t.Fatalf("cross-tenant write leaked: %+v", gotB3)
	}
}
