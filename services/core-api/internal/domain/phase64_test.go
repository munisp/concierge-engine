package domain

// Phase 64: channel engagement closure — guest + sourcing-partner confirm
// notifications on sourced bookings, and operator reattribution of
// off-channel demand (email/phone) to the partner who really sent it.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func sourcedBooking64(t *testing.T, s *Services, tenantID string, p *model.Partner, idem string) *model.Booking {
	t.Helper()
	ctx := context.Background()
	r, err := s.Channels.CreateSourcedRequest(ctx, tenantID, p.ID, &SourcedRequestInput{
		GuestName: "Notify Guest", GuestPhone: "+31660" + idem[len(idem)-5:] + "1",
		Items: []model.ServiceItem{sourceItem63(s)}, IdempotencyKey: idem,
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
	return b
}

func TestSourcedConfirmNotifications64(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)
	hotel := seedPartner56(t, s, tenantID, "Notify Hotel", 10)
	if _, err := s.Channels.SetPartnerKind(ctx, tenantID, hotel.ID, model.PartnerKindHotel); err != nil {
		t.Fatal(err)
	}

	b := sourcedBooking64(t, s, tenantID, hotel, "notify00001")

	// Confirm fires the notifications detached (channel latency never
	// holds the API); the test invokes the notifier synchronously to
	// assert the behavior, not the goroutine timing. The guest send is
	// idempotent, so the detached fire replays harmlessly.
	s.Channels.NotifySourcedConfirmed(ctx, tenantID, b)

	// The guest got a service-lane confirmation naming the arranger —
	// with no channel adapter configured the send records its honest
	// status, but the notification MUST exist (never silent, never fake).
	sends, err := s.Store.Outreach().ListSends(ctx, tenantID, 50)
	if err != nil {
		t.Fatal(err)
	}
	var guestSend *model.OutreachSend
	for i := range sends {
		if sends[i].IdempotencyKey == "src-confirm:"+b.ID {
			guestSend = &sends[i]
		}
	}
	if guestSend == nil {
		t.Fatal("no guest confirmation send recorded")
	}
	if guestSend.Purpose != model.OutreachService {
		t.Fatalf("must be the service lane, got %s", guestSend.Purpose)
	}

	// Direct bookings trigger no channel notification.
	bookAt(t, s, tenantID, "direct-client-64", "direct64", s.Now().Add(72*time.Hour).Unix(), 2)
	sends2, _ := s.Store.Outreach().ListSends(ctx, tenantID, 50)
	for _, sd := range sends2 {
		if sd.IdempotencyKey != "" && len(sd.IdempotencyKey) > 12 && sd.IdempotencyKey[:12] == "src-confirm:" && sd.IdempotencyKey != "src-confirm:"+b.ID {
			t.Fatalf("direct booking triggered a channel notification: %+v", sd)
		}
	}
}

func TestAttributeSource64(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)
	agency := seedPartner56(t, s, tenantID, "Email Agency", 10)
	if _, err := s.Channels.SetPartnerKind(ctx, tenantID, agency.ID, model.PartnerKindTravelAgency); err != nil {
		t.Fatal(err)
	}

	// Demand arrives "via email": a plain direct request, quoted, confirmed.
	bookingID := bookAt(t, s, tenantID, "email-client-64", "email64", s.Now().Add(72*time.Hour).Unix(), 2)
	b, _ := s.Store.Bookings().Get(ctx, tenantID, bookingID)
	if b.SourceKind != model.SourceDirect {
		t.Fatalf("precondition: %+v", b)
	}

	// The operator learns the agency sent it — reattribute.
	r, err := s.Channels.AttributeSource(ctx, tenantID, b.RequestID, agency.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.SourceKind != model.SourceTravelAgency || r.SourcePartnerID != agency.ID {
		t.Fatalf("request reattributed: %+v", r)
	}
	// Round-trip through the store.
	got, _ := s.Store.Requests().Get(ctx, tenantID, b.RequestID)
	if got.SourcePartnerID != agency.ID {
		t.Fatalf("request source persisted: %+v", got)
	}
	nb, _ := s.Store.Bookings().Get(ctx, tenantID, bookingID)
	if nb.SourceKind != model.SourceTravelAgency || nb.SourcePartnerID != agency.ID {
		t.Fatalf("booking followed: %+v", nb)
	}
	// The confirmed booking accrues the sourcing commission now.
	po := commissionPayouts56(t, s, tenantID, agency.ID)
	if len(po) != 1 || po[0].BookingID != bookingID {
		t.Fatalf("commission on reattribution: %+v", po)
	}

	// Idempotent: attributing again neither errors nor double-accrues.
	if _, err := s.Channels.AttributeSource(ctx, tenantID, b.RequestID, agency.ID); err != nil {
		t.Fatal(err)
	}
	if po := commissionPayouts56(t, s, tenantID, agency.ID); len(po) != 1 {
		t.Fatalf("double accrual on re-attribute: %+v", po)
	}

	// Unknown partner 404s; suspended partner 403s.
	if _, err := s.Channels.AttributeSource(ctx, tenantID, b.RequestID, NewID()); err != httpx.ErrNotFound {
		t.Fatalf("unknown partner: %v", err)
	}
	susp := seedPartner56(t, s, tenantID, "Suspended64", 5)
	susp.Status = "SUSPENDED"
	if err := s.Store.Partners().Update(ctx, susp); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Channels.AttributeSource(ctx, tenantID, b.RequestID, susp.ID); err != httpx.ErrForbidden {
		t.Fatalf("suspended partner: %v", err)
	}

	// Clearing returns the request and booking to direct; the accrued
	// commission is money history and is NOT rewritten.
	if _, err := s.Channels.AttributeSource(ctx, tenantID, b.RequestID, ""); err != nil {
		t.Fatal(err)
	}
	nb2, _ := s.Store.Bookings().Get(ctx, tenantID, bookingID)
	if nb2.SourceKind != model.SourceDirect || nb2.SourcePartnerID != "" {
		t.Fatalf("cleared booking: %+v", nb2)
	}
	if po := commissionPayouts56(t, s, tenantID, agency.ID); len(po) != 1 {
		t.Fatalf("history rewritten: %+v", po)
	}
}
