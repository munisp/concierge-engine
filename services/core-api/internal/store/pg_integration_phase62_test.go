//go:build pg

package store

// Postgres integration tests — Phase 62: crew language/style columns
// round-trip, assignment match columns round-trip, chauffeur links
// upsert/clear/list with RLS isolation, and the prior-trips join count.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGChauffeurMatching62(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-match62")

	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Match PG",
		Phone: "+971507200001", KYCStatus: "VERIFIED"}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatal(err)
	}

	// Crew with declared capabilities.
	crew := &model.CrewMember{ID: newTestUUID(), TenantID: tenant, FullName: "Amadou Diallo",
		Certificates: []string{"defensive-driving"}, Languages: []string{"fr", "en"},
		Styles: []string{"quiet"}, QualityScore: 9, Active: true}
	if err := s.Dispatch().AddCrewMember(ctx, crew); err != nil {
		t.Fatalf("add crew: %v", err)
	}
	list, err := s.Dispatch().ListCrew(ctx, tenant)
	if err != nil || len(list) != 1 {
		t.Fatalf("list crew: %+v %v", list, err)
	}
	if len(list[0].Languages) != 2 || list[0].Languages[0] != "fr" || list[0].Styles[0] != "quiet" {
		t.Fatalf("crew round-trip: %+v", list[0])
	}

	// Booking chain for assignments (quote_id NOT NULL).
	now := time.Now().UTC().Truncate(time.Second)
	mkBooking := func(idem string) string {
		req := &model.ServiceRequest{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
			State: model.StateNew, Version: 1, IdempotencyKey: idem,
			CreatedAt: now, UpdatedAt: now,
			Items: []model.ServiceItem{{ID: newTestUUID(), TenantID: tenant,
				Vertical: "chauffeur", PickupAddress: "A", PickupAt: now.Unix(), Passengers: 1}}}
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
		return b.ID
	}

	// Assignment with recorded match decision.
	b1 := mkBooking("req-pg62-a")
	a := &model.Assignment{ID: newTestUUID(), TenantID: tenant, BookingID: b1,
		CrewMemberID: crew.ID, Status: "ASSIGNED",
		MatchScore: 59, MatchReasons: []string{"speaks fr", "quiet-style match", "quality 9.0"},
		CreatedAt:  now}
	if err := s.Dispatch().CreateAssignment(ctx, a); err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	as, err := s.Dispatch().ListAssignments(ctx, tenant)
	if err != nil || len(as) != 1 {
		t.Fatalf("list assignments: %+v %v", as, err)
	}
	if as[0].MatchScore != 59 || len(as[0].MatchReasons) != 3 || as[0].MatchReasons[0] != "speaks fr" {
		t.Fatalf("match round-trip: %+v", as[0])
	}

	// A completed trip with this crew, then the prior-trips count.
	if err := s.Dispatch().UpdateAssignmentStatus(ctx, tenant, a.ID, "COMPLETED_TRIP"); err != nil {
		t.Fatal(err)
	}
	b2 := mkBooking("req-pg62-b")
	other := &model.Assignment{ID: newTestUUID(), TenantID: tenant, BookingID: b2,
		Status: "ASSIGNED", CreatedAt: now} // no crew — must not count
	if err := s.Dispatch().CreateAssignment(ctx, other); err != nil {
		t.Fatal(err)
	}
	n, err := s.Dispatch().CountClientTrips(ctx, tenant, cl.ID, crew.ID)
	if err != nil || n != 1 {
		t.Fatalf("prior trips: %d %v", n, err)
	}

	// Links: set, upsert, list, clear.
	l := &model.ChauffeurLink{TenantID: tenant, ClientID: cl.ID, CrewMemberID: crew.ID,
		Kind: model.LinkFavorite, CreatedAt: now}
	if err := s.ChauffeurLinks().Set(ctx, l); err != nil {
		t.Fatalf("set link: %v", err)
	}
	l.Kind = model.LinkBlocked
	if err := s.ChauffeurLinks().Set(ctx, l); err != nil {
		t.Fatalf("upsert link: %v", err)
	}
	links, err := s.ChauffeurLinks().ListForClient(ctx, tenant, cl.ID)
	if err != nil || len(links) != 1 || links[0].Kind != model.LinkBlocked {
		t.Fatalf("links: %+v %v", links, err)
	}
	if err := s.ChauffeurLinks().Clear(ctx, tenant, cl.ID, crew.ID); err != nil {
		t.Fatal(err)
	}
	links, err = s.ChauffeurLinks().ListForClient(ctx, tenant, cl.ID)
	if err != nil || len(links) != 0 {
		t.Fatalf("after clear: %+v %v", links, err)
	}

	// RLS: another tenant's view is empty even for the same ids.
	otherTenant := pgTenant(t, s, "pg-match62b")
	if err := s.ChauffeurLinks().Set(ctx, l); err != nil {
		t.Fatal(err)
	}
	links, err = s.ChauffeurLinks().ListForClient(ctx, otherTenant, cl.ID)
	if err != nil || len(links) != 0 {
		t.Fatalf("cross-tenant leak: %+v %v", links, err)
	}
	n, err = s.Dispatch().CountClientTrips(ctx, otherTenant, cl.ID, crew.ID)
	if err != nil || n != 0 {
		t.Fatalf("cross-tenant count: %d %v", n, err)
	}
}
