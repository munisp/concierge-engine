package domain

// Phase 65: partner rate cards — validation, quote-time application with
// honest adjustment lines, skip provenance, and commission on the adjusted
// gross.

import (
	"context"
	"testing"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestRateCardValidation65(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	hotel := seedPartner56(t, s, tenantID, "Rate Card Hotel", 10)

	// Happy path: percentage card upserts idempotently by key.
	if _, err := s.RateCards.Set(ctx, tenantID, hotel.ID, &model.RateCard{
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Kind: model.RateCardPctDiscount, Value: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RateCards.Set(ctx, tenantID, hotel.ID, &model.RateCard{
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Kind: model.RateCardPctDiscount, Value: 15,
	}); err != nil {
		t.Fatal(err)
	}
	cards, err := s.RateCards.List(ctx, tenantID, hotel.ID)
	if err != nil || len(cards) != 1 || cards[0].Value != 15 {
		t.Fatalf("upsert: %+v %v", cards, err)
	}

	// Fail-closed validation.
	for _, bad := range []*model.RateCard{
		{Vertical: "CHAUFFEUR_POINT_TO_POINT", Kind: "half_price", Value: 10},                   // unknown kind
		{Vertical: "CHAUFFEUR_POINT_TO_POINT", Kind: model.RateCardPctDiscount},                 // pct 0
		{Vertical: "CHAUFFEUR_POINT_TO_POINT", Kind: model.RateCardPctDiscount, Value: 51},      // over cap
		{Vertical: "CHAUFFEUR_POINT_TO_POINT", Kind: model.RateCardFixedTotal, Value: 7500},     // no currency
		{Vertical: "CHAUFFEUR_POINT_TO_POINT", Kind: model.RateCardFixedTotal, Currency: "EUR"}, // no value
		{Kind: model.RateCardPctDiscount, Value: 10},                                            // no vertical
	} {
		if _, err := s.RateCards.Set(ctx, tenantID, hotel.ID, bad); err != httpx.ErrValidation {
			t.Fatalf("expected 422 for %+v, got %v", bad, err)
		}
	}

	// Unknown partner 404s.
	if _, err := s.RateCards.Set(ctx, tenantID, NewID(), &model.RateCard{
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Kind: model.RateCardPctDiscount, Value: 10,
	}); err != httpx.ErrNotFound {
		t.Fatalf("unknown partner: %v", err)
	}

	// Delete: missing 404s, existing removes.
	if err := s.RateCards.Delete(ctx, tenantID, hotel.ID, "CHAUFFEUR_HOURLY"); err != httpx.ErrNotFound {
		t.Fatalf("delete missing: %v", err)
	}
	if err := s.RateCards.Delete(ctx, tenantID, hotel.ID, "CHAUFFEUR_POINT_TO_POINT"); err != nil {
		t.Fatal(err)
	}
	if cards, _ := s.RateCards.List(ctx, tenantID, hotel.ID); len(cards) != 0 {
		t.Fatalf("after delete: %+v", cards)
	}
}

func TestRateCardQuoting65(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID) // Sedan base 9000 EUR, per-km 0

	hotel := seedPartner56(t, s, tenantID, "Discount Hotel", 10) // 10% commission
	if _, err := s.Channels.SetPartnerKind(ctx, tenantID, hotel.ID, model.PartnerKindHotel); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RateCards.Set(ctx, tenantID, hotel.ID, &model.RateCard{
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Kind: model.RateCardPctDiscount, Value: 10,
	}); err != nil {
		t.Fatal(err)
	}

	// Sourced request reprices: −10% line on the quote, total 8100.
	r, err := s.Channels.CreateSourcedRequest(ctx, tenantID, hotel.ID, &SourcedRequestInput{
		GuestName: "Rate Guest", GuestPhone: "+31670000001",
		Items: []model.ServiceItem{sourceItem63(s)}, IdempotencyKey: "rc-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	q, instant, err := s.Booking.QuoteRequest(ctx, tenantID, r.ID)
	if err != nil || !instant {
		t.Fatalf("quote: %v instant=%v", err, instant)
	}
	if q.TotalMinor != 8100 {
		t.Fatalf("discounted total: %d lines %+v", q.TotalMinor, q.Lines)
	}
	var adjLine *model.QuoteLine
	for i := range q.Lines {
		if q.Lines[i].AmountMinor == -900 {
			adjLine = &q.Lines[i]
		}
	}
	if adjLine == nil || adjLine.Description != "Partner rate — Discount Hotel (−10%)" {
		t.Fatalf("adjustment line missing/opaque: %+v", q.Lines)
	}

	// Commission accrues on the ADJUSTED gross: 10% of 8100 = 810.
	if _, err := s.Booking.Confirm(ctx, tenantID, q.ID); err != nil {
		t.Fatal(err)
	}
	po := commissionPayouts56(t, s, tenantID, hotel.ID)
	if len(po) != 1 || po[0].AmountMinor != 810 {
		t.Fatalf("commission on adjusted gross: %+v", po)
	}

	// Direct demand for the same guest never sees the card.
	dr, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
		TenantID: tenantID, ClientID: r.ClientID, IdempotencyKey: "rc-direct",
		Items: []model.ServiceItem{sourceItem63(s)},
	})
	if err != nil {
		t.Fatal(err)
	}
	dq, instant, err := s.Booking.QuoteRequest(ctx, tenantID, dr.ID)
	if err != nil || !instant {
		t.Fatalf("direct quote: %v instant=%v", err, instant)
	}
	if dq.TotalMinor != 9000 {
		t.Fatalf("direct quote must ignore rate cards: %d", dq.TotalMinor)
	}
	for _, l := range dq.Lines {
		if l.AmountMinor < 0 {
			t.Fatalf("direct quote carried a partner adjustment: %+v", dq.Lines)
		}
	}
}

func TestRateCardFixedAndSkip65(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID) // 9000 EUR base

	agency := seedPartner56(t, s, tenantID, "Fixed Fare Agency", 15)
	if _, err := s.Channels.SetPartnerKind(ctx, tenantID, agency.ID, model.PartnerKindTravelAgency); err != nil {
		t.Fatal(err)
	}

	// Fixed contracted fare: the item subtotal becomes 7500 whatever the
	// catalog says — the adjustment line shows the −1500 delta honestly.
	if _, err := s.RateCards.Set(ctx, tenantID, agency.ID, &model.RateCard{
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Kind: model.RateCardFixedTotal,
		Value: 7500, Currency: "EUR",
	}); err != nil {
		t.Fatal(err)
	}
	r, err := s.Channels.CreateSourcedRequest(ctx, tenantID, agency.ID, &SourcedRequestInput{
		GuestName: "Fixed Guest", GuestPhone: "+31670000002",
		Items: []model.ServiceItem{sourceItem63(s)}, IdempotencyKey: "fx-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	q, _, err := s.Booking.QuoteRequest(ctx, tenantID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if q.TotalMinor != 7500 {
		t.Fatalf("fixed total: %d lines %+v", q.TotalMinor, q.Lines)
	}

	// Currency mismatch: the card is skipped (event emitted), the quote
	// prices at catalog — a wrong-currency charge is never invented.
	if _, err := s.RateCards.Set(ctx, tenantID, agency.ID, &model.RateCard{
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Kind: model.RateCardFixedTotal,
		Value: 6500, Currency: "GBP",
	}); err != nil {
		t.Fatal(err)
	}
	r2, err := s.Channels.CreateSourcedRequest(ctx, tenantID, agency.ID, &SourcedRequestInput{
		GuestName: "Fixed Guest", GuestPhone: "+31670000002",
		Items: []model.ServiceItem{sourceItem63(s)}, IdempotencyKey: "fx-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	q2, _, err := s.Booking.QuoteRequest(ctx, tenantID, r2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if q2.TotalMinor != 9000 {
		t.Fatalf("mismatched-currency card must skip: %d", q2.TotalMinor)
	}

	// Markup card: farm-in overflow carries +20%.
	fleet := seedPartner56(t, s, tenantID, "Farm-in Fleet", 20)
	if _, err := s.Channels.SetPartnerKind(ctx, tenantID, fleet.ID, model.PartnerKindTransportCompany); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RateCards.Set(ctx, tenantID, fleet.ID, &model.RateCard{
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Kind: model.RateCardPctMarkup, Value: 20,
	}); err != nil {
		t.Fatal(err)
	}
	r3, err := s.Channels.CreateSourcedRequest(ctx, tenantID, fleet.ID, &SourcedRequestInput{
		GuestName: "Overflow Guest", GuestPhone: "+31670000003",
		Items: []model.ServiceItem{sourceItem63(s)}, IdempotencyKey: "mk-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	q3, _, err := s.Booking.QuoteRequest(ctx, tenantID, r3.ID)
	if err != nil {
		t.Fatal(err)
	}
	if q3.TotalMinor != 10800 {
		t.Fatalf("markup total: %d", q3.TotalMinor)
	}
}
