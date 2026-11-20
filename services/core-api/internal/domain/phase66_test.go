package domain

// Phase 66: bill-to-desk folios — opt-in flag, accrual at confirm on the
// adjusted gross, idempotent billing, issue/settle lifecycle with ledger
// money truth, and blocked-accrual provenance once a period is issued.

import (
	"context"
	"testing"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// confirmSourced66 books one sourced guest through the full pipeline and
// returns the booking.
func confirmSourced66(t *testing.T, s *Services, tenantID, partnerID, phone, idem string) *model.Booking {
	t.Helper()
	r, err := s.Channels.CreateSourcedRequest(context.Background(), tenantID, partnerID, &SourcedRequestInput{
		GuestName: "Desk Guest", GuestPhone: phone,
		Items: []model.ServiceItem{sourceItem63(s)}, IdempotencyKey: idem,
	})
	if err != nil {
		t.Fatal(err)
	}
	q, instant, err := s.Booking.QuoteRequest(context.Background(), tenantID, r.ID)
	if err != nil || !instant {
		t.Fatalf("quote: %v instant=%v", err, instant)
	}
	b, err := s.Booking.Confirm(context.Background(), tenantID, q.ID)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func folioFor66(t *testing.T, s *Services, tenantID, partnerID string) []model.Folio {
	t.Helper()
	folios, err := s.Folios.List(context.Background(), tenantID, partnerID)
	if err != nil {
		t.Fatal(err)
	}
	return folios
}

func TestFolioAccrual66(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID) // Sedan 9000 EUR

	hotel := seedPartner56(t, s, tenantID, "Folio Hotel", 10)
	if _, err := s.Channels.SetPartnerKind(ctx, tenantID, hotel.ID, model.PartnerKindHotel); err != nil {
		t.Fatal(err)
	}

	// Off by default: a sourced confirm accrues NO folio.
	confirmSourced66(t, s, tenantID, hotel.ID, "+31690000001", "fl-0")
	if folios := folioFor66(t, s, tenantID, hotel.ID); len(folios) != 0 {
		t.Fatalf("desk billing off must not accrue: %+v", folios)
	}

	// Toggle on (idempotent) — sourced bookings accrue the adjusted gross.
	if _, err := s.Folios.SetBillToDesk(ctx, tenantID, hotel.ID, true); err != nil {
		t.Fatal(err)
	}
	p, err := s.Folios.SetBillToDesk(ctx, tenantID, hotel.ID, true)
	if err != nil || !p.BillToDesk {
		t.Fatalf("idempotent toggle: %+v %v", p, err)
	}
	if _, err := s.RateCards.Set(ctx, tenantID, hotel.ID, &model.RateCard{
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Kind: model.RateCardPctDiscount, Value: 10,
	}); err != nil {
		t.Fatal(err)
	}
	b1 := confirmSourced66(t, s, tenantID, hotel.ID, "+31690000002", "fl-1")
	b2 := confirmSourced66(t, s, tenantID, hotel.ID, "+31690000002", "fl-2")

	folios := folioFor66(t, s, tenantID, hotel.ID)
	if len(folios) != 1 {
		t.Fatalf("one folio for the month: %+v", folios)
	}
	f := folios[0]
	if f.State != model.FolioOpen || f.Currency != "EUR" || len(f.Lines) != 2 {
		t.Fatalf("folio: %+v", f)
	}
	if f.TotalMinor != 2*8100 { // rate card −10% applied before accrual
		t.Fatalf("folio bills the adjusted gross: %d", f.TotalMinor)
	}
	for _, l := range f.Lines {
		if l.AmountMinor != 8100 || l.GuestName != "Desk Guest" {
			t.Fatalf("line: %+v", l)
		}
	}
	if f.Lines[0].BookingID != b1.ID || f.Lines[1].BookingID != b2.ID {
		t.Fatalf("line booking ids: %+v", f.Lines)
	}

	// Sourcing commission still accrues independently — 10% of 8100 each.
	if po := commissionPayouts56(t, s, tenantID, hotel.ID); len(po) != 3 { // fl-0..fl-2
		t.Fatalf("commission independent of desk billing: %+v", po)
	}

	// A direct booking for the same guest never accrues.
	r1, err := s.Store.Requests().Get(ctx, tenantID, b1.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	dr, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
		TenantID: tenantID, ClientID: r1.ClientID, IdempotencyKey: "fl-direct",
		Items: []model.ServiceItem{sourceItem63(s)},
	})
	if err != nil {
		t.Fatal(err)
	}
	dq, _, err := s.Booking.QuoteRequest(ctx, tenantID, dr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Booking.Confirm(ctx, tenantID, dq.ID); err != nil {
		t.Fatal(err)
	}
	if folios := folioFor66(t, s, tenantID, hotel.ID); len(folios) != 1 || folios[0].TotalMinor != 2*8100 {
		t.Fatalf("direct booking must not accrue: %+v", folios)
	}
}

func TestFolioIssueSettle66(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	hotel := seedPartner56(t, s, tenantID, "Settle Hotel", 0)
	if _, err := s.Folios.SetBillToDesk(ctx, tenantID, hotel.ID, true); err != nil {
		t.Fatal(err)
	}
	confirmSourced66(t, s, tenantID, hotel.ID, "+31690000011", "st-1")
	confirmSourced66(t, s, tenantID, hotel.ID, "+31690000012", "st-2")

	f := folioFor66(t, s, tenantID, hotel.ID)[0]

	// Settle before issue is a conflict.
	if _, err := s.Folios.Settle(ctx, tenantID, f.ID); err != httpx.ErrConflict {
		t.Fatalf("settle open: %v", err)
	}
	// Validation: bad period / unknown partner / nothing to issue.
	if _, err := s.Folios.Issue(ctx, tenantID, hotel.ID, "2026-11-01", "EUR"); err != httpx.ErrValidation {
		t.Fatalf("bad period: %v", err)
	}
	if _, err := s.Folios.Issue(ctx, tenantID, NewID(), f.Period, "EUR"); err != httpx.ErrNotFound {
		t.Fatalf("unknown partner: %v", err)
	}
	if _, err := s.Folios.Issue(ctx, tenantID, hotel.ID, "2025-01", "EUR"); err != httpx.ErrNotFound {
		t.Fatalf("empty period: %v", err)
	}

	// Issue freezes the total; replay converges.
	issued, err := s.Folios.Issue(ctx, tenantID, hotel.ID, f.Period, "eur") // lowercase ok
	if err != nil {
		t.Fatal(err)
	}
	if issued.State != model.FolioIssued || issued.TotalMinor != 18000 || issued.IssuedAt.IsZero() {
		t.Fatalf("issued: %+v", issued)
	}
	again, err := s.Folios.Issue(ctx, tenantID, hotel.ID, f.Period, "EUR")
	if err != nil || again.State != model.FolioIssued || !again.IssuedAt.Equal(issued.IssuedAt) {
		t.Fatalf("issue idempotent: %+v %v", again, err)
	}

	// Accrual after issue is BLOCKED with provenance, never silently
	// dropped and never reopening the billed statement.
	b3 := confirmSourced66(t, s, tenantID, hotel.ID, "+31690000013", "st-3")
	if folios := folioFor66(t, s, tenantID, hotel.ID); len(folios) != 1 || folios[0].TotalMinor != 18000 {
		t.Fatalf("issued folio frozen: %+v", folios)
	}
	_ = b3

	// Settle posts the money truth: net to the tenant, fee to the platform,
	// keyed per folio so a retry can never double-post.
	settled, err := s.Folios.Settle(ctx, tenantID, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.State != model.FolioSettled || settled.SettledAt.IsZero() {
		t.Fatalf("settled: %+v", settled)
	}
	if _, err := s.Folios.Settle(ctx, tenantID, f.ID); err != nil {
		t.Fatalf("settle idempotent: %v", err)
	}
	postings, err := s.Payments.Postings(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	var net, fee int64
	for _, p := range postings {
		switch p.IdempotencyKey {
		case "folio:net:" + f.ID:
			net = p.AmountMinor
			if p.DebitCode != "PARTNER_CLEARING" || p.CreditCode != "TENANT_REVENUE" || p.Currency != "EUR" {
				t.Fatalf("net posting: %+v", p)
			}
		case "folio:fee:" + f.ID:
			fee = p.AmountMinor
			if p.CreditCode != "PLATFORM_FEES" {
				t.Fatalf("fee posting: %+v", p)
			}
		}
	}
	if net == 0 || fee == 0 || net+fee != 18000 {
		t.Fatalf("postings net=%d fee=%d", net, fee)
	}
}
