package domain

// Phase 25: client journey tracking (track tokens + TrackView) and
// lifecycle follow-ups (post-trip service message, consent-gated win-back).

import (
	"context"
	"errors"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// trackFixture builds client -> request -> booking -> assignment with a
// labeled vehicle and a named chauffeur; returns the assignment id.
func trackFixture(t *testing.T, s *Services, tenantID, clientID string) string {
	t.Helper()
	ctx := context.Background()
	sr, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
		TenantID: tenantID, ClientID: clientID, IdempotencyKey: "track-" + NewID()[:8],
		Items: []model.ServiceItem{{
			Vertical: "CHAUFFEUR_POINT_TO_POINT", PickupAddress: "Amstel Hotel",
			DropoffAddress: "Schiphol", PickupAt: s.Now().Add(2 * time.Hour).Unix(),
			Passengers: 2, VehicleClass: "sedan",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	booking := &model.Booking{ID: NewID(), TenantID: tenantID, RequestID: sr.ID,
		State: "CONFIRMED", CreatedAt: s.Now()}
	if err := s.Store.Bookings().Create(ctx, booking); err != nil {
		t.Fatal(err)
	}
	fleet := &model.FleetResource{ID: NewID(), TenantID: tenantID, Kind: "sedan",
		Label: "Mercedes S-Class", Active: true}
	if err := s.Store.Dispatch().AddFleetResource(ctx, fleet); err != nil {
		t.Fatal(err)
	}
	crew := &model.CrewMember{ID: NewID(), TenantID: tenantID, FullName: "Adebayo Okafor", Active: true}
	if err := s.Store.Dispatch().AddCrewMember(ctx, crew); err != nil {
		t.Fatal(err)
	}
	a, err := s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: booking.ID, PartnerID: "partner-x",
		FleetResourceID: fleet.ID, CrewMemberID: crew.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}

func TestClientTrackTokenRoundtrip(t *testing.T) {
	s := testServices(t, Deps{TripSecret: "test-secret"})
	tenantID := seedTenant(t, s)

	track := s.Trips.ClientTokenFor(tenantID, "asg-1")
	tid, aid, err := s.Trips.ParseClientToken(track)
	if err != nil || tid != tenantID || aid != "asg-1" {
		t.Fatalf("track token roundtrip: %q %q %v", tid, aid, err)
	}
	// A monitoring link must never drive the trip.
	if _, _, err := s.Trips.ParseToken(track); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("driver parser accepted a track token: %v", err)
	}
	// And a chauffeur token must never masquerade as monitoring.
	driver := s.Trips.TokenFor(tenantID, "asg-1")
	if _, _, err := s.Trips.ParseClientToken(driver); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("client parser accepted a driver token: %v", err)
	}
	// Tampering fails on both.
	if _, _, err := s.Trips.ParseClientToken(track[:len(track)-2] + "zz"); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("tampered track token accepted: %v", err)
	}
}

func TestTrackView(t *testing.T) {
	s := testServices(t, Deps{TripSecret: "test-secret"})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	rr, _ := s.Clients.Resolve(ctx, tenantID, "+31611113333", "", "VIP Client")
	assignmentID := trackFixture(t, s, tenantID, rr.Client.ID)

	token := s.Trips.ClientTokenFor(tenantID, assignmentID)
	view, err := s.Trips.Track(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if view.Pickup != "Amstel Hotel" || view.Dropoff != "Schiphol" {
		t.Fatalf("addresses missing: %+v", view)
	}
	if view.VehicleLabel != "Mercedes S-Class" {
		t.Fatalf("vehicle label: %q", view.VehicleLabel)
	}
	if view.DriverName != "Adebayo" {
		t.Fatalf("driver must be first-name only: %q", view.DriverName)
	}
	// No stamp yet — honest "position pending", never an invented one.
	if view.Position != nil {
		t.Fatalf("position before first stamp: %+v", view.Position)
	}

	// The chauffeur stamps via THEIR token; the client view picks it up.
	driverToken := s.Trips.TokenFor(tenantID, assignmentID)
	if err := s.Trips.Stamp(ctx, driverToken, "en_route", 52.37, 4.90, 8); err != nil {
		t.Fatal(err)
	}
	view, err = s.Trips.Track(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if view.Position == nil || view.Position.Kind != "EN_ROUTE" ||
		view.Position.Lat != 52.37 || view.Position.Lng != 4.90 {
		t.Fatalf("position after stamp: %+v", view.Position)
	}

	// The track token cannot advance or stamp.
	if err := s.Trips.Advance(ctx, token, "EN_ROUTE_TO_PICKUP"); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("track token advanced status: %v", err)
	}
	if err := s.Trips.Stamp(ctx, token, "ping", 1, 1, 1); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("track token stamped: %v", err)
	}
	if _, err := s.Trips.Track(ctx, "garbage-token"); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("garbage token tracked: %v", err)
	}
	// Unknown assignment behind a validly-signed token is a clean 404.
	ghost := s.Trips.ClientTokenFor(tenantID, "asg-nope")
	if _, err := s.Trips.Track(ctx, ghost); !errors.Is(err, httpx.ErrNotFound) {
		t.Fatalf("ghost assignment: %v", err)
	}
}

func TestPostTripFollowUp(t *testing.T) {
	calls := 0
	s := testServices(t, Deps{TripSecret: "test-secret", WhatsAppURL: waCountingStub(t, &calls).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	// Service lane needs no marketing consent.
	clientID := outreachClient(t, s, tenantID, "+31611114444", false)

	// A trip that ended 3 days ago (window end is the completion proxy).
	pickup := s.Now().Add(-72 * time.Hour).Unix()
	bookingID := bookAt(t, s, tenantID, clientID, "post-trip-1", pickup, 2)
	a, err := s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: bookingID, PartnerID: "partner-x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.EndAt.IsZero() {
		t.Fatal("assignment window not resolved")
	}
	// Drive the legal chain to completion via the chauffeur token.
	token := s.Trips.TokenFor(tenantID, a.ID)
	for _, st := range []string{"EN_ROUTE_TO_PICKUP", "ARRIVED", "PASSENGER_ON_BOARD", "IN_TRIP", "COMPLETED_TRIP"} {
		if err := s.Trips.Advance(ctx, token, st); err != nil {
			t.Fatalf("advance %s: %v", st, err)
		}
	}

	res, err := s.FollowUps.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if res.PostTripSent != 1 {
		t.Fatalf("post-trip sends: %+v", res)
	}
	if calls != 1 {
		t.Fatalf("adapter calls: %d", calls)
	}
	// Hourly re-run: idempotency key replays, nothing re-sends, counters
	// reflect this run only.
	res, err = s.FollowUps.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if res.PostTripSent != 0 || calls != 1 {
		t.Fatalf("re-run re-sent: %+v calls=%d", res, calls)
	}
}

func TestWinBackConsentGate(t *testing.T) {
	calls := 0
	s := testServices(t, Deps{TripSecret: "test-secret", WhatsAppURL: waCountingStub(t, &calls).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	consented := outreachClient(t, s, tenantID, "+31611115555", true)
	silent := outreachClient(t, s, tenantID, "+31611116666", false)
	// A third client who never booked is scanned but never contacted.
	outreachClient(t, s, tenantID, "+31611117777", true)

	old := s.Now().Add(-100 * 24 * time.Hour)
	for i, cid := range []string{consented, silent} {
		sr, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
			TenantID: tenantID, ClientID: cid, IdempotencyKey: "wb-" + string(rune('a'+i)),
			Items: []model.ServiceItem{{
				Vertical: "CHAUFFEUR_POINT_TO_POINT", PickupAddress: "A",
				PickupAt: old.Unix(), Passengers: 1,
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Store.Bookings().Create(ctx, &model.Booking{
			ID: NewID(), TenantID: tenantID, RequestID: sr.ID,
			State: "CONFIRMED", CreatedAt: old,
		}); err != nil {
			t.Fatal(err)
		}
	}

	res, err := s.FollowUps.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if res.ClientsScanned != 3 {
		t.Fatalf("scanned: %+v", res)
	}
	if res.WinBackSent != 1 || res.WinBackHeld != 1 {
		t.Fatalf("consent gate not honored: %+v", res)
	}
	if calls != 1 {
		t.Fatalf("adapter called for a non-consented client: %d", calls)
	}
	// Same ISO week: idempotency keys replay — no re-send, no double count.
	res, err = s.FollowUps.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if res.WinBackSent != 0 || res.WinBackHeld != 0 || calls != 1 {
		t.Fatalf("weekly idempotency broken: %+v calls=%d", res, calls)
	}
}

func TestLifecycleDisabled(t *testing.T) {
	calls := 0
	s := testServices(t, Deps{TripSecret: "test-secret", WhatsAppURL: waCountingStub(t, &calls).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+31611118888", false)
	bookAt(t, s, tenantID, clientID, "disabled-1", s.Now().Add(-72*time.Hour).Unix(), 2)

	tenant, err := s.Tenants.Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	tenant.LifecycleConfig = model.LifecycleConfig{Disabled: true}
	if _, err := s.Tenants.UpdateConfig(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	res, err := s.FollowUps.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if res.PostTripSent != 0 || res.WinBackSent != 0 || res.WinBackHeld != 0 || calls != 0 {
		t.Fatalf("disabled tenant still engaged: %+v calls=%d", res, calls)
	}
}
