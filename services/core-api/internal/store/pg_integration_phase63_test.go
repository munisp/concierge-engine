//go:build pg

package store

// Postgres integration tests — Phase 63: partner kind column round-trip,
// request/booking source columns round-trip (including defaults), the
// ForSource partner feed with ordering/limit, and cross-tenant RLS
// isolation of sourced demand.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGDemandChannels63(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-channels63")
	now := time.Now().UTC().Truncate(time.Second)

	// Partner with a channel kind.
	hotel := &model.Partner{ID: newTestUUID(), TenantID: tenant, Name: "PG Hotel Desk",
		Verticals: []string{"CHAUFFEUR"}, Status: "VETTED", QualityScore: 8,
		PayoutRail: "MANUAL", CommissionPct: 10, Kind: model.PartnerKindHotel, CreatedAt: now}
	if err := s.Partners().Create(ctx, hotel); err != nil {
		t.Fatalf("create partner: %v", err)
	}
	got, err := s.Partners().Get(ctx, tenant, hotel.ID)
	if err != nil || got.Kind != model.PartnerKindHotel {
		t.Fatalf("partner kind round-trip: %+v %v", got, err)
	}

	// Legacy partner with empty kind lands on the SQL default.
	legacy := &model.Partner{ID: newTestUUID(), TenantID: tenant, Name: "PG Legacy Operator",
		Verticals: []string{"TOURS"}, Status: "PENDING", QualityScore: 5,
		PayoutRail: "MANUAL", CreatedAt: now}
	if err := s.Partners().Create(ctx, legacy); err != nil {
		t.Fatalf("create legacy partner: %v", err)
	}
	gotLegacy, err := s.Partners().Get(ctx, tenant, legacy.ID)
	if err != nil || gotLegacy.Kind != model.PartnerKindTourOperator {
		t.Fatalf("legacy kind default: %+v %v", gotLegacy, err)
	}

	// Kind survives Update (the console reclassify path).
	got.Kind = model.PartnerKindTravelAgency
	if err := s.Partners().Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.Partners().Get(ctx, tenant, hotel.ID)
	if got2.Kind != model.PartnerKindTravelAgency {
		t.Fatalf("kind update: %+v", got2)
	}
	got2.Kind = model.PartnerKindHotel
	if err := s.Partners().Update(ctx, got2); err != nil {
		t.Fatal(err)
	}

	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "PG Guest",
		Phone: "+316607200001", KYCStatus: "VERIFIED"}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatal(err)
	}

	// Sourced request: source columns round-trip.
	req := &model.ServiceRequest{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		State: model.StateCapturing, Version: 1, IdempotencyKey: "pg63-src-1",
		SourceKind: model.SourceHotel, SourcePartnerID: hotel.ID,
		CreatedAt: now, UpdatedAt: now,
		Items: []model.ServiceItem{{ID: newTestUUID(), TenantID: tenant,
			Vertical: "chauffeur", PickupAddress: "Hotel", PickupAt: now.Unix(), Passengers: 1}}}
	if err := s.Requests().Create(ctx, req); err != nil {
		t.Fatalf("create sourced request: %v", err)
	}
	gotReq, err := s.Requests().Get(ctx, tenant, req.ID)
	if err != nil || gotReq.SourceKind != model.SourceHotel || gotReq.SourcePartnerID != hotel.ID {
		t.Fatalf("request source round-trip: %+v %v", gotReq, err)
	}

	// Direct request: empty source lands on the SQL default.
	dreq := &model.ServiceRequest{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		State: model.StateCapturing, Version: 1, IdempotencyKey: "req-pg63-b",
		CreatedAt: now, UpdatedAt: now,
		Items: []model.ServiceItem{{ID: newTestUUID(), TenantID: tenant,
			Vertical: "chauffeur", PickupAddress: "Home", PickupAt: now.Unix(), Passengers: 1}}}
	if err := s.Requests().Create(ctx, dreq); err != nil {
		t.Fatal(err)
	}
	gotD, _ := s.Requests().Get(ctx, tenant, dreq.ID)
	if gotD.SourceKind != model.SourceDirect || gotD.SourcePartnerID != "" {
		t.Fatalf("direct request defaults: %+v", gotD)
	}

	// A second sourced request, newer — ForSource must order newest first.
	req2 := &model.ServiceRequest{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		State: model.StateCapturing, Version: 1, IdempotencyKey: "pg63-src-2",
		SourceKind: model.SourceHotel, SourcePartnerID: hotel.ID,
		CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute),
		Items: []model.ServiceItem{{ID: newTestUUID(), TenantID: tenant,
			Vertical: "chauffeur", PickupAddress: "Hotel", PickupAt: now.Unix(), Passengers: 1}}}
	if err := s.Requests().Create(ctx, req2); err != nil {
		t.Fatal(err)
	}
	feed, err := s.Requests().ForSource(ctx, tenant, hotel.ID, 10)
	if err != nil || len(feed) != 2 {
		t.Fatalf("for source: %+v %v", feed, err)
	}
	if feed[0].ID != req2.ID || feed[1].ID != req.ID {
		t.Fatalf("for source order: %+v", feed)
	}
	// Limit and the direct request excluded.
	feed1, _ := s.Requests().ForSource(ctx, tenant, hotel.ID, 1)
	if len(feed1) != 1 || feed1[0].ID != req2.ID {
		t.Fatalf("for source limit: %+v", feed1)
	}

	// Booking source columns round-trip (quote first — quote_id NOT NULL).
	q := &model.Quote{ID: newTestUUID(), TenantID: tenant, RequestID: req.ID,
		Version: 1, Origin: "INSTANT", TotalMinor: 9000, Currency: "EUR",
		ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := s.Quotes().Create(ctx, q); err != nil {
		t.Fatal(err)
	}
	b := &model.Booking{ID: newTestUUID(), TenantID: tenant, RequestID: req.ID,
		QuoteID: q.ID, State: "CONFIRMED",
		SourceKind: model.SourceHotel, SourcePartnerID: hotel.ID, CreatedAt: now}
	if err := s.Bookings().Create(ctx, b); err != nil {
		t.Fatalf("create sourced booking: %v", err)
	}
	gotB, err := s.Bookings().Get(ctx, tenant, b.ID)
	if err != nil || gotB.SourceKind != model.SourceHotel || gotB.SourcePartnerID != hotel.ID {
		t.Fatalf("booking source round-trip: %+v %v", gotB, err)
	}
	bl, err := s.Bookings().List(ctx, tenant)
	if err != nil || len(bl) != 1 || bl[0].SourceKind != model.SourceHotel {
		t.Fatalf("booking list source: %+v %v", bl, err)
	}

	// RLS: a second tenant never sees the first tenant's sourced demand.
	other := pgTenant(t, s, "pg-channels63-b")
	crossFeed, err := s.Requests().ForSource(ctx, other, hotel.ID, 10)
	if err != nil || len(crossFeed) != 0 {
		t.Fatalf("cross-tenant feed: %+v %v", crossFeed, err)
	}
	if _, err := s.Partners().Get(ctx, other, hotel.ID); err == nil {
		t.Fatal("cross-tenant partner read must fail")
	}
}
