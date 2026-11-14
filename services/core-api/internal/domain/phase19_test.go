package domain

// Phase 26: acquisition monetization — pay-for-performance fee on
// platform-attributed first bookings, refund clawback, the fee invoice,
// and the new-client cohort tracker.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// captureAcq books one trip for the client and captures its payment;
// returns the intent id.
func captureAcq(t *testing.T, s *Services, tenantID, clientID, idem string, amount int64) string {
	t.Helper()
	ctx := context.Background()
	bookingID := bookAt(t, s, tenantID, clientID, idem, s.Now().Add(48*time.Hour).Unix(), 2)
	intent := &model.PaymentIntent{
		ID: NewID(), TenantID: tenantID, BookingID: bookingID,
		Mode: "FULL", AmountMinor: amount, Currency: "EUR", PSP: "stripe",
		IdempotencyKey: "acq-intent-" + idem,
	}
	if err := s.Store.Payments().CreateIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	if err := s.Payments.HandlePSPWebhook(ctx, tenantID, intent.ID, "payment.captured", "psp-"+idem); err != nil {
		t.Fatal(err)
	}
	return intent.ID
}

func acqFees(t *testing.T, s *Services, tenantID string) (fees, clawbacks int64) {
	t.Helper()
	postings, err := s.Store.Payments().Postings(context.Background(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range postings {
		switch {
		case p.CreditCode == "PLATFORM_ACQUISITION_FEES":
			fees += p.AmountMinor
		case p.DebitCode == "PLATFORM_ACQUISITION_FEES":
			clawbacks += p.AmountMinor
		}
	}
	return fees, clawbacks
}

func TestAcquisitionFeeAssessed(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	// SEO-sourced client: page_view first touch, then the first booking.
	seoClient := outreachClient(t, s, tenantID, "+31622220001", false)
	s.TrackInherited(ctx, &model.AttributionEvent{
		TenantID: tenantID, ClientID: seoClient, Kind: "page_view", Channel: "seo"})
	captureAcq(t, s, tenantID, seoClient, "acq-seo-1", 100000)

	fees, _ := acqFees(t, s, tenantID)
	if fees != 10000 {
		t.Fatalf("acquisition fee: want 10000 (10%% of 100000), got %d", fees)
	}

	// The SAME client's second booking is retention, not acquisition.
	captureAcq(t, s, tenantID, seoClient, "acq-seo-2", 100000)
	fees, _ = acqFees(t, s, tenantID)
	if fees != 10000 {
		t.Fatalf("fee charged on a second booking: %d", fees)
	}

	// A direct client (no attributed touch) pays no acquisition fee.
	direct := outreachClient(t, s, tenantID, "+31622220002", false)
	captureAcq(t, s, tenantID, direct, "acq-direct-1", 100000)
	fees, _ = acqFees(t, s, tenantID)
	if fees != 10000 {
		t.Fatalf("fee charged for direct traffic: %d", fees)
	}

	// A referral arrival classifies as "referral" via the ref: campaign.
	refClient := outreachClient(t, s, tenantID, "+31622220003", false)
	s.TrackInherited(ctx, &model.AttributionEvent{
		TenantID: tenantID, ClientID: refClient, Kind: "page_view",
		Channel: "referral", Campaign: "ref:VIP-ABCDEF12"})
	captureAcq(t, s, tenantID, refClient, "acq-ref-1", 50000)
	fees, _ = acqFees(t, s, tenantID)
	if fees != 15000 {
		t.Fatalf("referral first booking not charged: %d", fees)
	}
}

func TestAcquisitionFeeDisabled(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	tenant, err := s.Tenants.Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	tenant.AcquisitionConfig = model.AcquisitionConfig{Disabled: true}
	if _, err := s.Tenants.UpdateConfig(ctx, tenant); err != nil {
		t.Fatal(err)
	}

	cl := outreachClient(t, s, tenantID, "+31622220010", false)
	s.TrackInherited(ctx, &model.AttributionEvent{
		TenantID: tenantID, ClientID: cl, Kind: "page_view", Channel: "seo"})
	captureAcq(t, s, tenantID, cl, "acq-off-1", 100000)
	if fees, _ := acqFees(t, s, tenantID); fees != 0 {
		t.Fatalf("disabled tenant charged: %d", fees)
	}
}

func TestAcquisitionFeeRefundClawback(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	cl := outreachClient(t, s, tenantID, "+31622220020", false)
	s.TrackInherited(ctx, &model.AttributionEvent{
		TenantID: tenantID, ClientID: cl, Kind: "page_view", Channel: "seo"})
	intentID := captureAcq(t, s, tenantID, cl, "acq-claw-1", 100000)

	if err := s.Payments.HandlePSPWebhook(ctx, tenantID, intentID, "charge.refunded", "psp-claw-1"); err != nil {
		t.Fatal(err)
	}
	fees, clawbacks := acqFees(t, s, tenantID)
	if fees != 10000 || clawbacks != 10000 {
		t.Fatalf("clawback mismatch: fees=%d clawbacks=%d", fees, clawbacks)
	}

	// The invoice nets to zero for the period.
	now := s.Now()
	inv, err := s.AcquisitionInvoice(ctx, tenantID, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if inv.GrossFeeMinor != 10000 || inv.ClawbackMinor != 10000 || inv.NetFeeMinor != 0 {
		t.Fatalf("invoice: %+v", inv)
	}
	if len(inv.Lines) != 1 || inv.Lines[0].Channel != "seo" || inv.Lines[0].ClientID != cl {
		t.Fatalf("invoice line evidence: %+v", inv.Lines)
	}
	if inv.AttributedGMVMinor != 100000 {
		t.Fatalf("attributed GMV: %d", inv.AttributedGMVMinor)
	}
}

func TestAcquisitionCohort(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	mk := func(phone, channel, campaign string, book bool) string {
		cl := outreachClient(t, s, tenantID, phone, false)
		if channel != "" || campaign != "" {
			s.TrackInherited(ctx, &model.AttributionEvent{
				TenantID: tenantID, ClientID: cl, Kind: "page_view",
				Channel: channel, Campaign: campaign})
		}
		if book {
			bookAt(t, s, tenantID, cl, "cohort-"+phone, s.Now().Add(48*time.Hour).Unix(), 1)
		}
		return cl
	}
	mk("+31622220030", "seo", "", true)              // counts
	mk("+31622220031", "", "ref:VIP-ABCDEF12", true) // counts (referral)
	mk("+31622220032", "", "", true)                 // direct — no
	mk("+31622220033", "seo", "", false)             // never booked — no
	// A client created BEFORE the cohort window is not a new acquisition.
	// The window starts at tenant creation (seedTenant, before the clients
	// above); backdating this client's CreatedAt puts them outside it.
	old := outreachClient(t, s, tenantID, "+31622220034", false)
	s.TrackInherited(ctx, &model.AttributionEvent{
		TenantID: tenantID, ClientID: old, Kind: "page_view", Channel: "seo"})
	bookAt(t, s, tenantID, old, "cohort-old", s.Now().Add(48*time.Hour).Unix(), 1)
	oldClient, err := s.Clients.Get(ctx, tenantID, old)
	if err != nil {
		t.Fatal(err)
	}
	oldClient.CreatedAt = s.Now().Add(-10 * 24 * time.Hour)
	if err := s.Store.Clients().Upsert(ctx, oldClient); err != nil {
		t.Fatal(err)
	}

	rep, err := s.AcquisitionCohort(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.GoalClients != 100 || rep.GoalDays != 90 {
		t.Fatalf("goal defaults: %+v", rep)
	}
	if rep.NewClients != 2 {
		t.Fatalf("new clients: want 2, got %d (%+v)", rep.NewClients, rep.ByChannel)
	}
	if rep.ByChannel["seo"] != 1 || rep.ByChannel["referral"] != 1 {
		t.Fatalf("channel breakdown: %+v", rep.ByChannel)
	}
	if rep.DaysElapsed != 1 || rep.PacingExpected < 1.0 || rep.PacingExpected > 1.2 {
		t.Fatalf("pacing: %+v", rep)
	}
	if rep.Projection != 180 || !rep.OnTrack {
		t.Fatalf("projection: %+v", rep)
	}
}
