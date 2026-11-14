package domain

// Phase 22: conversation threading (get-or-create per client+channel),
// scheduling v1 (assignment windows, conflict gating with turnaround
// buffers, availability), unified client threads.

import (
	"context"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// bookAt creates a confirmed booking whose request item picks up at
// pickupAt (epoch) for hours hours; returns the booking id.
func bookAt(t *testing.T, s *Services, tenantID, clientID, idem string, pickupAt int64, hours int) string {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Catalog.Add(ctx, &model.CatalogService{
		TenantID: tenantID, Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Sedan|" + idem,
		BaseMinor: 9000, Currency: "EUR", PerKMMinor: 250, AutoQuotable: true, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	sr, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
		TenantID: tenantID, ClientID: clientID, IdempotencyKey: idem,
		Items: []model.ServiceItem{{
			Vertical: "CHAUFFEUR_POINT_TO_POINT", PickupAddress: "A", DropoffAddress: "B",
			PickupAt: pickupAt, Passengers: 1, Hours: hours,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	q, instant, err := s.Booking.QuoteRequest(ctx, tenantID, sr.ID)
	if err != nil || !instant {
		t.Fatalf("quote: %v instant=%v", err, instant)
	}
	b, err := s.Booking.Confirm(ctx, tenantID, q.ID)
	if err != nil {
		t.Fatal(err)
	}
	return b.ID
}

func TestConversationThreading(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000200", false)

	// Every inbound message used to open a NEW conversation. Now: one
	// active thread per (client, channel).
	c1, err := s.Conversations.Open(ctx, tenantID, clientID, "WHATSAPP", "wamid.m1")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := s.Conversations.Open(ctx, tenantID, clientID, "WHATSAPP", "wamid.m2")
	if err != nil {
		t.Fatal(err)
	}
	if c1.ID != c2.ID {
		t.Fatalf("same client+channel must reuse the active thread: %s vs %s", c1.ID, c2.ID)
	}
	if got := outboxHas(t, s, "conversation.started"); got != 1 {
		t.Fatalf("conversation.started fires once per thread, got %d", got)
	}

	// A different channel gets its own thread (web vs WhatsApp).
	c3, err := s.Conversations.Open(ctx, tenantID, clientID, "WEB", "")
	if err != nil {
		t.Fatal(err)
	}
	if c3.ID == c1.ID {
		t.Fatal("channels thread separately")
	}

	// Closing the thread lets the next message open a fresh one.
	if err := s.Conversations.SetState(ctx, tenantID, c1.ID, "CLOSED"); err != nil {
		t.Fatal(err)
	}
	c4, err := s.Conversations.Open(ctx, tenantID, clientID, "WHATSAPP", "wamid.m3")
	if err != nil {
		t.Fatal(err)
	}
	if c4.ID == c1.ID {
		t.Fatal("a CLOSED thread must not absorb new messages")
	}
}

func TestClientThreads(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000201", false)

	if _, err := s.Conversations.Open(ctx, tenantID, clientID, "WHATSAPP", "w1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Conversations.Open(ctx, tenantID, clientID, "WEB", ""); err != nil {
		t.Fatal(err)
	}
	threads, err := s.Conversations.Threads(ctx, tenantID, clientID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 2 {
		t.Fatalf("one thread per channel: %+v", threads)
	}
	channels := map[string]bool{}
	for _, th := range threads {
		channels[th.Channel] = true
	}
	if !channels["WHATSAPP"] || !channels["WEB"] {
		t.Fatalf("threads must span channels: %v", channels)
	}
	if _, err := s.Conversations.Threads(ctx, tenantID, "", 10); err == nil {
		t.Fatal("empty client id must be rejected")
	}
}

func TestAssignmentWindowResolution(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000202", false)
	pickup := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)

	bookingID := bookAt(t, s, tenantID, clientID, "win-1", pickup.Unix(), 3)
	if _, err := s.Dispatch.AddFleetResource(ctx, &model.FleetResource{
		ID: NewID(), TenantID: tenantID, Kind: "sedan", Label: "S-01", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	fleet, _ := s.Dispatch.ListFleet(ctx, tenantID)

	a, err := s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: bookingID, FleetResourceID: fleet[0].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !a.StartAt.Equal(pickup) {
		t.Fatalf("window start must derive from pickup_at: %s vs %s", a.StartAt, pickup)
	}
	if !a.EndAt.Equal(pickup.Add(3 * time.Hour)) {
		t.Fatalf("window end must be pickup + item hours: %s", a.EndAt)
	}

	// No hours on the item -> tenant default duration (120m platform floor).
	bookingID2 := bookAt(t, s, tenantID, clientID, "win-2", pickup.Add(24*time.Hour).Unix(), 0)
	a2, err := s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: bookingID2, FleetResourceID: fleet[0].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if a2.EndAt.Sub(a2.StartAt) != 120*time.Minute {
		t.Fatalf("default duration must be 120m: %s", a2.EndAt.Sub(a2.StartAt))
	}
}

func TestAssignmentConflictGate(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000203", false)
	base := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)

	res := &model.FleetResource{ID: NewID(), TenantID: tenantID, Kind: "sedan", Label: "S-77", Active: true}
	if _, err := s.Dispatch.AddFleetResource(ctx, res); err != nil {
		t.Fatal(err)
	}

	// Trip A: base -> base+2h. Turnaround buffer (30m default) pads both sides.
	b1 := bookAt(t, s, tenantID, clientID, "cf-1", base.Unix(), 2)
	a1, err := s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: b1, FleetResourceID: res.ID})
	if err != nil {
		t.Fatal(err)
	}

	// Overlap: pickup 1h into trip A -> refused, conflict named.
	b2 := bookAt(t, s, tenantID, clientID, "cf-2", base.Add(time.Hour).Unix(), 2)
	_, err = s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: b2, FleetResourceID: res.ID})
	if err == nil || !strings.Contains(err.Error(), a1.ID) {
		t.Fatalf("overlap must 409 and name the conflict: %v", err)
	}

	// Inside the buffer: pickup 15m after trip A ends -> still refused.
	b3 := bookAt(t, s, tenantID, clientID, "cf-3", base.Add(2*time.Hour+15*time.Minute).Unix(), 2)
	if _, err = s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: b3, FleetResourceID: res.ID}); err == nil {
		t.Fatal("pickup inside the turnaround buffer must be refused")
	}

	// Outside the buffer: pickup 45m after trip A ends -> allowed.
	b4 := bookAt(t, s, tenantID, clientID, "cf-4", base.Add(2*time.Hour+45*time.Minute).Unix(), 1)
	a4, err := s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: b4, FleetResourceID: res.ID})
	if err != nil {
		t.Fatalf("gap beyond the buffer must be assignable: %v", err)
	}

	// A different resource is unaffected.
	res2 := &model.FleetResource{ID: NewID(), TenantID: tenantID, Kind: "sedan", Label: "S-78", Active: true}
	if _, err := s.Dispatch.AddFleetResource(ctx, res2); err != nil {
		t.Fatal(err)
	}
	b5 := bookAt(t, s, tenantID, clientID, "cf-5", base.Add(time.Hour).Unix(), 2)
	if _, err = s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: b5, FleetResourceID: res2.ID}); err != nil {
		t.Fatalf("other resource unaffected: %v", err)
	}

	// Terminal states free the resource: complete trip A, then its slot opens.
	for _, st := range []string{"EN_ROUTE_TO_PICKUP", "ARRIVED", "PASSENGER_ON_BOARD", "IN_TRIP", "COMPLETED_TRIP"} {
		if err := s.Dispatch.UpdateStatus(ctx, tenantID, a1.ID, st); err != nil {
			t.Fatal(err)
		}
	}
	b6 := bookAt(t, s, tenantID, clientID, "cf-6", base.Add(30*time.Minute).Unix(), 1)
	if _, err = s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: b6, FleetResourceID: res.ID}); err != nil {
		t.Fatalf("completed trips must free the resource: %v", err)
	}

	// Crew double-booking is gated the same way.
	crew := &model.CrewMember{ID: NewID(), TenantID: tenantID, FullName: "Mo", Active: true}
	if _, err := s.Dispatch.AddCrewMember(ctx, crew); err != nil {
		t.Fatal(err)
	}
	b7 := bookAt(t, s, tenantID, clientID, "cf-7", base.Add(6*time.Hour).Unix(), 2)
	if _, err = s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: b7, CrewMemberID: crew.ID, PartnerID: "p1"}); err != nil {
		t.Fatal(err)
	}
	b8 := bookAt(t, s, tenantID, clientID, "cf-8", base.Add(7*time.Hour).Unix(), 2)
	if _, err = s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: b8, CrewMemberID: crew.ID, PartnerID: "p1"}); err == nil {
		t.Fatal("crew double-booking must be refused")
	}

	// Partner-routed with no own resource/crew: not gated (partner owns their fleet).
	b9 := bookAt(t, s, tenantID, clientID, "cf-9", base.Add(6*time.Hour+30*time.Minute).Unix(), 2)
	if _, err = s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: b9, PartnerID: "partner-x"}); err != nil {
		t.Fatalf("partner-routed assignments are not gated: %v", err)
	}
	_ = a4
}

func TestAvailability(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000204", false)
	base := time.Now().UTC().Add(96 * time.Hour).Truncate(time.Second)

	free := &model.FleetResource{ID: NewID(), TenantID: tenantID, Kind: "suv", Label: "X-01", Active: true}
	busy := &model.FleetResource{ID: NewID(), TenantID: tenantID, Kind: "sedan", Label: "S-01", Active: true}
	inactive := &model.FleetResource{ID: NewID(), TenantID: tenantID, Kind: "van", Label: "V-01", Active: false}
	for _, f := range []*model.FleetResource{free, busy} {
		if _, err := s.Dispatch.AddFleetResource(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	// Deactivation is a store-level flag (creation always activates); the
	// availability query must filter it.
	if err := s.Store.Dispatch().AddFleetResource(ctx, inactive); err != nil {
		t.Fatal(err)
	}
	b1 := bookAt(t, s, tenantID, clientID, "av-1", base.Unix(), 2)
	if _, err := s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: b1, FleetResourceID: busy.ID}); err != nil {
		t.Fatal(err)
	}

	avail, err := s.Dispatch.Availability(ctx, tenantID, base.Add(-time.Hour), base.Add(4*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]ResourceAvailability{}
	for _, ra := range avail {
		byID[ra.FleetResourceID] = ra
	}
	if byID[free.ID].Free != true || len(byID[free.ID].Busy) != 0 {
		t.Fatalf("free resource must be free: %+v", byID[free.ID])
	}
	if byID[busy.ID].Free != false || len(byID[busy.ID].Busy) != 1 {
		t.Fatalf("busy resource must list its window: %+v", byID[busy.ID])
	}
	if _, ok := byID[inactive.ID]; ok {
		t.Fatal("inactive resources are not availability candidates")
	}

	// A range outside every window: everything free.
	avail2, err := s.Dispatch.Availability(ctx, tenantID, base.Add(24*time.Hour), base.Add(26*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, ra := range avail2 {
		if !ra.Free {
			t.Fatalf("outside all windows every resource is free: %+v", ra)
		}
	}

	// Validation.
	if _, err := s.Dispatch.Availability(ctx, tenantID, time.Time{}, base); err != httpx.ErrValidation {
		t.Fatal("missing range must be rejected")
	}
}
