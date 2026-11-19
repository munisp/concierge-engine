package domain

// Phase 56: partner integration layer — commission accrual into the payout
// saga, partner accept/decline over the trip state machine, partner-scoped
// webhook delivery, and partner API-key minting.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func seedPartner56(t *testing.T, s *Services, tenantID, name string, pct float64) *model.Partner {
	t.Helper()
	p, err := s.Partners.Create(context.Background(), &model.Partner{
		ID: NewID(), TenantID: tenantID, Name: name,
		Verticals: []string{"TOURS"}, Status: "VETTED",
		PayoutRail: "MANUAL", CommissionPct: pct,
	})
	if err != nil {
		t.Fatalf("seed partner: %v", err)
	}
	return p
}

// bookForPartner56 seeds a confirmed booking with a quoted total — the
// commission base.
func bookForPartner56(t *testing.T, s *Services, tenantID string, totalMinor int64) (bookingID string) {
	t.Helper()
	ctx := context.Background()
	sr, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
		TenantID: tenantID, ClientID: "c56", IdempotencyKey: "p56-" + NewID()[:8],
		Items: []model.ServiceItem{{Vertical: "TOURS",
			PickupAddress: "Lisbon", PickupAt: s.Now().Add(48 * time.Hour).Unix(), Passengers: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	q := &model.Quote{ID: NewID(), TenantID: tenantID, RequestID: sr.ID,
		TotalMinor: totalMinor, Currency: "EUR", CreatedAt: s.Now()}
	if err := s.Store.Quotes().Create(ctx, q); err != nil {
		t.Fatal(err)
	}
	b := &model.Booking{ID: NewID(), TenantID: tenantID, RequestID: sr.ID, QuoteID: q.ID,
		State: "CONFIRMED", CreatedAt: s.Now()}
	if err := s.Store.Bookings().Create(ctx, b); err != nil {
		t.Fatal(err)
	}
	return b.ID
}

func commissionPayouts56(t *testing.T, s *Services, tenantID, partnerID string) []model.Payout {
	t.Helper()
	list, err := s.Store.Payouts().List(context.Background(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	var out []model.Payout
	for _, po := range list {
		if po.PartnerID == partnerID {
			out = append(out, po)
		}
	}
	return out
}

func TestPartnerCommissionAccrual56(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	p := seedPartner56(t, s, tenantID, "Younique Tailor Tours", 12.5)
	bookingID := bookForPartner56(t, s, tenantID, 12000) // €120.00 → 12.5% = €15.00

	if _, err := s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: bookingID, PartnerID: p.ID,
	}); err != nil {
		t.Fatal(err)
	}
	po := commissionPayouts56(t, s, tenantID, p.ID)
	if len(po) != 1 || po[0].AmountMinor != 1500 || po[0].State != model.PayoutPending {
		t.Fatalf("commission payout: %+v", po)
	}
	if po[0].Currency != "EUR" || po[0].Rail != "MANUAL" || po[0].BookingID != bookingID {
		t.Fatalf("commission fields: %+v", po[0])
	}

	// A second assignment of the same booking to the same partner must not
	// double-accrue (idempotency key commission:{booking}:{partner}).
	if _, err := s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: bookingID, PartnerID: p.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if po := commissionPayouts56(t, s, tenantID, p.ID); len(po) != 1 {
		t.Fatalf("double accrual: %d payouts", len(po))
	}

	// A partner without a commission rate accrues nothing.
	p2 := seedPartner56(t, s, tenantID, "Mr Travel", 0)
	if _, err := s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: bookingID, PartnerID: p2.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if po := commissionPayouts56(t, s, tenantID, p2.ID); len(po) != 0 {
		t.Fatalf("zero-pct partner accrued: %+v", po)
	}
}

func TestPartnerRespondFlow56(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	p := seedPartner56(t, s, tenantID, "The Little Orange", 10)
	other := seedPartner56(t, s, tenantID, "Other Operator", 10)
	bookingID := bookForPartner56(t, s, tenantID, 20000)

	a, err := s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: bookingID, PartnerID: p.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Another partner never learns the assignment exists.
	if _, err := s.PartnerOps.Respond(ctx, tenantID, other.ID, a.ID, "accept", ""); err != httpx.ErrNotFound {
		t.Fatalf("cross-partner respond: %v", err)
	}
	// Bad action.
	if _, err := s.PartnerOps.Respond(ctx, tenantID, p.ID, a.ID, "maybe", ""); err != httpx.ErrValidation {
		t.Fatalf("bad action: %v", err)
	}
	// Accept rides the state machine.
	got, err := s.PartnerOps.Respond(ctx, tenantID, p.ID, a.ID, "accept", "confirmed for Saturday")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "PARTNER_ACCEPTED" {
		t.Fatalf("status: %s", got.Status)
	}
	// No double response.
	if _, err := s.PartnerOps.Respond(ctx, tenantID, p.ID, a.ID, "decline", ""); err != httpx.ErrConflict {
		t.Fatalf("double respond: %v", err)
	}
	// Commission stays pending after accept.
	if po := commissionPayouts56(t, s, tenantID, p.ID); len(po) != 1 || po[0].State != model.PayoutPending {
		t.Fatalf("accept must keep commission pending: %+v", po)
	}

	// A declined assignment voids its still-pending commission.
	bookingID2 := bookForPartner56(t, s, tenantID, 20000)
	a2, err := s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: bookingID2, PartnerID: p.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PartnerOps.Respond(ctx, tenantID, p.ID, a2.ID, "decline", "no availability"); err != nil {
		t.Fatal(err)
	}
	pos := commissionPayouts56(t, s, tenantID, p.ID)
	var declined *model.Payout
	for i := range pos {
		if pos[i].BookingID == bookingID2 {
			declined = &pos[i]
		}
	}
	if declined == nil || declined.State != model.PayoutFailed {
		t.Fatalf("decline must void pending commission: %+v", pos)
	}
	// Declined is terminal — the slot frees for reassignment.
	if !terminalAssignmentStatuses["PARTNER_DECLINED"] {
		t.Fatal("PARTNER_DECLINED must be terminal")
	}
}

func TestPartnerWebhookScoping56(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	p := seedPartner56(t, s, tenantID, "Younique Tailor Tours", 10)

	// Tenant-wide feed + one partner-scoped feed.
	if _, err := s.Webhooks.CreateSubscription(ctx, tenantID, "https://ops.example.com/hook", []string{"*"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Webhooks.CreatePartnerSubscription(ctx, tenantID, p.ID,
		"https://younique.example.com/hook", []string{"assignment.*", "partner.*"}); err != nil {
		t.Fatal(err)
	}

	// Event carrying this partner's id → both feeds.
	s.Webhooks.FanOut(ctx, tenantID, "assignment.partner_offered", NewID(),
		map[string]any{"partner_id": p.ID, "booking_id": "b1"})
	// Event for a different partner → tenant-wide feed only.
	s.Webhooks.FanOut(ctx, tenantID, "assignment.partner_offered", NewID(),
		map[string]any{"partner_id": "someone-else", "booking_id": "b2"})
	// Non-matching kind for the partner sub's patterns → tenant-wide only.
	s.Webhooks.FanOut(ctx, tenantID, "quote.created", NewID(),
		map[string]any{"partner_id": p.ID})

	dels, err := s.Webhooks.ListDeliveries(ctx, tenantID, 50)
	if err != nil {
		t.Fatal(err)
	}
	subs, _ := s.Webhooks.ListSubscriptions(ctx, tenantID)
	var partnerSubID string
	for _, sub := range subs {
		if sub.PartnerID == p.ID {
			partnerSubID = sub.ID
		}
	}
	if partnerSubID == "" {
		t.Fatal("partner subscription not persisted")
	}
	var tenantWide, partnerFeed int
	for _, d := range dels {
		if d.SubscriptionID == partnerSubID {
			partnerFeed++
			if d.EventKind != "assignment.partner_offered" {
				t.Fatalf("partner feed received %s", d.EventKind)
			}
		} else {
			tenantWide++
		}
	}
	if tenantWide != 3 || partnerFeed != 1 {
		t.Fatalf("deliveries: tenant-wide=%d partner=%d (want 3/1)", tenantWide, partnerFeed)
	}
}

func TestPartnerKeysAndCommission56(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	p := seedPartner56(t, s, tenantID, "Mr Travel", 0)

	// Partner key mint: scope forced to "partner", bound to the partner.
	k, secret, err := s.ApiKeys.CreatePartnerKey(ctx, tenantID, p.ID, "mr-travel-sync")
	if err != nil {
		t.Fatal(err)
	}
	if k.PartnerID != p.ID || len(k.Scopes) != 1 || k.Scopes[0] != "partner" {
		t.Fatalf("key: %+v", k)
	}
	tid, scopes, pid, err := s.ApiKeys.AuthenticateTenant(ctx, "Bearer "+secret)
	if err != nil || tid != tenantID || pid != p.ID || !HasScope(scopes, "partner") {
		t.Fatalf("auth: %v %s %v %v", tid, pid, scopes, err)
	}
	// Tenant-wide keys keep working through the same authenticator.
	tk, tsecret, err := s.ApiKeys.Create(ctx, tenantID, "ops", []string{"read", "write"})
	if err != nil {
		t.Fatal(err)
	}
	if tk.PartnerID != "" {
		t.Fatal("tenant key must not be partner-bound")
	}
	if _, _, pid, err := s.ApiKeys.AuthenticateTenant(ctx, "Bearer "+tsecret); err != nil || pid != "" {
		t.Fatalf("tenant key auth: %q %v", pid, err)
	}
	// Unknown partner rejected at mint.
	if _, _, err := s.ApiKeys.CreatePartnerKey(ctx, tenantID, "no-such-partner", "x"); err == nil {
		t.Fatal("expected error for unknown partner")
	}

	// Commission rate validation.
	if _, err := s.PartnerOps.SetCommissionPct(ctx, tenantID, p.ID, 15); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PartnerOps.SetCommissionPct(ctx, tenantID, p.ID, 51); err != httpx.ErrValidation {
		t.Fatalf("pct>50: %v", err)
	}
	if _, err := s.PartnerOps.SetCommissionPct(ctx, tenantID, p.ID, -1); err != httpx.ErrValidation {
		t.Fatalf("pct<0: %v", err)
	}
}
