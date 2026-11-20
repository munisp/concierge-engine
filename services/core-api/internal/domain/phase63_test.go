package domain

// Phase 63: demand channels — partner book-on-behalf intake into the
// standard pipeline, sourcing commission with the no-double-pay rule, and
// the per-channel margin report.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func sourceItem63(s *Services) model.ServiceItem {
	return model.ServiceItem{Vertical: "CHAUFFEUR_POINT_TO_POINT",
		PickupAddress: "Hotel Lobby", DropoffAddress: "Airport",
		PickupAt: s.Now().Add(48 * time.Hour).Unix(), Passengers: 2, Hours: 2}
}

func seedCatalog63(t *testing.T, s *Services, tenantID string) {
	t.Helper()
	if _, err := s.Catalog.Add(context.Background(), &model.CatalogService{
		TenantID: tenantID, Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Sedan|63",
		BaseMinor: 9000, Currency: "EUR", PerKMMinor: 0, AutoQuotable: true, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSourcedRequestFlow63(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)
	hotel := seedPartner56(t, s, tenantID, "Hotel Concierge Desk", 10)
	if _, err := s.Channels.SetPartnerKind(ctx, tenantID, hotel.ID, model.PartnerKindHotel); err != nil {
		t.Fatal(err)
	}

	// Book-on-behalf: the hotel desk creates a request for a guest.
	r, err := s.Channels.CreateSourcedRequest(ctx, tenantID, hotel.ID, &SourcedRequestInput{
		GuestName: "Ada Guest", GuestPhone: "+31610000001",
		Items: []model.ServiceItem{sourceItem63(s)}, IdempotencyKey: "desk-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.SourceKind != model.SourceHotel || r.SourcePartnerID != hotel.ID {
		t.Fatalf("source fields: %+v", r)
	}
	if r.State != model.StateCapturing {
		t.Fatalf("sourced request must enter the standard pipeline, got %s", r.State)
	}

	// Idempotent replay returns the original request.
	r2, err := s.Channels.CreateSourcedRequest(ctx, tenantID, hotel.ID, &SourcedRequestInput{
		GuestName: "Ada Guest", GuestPhone: "+31610000001",
		Items: []model.ServiceItem{sourceItem63(s)}, IdempotencyKey: "desk-1",
	})
	if err != nil || r2.ID != r.ID {
		t.Fatalf("replay: %v %v", r2, err)
	}

	// The guest was resolved into a real client; a second request for the
	// same phone reuses it.
	r3, err := s.Channels.CreateSourcedRequest(ctx, tenantID, hotel.ID, &SourcedRequestInput{
		GuestName: "Ada Guest", GuestPhone: "+31610000001",
		Items: []model.ServiceItem{sourceItem63(s)}, IdempotencyKey: "desk-2",
	})
	if err != nil || r3.ClientID != r.ClientID {
		t.Fatalf("guest reuse: %v %v", r3, err)
	}

	// Partner portal feed: newest first, only this partner's demand.
	feed, err := s.Channels.SourcedRequests(ctx, tenantID, hotel.ID, 10)
	if err != nil || len(feed) != 2 {
		t.Fatalf("feed: %v %v", feed, err)
	}
	if feed[0].ID != r3.ID || feed[1].ID != r.ID || feed[0].GuestName != "Ada Guest" {
		t.Fatalf("feed order/content: %+v", feed)
	}

	// Validation: unreachable guest, no items, missing idem.
	if _, err := s.Channels.CreateSourcedRequest(ctx, tenantID, hotel.ID, &SourcedRequestInput{
		GuestName: "Nobody", Items: []model.ServiceItem{sourceItem63(s)}, IdempotencyKey: "x",
	}); err != httpx.ErrValidation {
		t.Fatalf("unreachable guest: %v", err)
	}
	if _, err := s.Channels.CreateSourcedRequest(ctx, tenantID, hotel.ID, &SourcedRequestInput{
		GuestPhone: "+31610000002", IdempotencyKey: "x",
	}); err != httpx.ErrValidation {
		t.Fatalf("no items: %v", err)
	}

	// Suspended partners cannot source demand.
	susp := seedPartner56(t, s, tenantID, "Suspended Partner", 10)
	susp.Status = "SUSPENDED"
	if err := s.Store.Partners().Update(ctx, susp); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Channels.CreateSourcedRequest(ctx, tenantID, susp.ID, &SourcedRequestInput{
		GuestPhone: "+31610000003", Items: []model.ServiceItem{sourceItem63(s)}, IdempotencyKey: "x",
	}); err != httpx.ErrForbidden {
		t.Fatalf("suspended: %v", err)
	}

	// Kind validation.
	if _, err := s.Channels.SetPartnerKind(ctx, tenantID, hotel.ID, "airline"); err != httpx.ErrValidation {
		t.Fatalf("bad kind: %v", err)
	}
}

func TestSourcingCommission63(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)
	agency := seedPartner56(t, s, tenantID, "Travel Agency", 10) // 10% → €9.00 on €90.00
	if _, err := s.Channels.SetPartnerKind(ctx, tenantID, agency.ID, model.PartnerKindTravelAgency); err != nil {
		t.Fatal(err)
	}

	// Sourced request → quote → confirm accrues sourcing commission.
	r, err := s.Channels.CreateSourcedRequest(ctx, tenantID, agency.ID, &SourcedRequestInput{
		GuestName: "Bo Guest", GuestPhone: "+31620000001",
		Items: []model.ServiceItem{sourceItem63(s)}, IdempotencyKey: "ta-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	q, instant, err := s.Booking.QuoteRequest(ctx, tenantID, r.ID)
	if err != nil || !instant {
		t.Fatalf("quote: %v instant=%v", err, instant)
	}
	b, err := s.Booking.Confirm(ctx, tenantID, q.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Source denormalized onto the booking.
	if b.SourceKind != model.SourceTravelAgency || b.SourcePartnerID != agency.ID {
		t.Fatalf("booking source: %+v", b)
	}
	po := commissionPayouts56(t, s, tenantID, agency.ID)
	if len(po) != 1 || po[0].AmountMinor != 900 || po[0].BookingID != b.ID {
		t.Fatalf("sourcing commission: %+v", po)
	}

	// The same partner fulfilling their own sourced booking is paid ONCE —
	// fulfilment accrual skips when a sourcing payout exists.
	if _, err := s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: b.ID, PartnerID: agency.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if po := commissionPayouts56(t, s, tenantID, agency.ID); len(po) != 1 {
		t.Fatalf("double pay: %d payouts", len(po))
	}

	// A different fulfiller earns their own fulfilment commission.
	fleet := seedPartner56(t, s, tenantID, "Transport Company", 20)
	if _, err := s.Channels.SetPartnerKind(ctx, tenantID, fleet.ID, model.PartnerKindTransportCompany); err != nil {
		t.Fatal(err)
	}
	b2 := bookAt(t, s, tenantID, r.ClientID, "direct-63", s.Now().Add(72*time.Hour).Unix(), 2)
	if _, err := s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: b2, PartnerID: fleet.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if po := commissionPayouts56(t, s, tenantID, fleet.ID); len(po) != 1 {
		t.Fatalf("fulfilment commission missing: %+v", po)
	}

	// Direct bookings accrue no sourcing commission at all.
	nb, _ := s.Store.Bookings().Get(ctx, tenantID, b2)
	if nb.SourceKind != model.SourceDirect || nb.SourcePartnerID != "" {
		t.Fatalf("direct booking source: %+v", nb)
	}
}

func TestSourceReport63(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)
	hotel := seedPartner56(t, s, tenantID, "Hotel Desk", 25) // 25% commission
	if _, err := s.Channels.SetPartnerKind(ctx, tenantID, hotel.ID, model.PartnerKindHotel); err != nil {
		t.Fatal(err)
	}

	// One sourced booking (commission-bearing) + one direct booking.
	r, err := s.Channels.CreateSourcedRequest(ctx, tenantID, hotel.ID, &SourcedRequestInput{
		GuestName: "Cy Guest", GuestPhone: "+31630000001",
		Items: []model.ServiceItem{sourceItem63(s)}, IdempotencyKey: "rep-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	q, _, err := s.Booking.QuoteRequest(ctx, tenantID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Booking.Confirm(ctx, tenantID, q.ID); err != nil {
		t.Fatal(err)
	}
	bookAt(t, s, tenantID, r.ClientID, "rep-direct", s.Now().Add(96*time.Hour).Unix(), 2)

	rep, err := s.Channels.SourceReport(ctx, tenantID, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 2 {
		t.Fatalf("rows: %+v", rep.Rows)
	}
	// Both quotes are €90.00 (base 9000, per-km 0). Hotel row: gross 9000,
	// commission 2250, net 6750. Direct row: gross 9000, commission 0,
	// net 9000 — the most lucrative channel, and the report shows it.
	var direct, hotelRow *model.SourceReportRow
	for i := range rep.Rows {
		switch rep.Rows[i].SourceKind {
		case model.SourceDirect:
			direct = &rep.Rows[i]
		case model.SourceHotel:
			hotelRow = &rep.Rows[i]
		}
	}
	if direct == nil || hotelRow == nil {
		t.Fatalf("missing rows: %+v", rep.Rows)
	}
	if direct.GrossMinor != 9000 || direct.CommissionMinor != 0 || direct.NetMinor != 9000 || direct.Currency != "EUR" {
		t.Fatalf("direct row: %+v", direct)
	}
	if hotelRow.GrossMinor != 9000 || hotelRow.CommissionMinor != 2250 || hotelRow.NetMinor != 6750 {
		t.Fatalf("hotel row: %+v", hotelRow)
	}
	if rep.Rows[0].SourceKind != model.SourceDirect && rep.Rows[0].GrossMinor < rep.Rows[1].GrossMinor {
		t.Fatalf("not sorted by gross desc: %+v", rep.Rows)
	}
}
