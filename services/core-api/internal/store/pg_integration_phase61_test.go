//go:build pg

package store

// Postgres integration tests — Phase 61: itinerary legs round-trip with
// nullable booking/linked FKs, the timeline ordering, the ForBooking join,
// the flight-watch scan window, RLS isolation, and update semantics.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPGItineraries61(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-journey61")

	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Journey PG",
		Phone: "+971507100001", KYCStatus: "VERIFIED"}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	dep := now.Add(20 * time.Hour)
	arr := dep.Add(6 * time.Hour)

	// Flight leg — no booking/linked FKs (both must persist as NULL).
	fl := &model.ItineraryLeg{
		ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		Kind: model.LegFlight, Title: "BA75 LHR → LOS", FlightNumber: "BA75",
		Origin: "LHR", Destination: "LOS",
		SchedStart: dep, SchedEnd: arr,
		Status: model.LegScheduled, Source: "scheduled",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Itineraries().Create(ctx, fl); err != nil {
		t.Fatalf("create flight: %v", err)
	}
	got, err := s.Itineraries().Get(ctx, tenant, fl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BookingID != "" || got.LinkedLegID != "" || got.Title != fl.Title ||
		!got.SchedEnd.Equal(arr) || !got.ActualStart.IsZero() {
		t.Fatalf("flight round-trip: %+v", got)
	}

	// Ground leg chained to the flight — needs a real booking row for the FK.
	req := &model.ServiceRequest{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		State: model.StateNew, Version: 1, IdempotencyKey: "req-pg61-" + cl.ID[:8],
		CreatedAt: now, UpdatedAt: now,
		Items: []model.ServiceItem{{ID: newTestUUID(), TenantID: tenant,
			Vertical: "chauffeur", PickupAddress: "LOS Arrivals", PickupAt: arr.Unix(),
			Passengers: 1}}}
	if err := s.Requests().Create(ctx, req); err != nil {
		t.Fatal(err)
	}
	q := &model.Quote{ID: newTestUUID(), TenantID: tenant, RequestID: req.ID,
		Version: 1, Origin: "INSTANT", TotalMinor: 18000, Currency: "EUR",
		ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := s.Quotes().Create(ctx, q); err != nil {
		t.Fatal(err)
	}
	b := &model.Booking{ID: newTestUUID(), TenantID: tenant, RequestID: req.ID,
		QuoteID: q.ID, State: "CONFIRMED", CreatedAt: now}
	if err := s.Bookings().Create(ctx, b); err != nil {
		t.Fatal(err)
	}

	gl := &model.ItineraryLeg{
		ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		BookingID: b.ID, LinkedLegID: fl.ID,
		Kind: model.LegGround, Title: "LOS arrivals transfer",
		Origin: "LOS", Destination: "Eko Hotel",
		SchedStart: arr.Add(45 * time.Minute),
		Status:     model.LegScheduled, Source: "manual",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Itineraries().Create(ctx, gl); err != nil {
		t.Fatalf("create ground: %v", err)
	}

	// Timeline: soonest first.
	legs, err := s.Itineraries().ListForClient(ctx, tenant, cl.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(legs) != 2 || legs[0].ID != fl.ID || legs[1].ID != gl.ID {
		t.Fatalf("timeline: %+v", legs)
	}
	if legs[1].BookingID != b.ID || legs[1].LinkedLegID != fl.ID {
		t.Fatalf("FKs: %+v", legs[1])
	}

	// ForBooking join.
	byBooking, err := s.Itineraries().ForBooking(ctx, tenant, b.ID)
	if err != nil || len(byBooking) != 1 || byBooking[0].ID != gl.ID {
		t.Fatalf("for booking: %+v %v", byBooking, err)
	}

	// Watch scan: the flight is due in the 24h window; after marking it
	// ARRIVED it leaves the scan set.
	due, err := s.Itineraries().FlightLegsDue(ctx, tenant, now.Add(-time.Hour), now.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != fl.ID {
		t.Fatalf("watch scan: %+v", due)
	}

	// Delay cascade write-back: 40m delay → status + delay + ETA persist.
	fl.Status = model.LegDelayed
	fl.DelayMinutes = 40
	fl.ActualStart = arr.Add(40 * time.Minute)
	fl.Source = "live"
	fl.UpdatedAt = now.Add(time.Minute)
	if err := s.Itineraries().Update(ctx, fl); err != nil {
		t.Fatalf("update flight: %v", err)
	}
	gl.ActualStart = fl.ActualStart.Add(45 * time.Minute)
	gl.UpdatedAt = now.Add(time.Minute)
	if err := s.Itineraries().Update(ctx, gl); err != nil {
		t.Fatalf("update ground: %v", err)
	}
	got, err = s.Itineraries().Get(ctx, tenant, fl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DelayMinutes != 40 || got.Source != "live" || !got.ActualStart.Equal(fl.ActualStart) {
		t.Fatalf("cascade round-trip: %+v", got)
	}
	gotG, err := s.Itineraries().Get(ctx, tenant, gl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !gotG.ActualStart.Equal(gl.ActualStart) {
		t.Fatalf("ground cascade round-trip: %+v", gotG)
	}

	// Terminal status leaves the watch set.
	fl.Status = model.LegArrived
	if err := s.Itineraries().Update(ctx, fl); err != nil {
		t.Fatal(err)
	}
	due, err = s.Itineraries().FlightLegsDue(ctx, tenant, now.Add(-time.Hour), now.Add(24*time.Hour))
	if err != nil || len(due) != 0 {
		t.Fatalf("watch after arrival: %+v %v", due, err)
	}

	// RLS: another tenant sees nothing of this journey.
	other := pgTenant(t, s, "pg-journey61b")
	legs, err = s.Itineraries().ListForClient(ctx, other, cl.ID, 50)
	if err != nil || len(legs) != 0 {
		t.Fatalf("cross-tenant leak: %+v %v", legs, err)
	}
	if _, err := s.Itineraries().Get(ctx, other, fl.ID); err != httpx.ErrNotFound {
		t.Fatalf("cross-tenant get: %v", err)
	}
}
