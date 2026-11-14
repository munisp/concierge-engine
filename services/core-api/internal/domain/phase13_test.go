// Phase 18 domain tests: referrer ledger rewards — accrual on the referee's
// first booking (default 5%, tenant-overridable), self-referral exclusion,
// reward application at payment-link creation, redemption at capture with
// the platform compensation posting, and refund returning the credit.
package domain

import (
	"context"
	"math"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// rewardSetup seeds a tenant with an auto-quotable catalog and returns a
// book() closure that confirms a booking for the given client.
func rewardSetup(t *testing.T, s *Services, tenantID string) func(clientID, idem string) *model.Quote {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Catalog.Add(ctx, &model.CatalogService{
		TenantID: tenantID, Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Sedan|business",
		BaseMinor: 9000, Currency: "EUR", PerKMMinor: 250, AutoQuotable: true, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	return func(clientID, idem string) *model.Quote {
		t.Helper()
		sr, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
			TenantID: tenantID, ClientID: clientID, IdempotencyKey: idem,
			Items: []model.ServiceItem{{
				Vertical: "CHAUFFEUR_POINT_TO_POINT", PickupAddress: "A", DropoffAddress: "B",
				PickupLat: 52.37, PickupLng: 4.90, DropoffLat: 52.31, DropoffLng: 4.76,
				PickupAt: 1733500000, Passengers: 1,
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		q, instant, err := s.Booking.QuoteRequest(ctx, tenantID, sr.ID)
		if err != nil || !instant {
			t.Fatalf("quote: %v instant=%v", err, instant)
		}
		if _, err := s.Booking.Confirm(ctx, tenantID, q.ID); err != nil {
			t.Fatal(err)
		}
		return q
	}
}

func rewardBalance(t *testing.T, s *Services, tenantID, clientID string) int64 {
	t.Helper()
	bal, err := s.Store.Rewards().Balance(context.Background(), tenantID, clientID)
	if err != nil {
		t.Fatal(err)
	}
	return bal
}

func outboxHas(t *testing.T, s *Services, eventType string) int {
	t.Helper()
	events, err := s.Store.Outbox().Unpublished(context.Background(), 500)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range events {
		if e.EventType == eventType {
			n++
		}
	}
	return n
}

func TestReferralRewardAccrual(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	referrerID := outreachClient(t, s, tenantID, "+971500000009", false)
	refereeID := outreachClient(t, s, tenantID, "+971500000010", false)
	code := model.ClientReferralCode(referrerID)
	book := rewardSetup(t, s, tenantID)

	s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "page_view", Channel: "referral",
		Campaign: "ref:" + code, ClientID: refereeID,
	})

	q := book(refereeID, "rw-book-1")
	want := int64(math.Round(float64(q.TotalMinor) * 0.05))
	if want <= 0 {
		t.Fatalf("expected positive reward on quote total %d", q.TotalMinor)
	}
	if bal := rewardBalance(t, s, tenantID, referrerID); bal != want {
		t.Fatalf("referrer balance: want %d (5%% of %d), got %d", want, q.TotalMinor, bal)
	}
	if n := outboxHas(t, s, "referral.reward_accrued"); n != 1 {
		t.Fatalf("want 1 referral.reward_accrued event, got %d", n)
	}

	// A second referee booking must not double-credit the referrer.
	book(refereeID, "rw-book-2")
	if bal := rewardBalance(t, s, tenantID, referrerID); bal != want {
		t.Fatalf("double accrual: balance %d, want %d", bal, want)
	}
	if n := outboxHas(t, s, "referral.reward_accrued"); n != 1 {
		t.Fatalf("double accrual event: got %d", n)
	}
	// The referee earns nothing from their own booking.
	if bal := rewardBalance(t, s, tenantID, refereeID); bal != 0 {
		t.Fatalf("referee balance: want 0, got %d", bal)
	}
}

func TestReferralRewardSelfReferralExcluded(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000011", false)
	code := model.ClientReferralCode(clientID)
	book := rewardSetup(t, s, tenantID)

	s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "page_view", Channel: "referral",
		Campaign: "ref:" + code, ClientID: clientID, // own code — self-referral
	})
	book(clientID, "rw-self-1")
	if bal := rewardBalance(t, s, tenantID, clientID); bal != 0 {
		t.Fatalf("self-referral must not accrue: balance %d", bal)
	}
	if n := outboxHas(t, s, "referral.reward_accrued"); n != 0 {
		t.Fatalf("self-referral emitted reward event")
	}
}

func TestReferralRewardTenantPctOverride(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	referrerID := outreachClient(t, s, tenantID, "+971500000012", false)
	refereeID := outreachClient(t, s, tenantID, "+971500000013", false)
	code := model.ClientReferralCode(referrerID)
	book := rewardSetup(t, s, tenantID)

	tenant, err := s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	tenant.PricingRules.ReferralRewardPct = 0.10
	if err := s.Store.Tenants().UpdateConfig(ctx, tenant); err != nil {
		t.Fatal(err)
	}

	s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "page_view", Channel: "referral",
		Campaign: "ref:" + code, ClientID: refereeID,
	})
	q := book(refereeID, "rw-ovr-1")
	want := int64(math.Round(float64(q.TotalMinor) * 0.10))
	if bal := rewardBalance(t, s, tenantID, referrerID); bal != want {
		t.Fatalf("override balance: want %d (10%% of %d), got %d", want, q.TotalMinor, bal)
	}
}

func TestRewardAppliedRedeemedAndRefunded(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	referrerID := outreachClient(t, s, tenantID, "+971500000014", false)
	refereeID := outreachClient(t, s, tenantID, "+971500000015", false)
	code := model.ClientReferralCode(referrerID)
	book := rewardSetup(t, s, tenantID)

	s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "page_view", Channel: "referral",
		Campaign: "ref:" + code, ClientID: refereeID,
	})
	q1 := book(refereeID, "rw-life-1")
	reward := int64(math.Round(float64(q1.TotalMinor) * 0.05))
	if bal := rewardBalance(t, s, tenantID, referrerID); bal != reward {
		t.Fatalf("accrual: want %d, got %d", reward, bal)
	}

	// The referrer books their own trip; the payment link applies the credit.
	q2 := book(referrerID, "rw-life-2")
	intent, err := s.Payments.CreatePaymentLinkV2(ctx, tenantID, CreatePaymentLinkInput{
		QuoteID: q2.ID, Mode: "FULL", IdempotencyKey: "rw-pay-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if intent.RewardAppliedMinor != reward {
		t.Fatalf("reward applied: want %d, got %d (intent %+v)", reward, intent.RewardAppliedMinor, intent)
	}
	if intent.AmountMinor != q2.TotalMinor {
		t.Fatalf("intent must carry the FULL obligation: want %d, got %d", q2.TotalMinor, intent.AmountMinor)
	}

	// Capture redeems the credit and posts the platform compensation.
	if err := s.Payments.HandlePSPWebhook(ctx, tenantID, intent.ID, "payment.captured", "ref-rw-1"); err != nil {
		t.Fatal(err)
	}
	if bal := rewardBalance(t, s, tenantID, referrerID); bal != 0 {
		t.Fatalf("post-capture balance: want 0, got %d", bal)
	}
	postings, err := s.Payments.Postings(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	compensation := int64(0)
	for _, p := range postings {
		if p.DebitCode == "PLATFORM_FEES" && p.CreditCode == "CLIENT_CLEARING" {
			compensation += p.AmountMinor
		}
	}
	if compensation != reward {
		t.Fatalf("compensation posting: want %d PLATFORM_FEES->CLIENT_CLEARING, got %d", reward, compensation)
	}
	if n := outboxHas(t, s, "referral.reward_redeemed"); n != 1 {
		t.Fatalf("want 1 referral.reward_redeemed event, got %d", n)
	}

	// A refund never burns referral credit: rewards return to ACCRUED and the
	// compensation is clawed back.
	if err := s.Payments.HandlePSPWebhook(ctx, tenantID, intent.ID, "charge.refunded", "ref-rw-1"); err != nil {
		t.Fatal(err)
	}
	if bal := rewardBalance(t, s, tenantID, referrerID); bal != reward {
		t.Fatalf("post-refund balance: want %d restored, got %d", reward, bal)
	}
	postings, err = s.Payments.Postings(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	clawback := int64(0)
	for _, p := range postings {
		if p.DebitCode == "CLIENT_CLEARING" && p.CreditCode == "PLATFORM_FEES" && p.AmountMinor == reward {
			clawback += p.AmountMinor
		}
	}
	if clawback != reward {
		t.Fatalf("clawback posting: want %d CLIENT_CLEARING->PLATFORM_FEES, got %d", reward, clawback)
	}
}

func TestRewardStoreSemantics(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000016", false)
	rw := s.Store.Rewards()

	now := time.Now().UTC()
	r1 := &model.ClientReward{
		ID: NewID(), TenantID: tenantID, ClientID: clientID, SourceBookingID: NewID(),
		Kind: "referral_reward", AmountMinor: 100, Currency: "EUR",
		State: model.RewardAccrued, CreatedAt: now,
	}
	if err := rw.Accrue(ctx, r1); err != nil {
		t.Fatal(err)
	}
	// Idempotent per (tenant, client, source booking): replay conflicts.
	dup := *r1
	dup.ID = NewID()
	if err := rw.Accrue(ctx, &dup); err != httpx.ErrConflict {
		t.Fatalf("duplicate accrual: want ErrConflict, got %v", err)
	}
	r2 := &model.ClientReward{
		ID: NewID(), TenantID: tenantID, ClientID: clientID, SourceBookingID: NewID(),
		Kind: "referral_reward", AmountMinor: 200, Currency: "EUR",
		State: model.RewardAccrued, CreatedAt: now.Add(time.Second),
	}
	if err := rw.Accrue(ctx, r2); err != nil {
		t.Fatal(err)
	}
	if bal := rewardBalance(t, s, tenantID, clientID); bal != 300 {
		t.Fatalf("balance: want 300, got %d", bal)
	}

	// Oldest-first redemption: a 50 request covers the oldest 100 reward whole.
	covered, err := rw.Redeem(ctx, tenantID, clientID, "intent-a", 50)
	if err != nil || covered != 100 {
		t.Fatalf("redeem 50: covered=%d err=%v", covered, err)
	}
	if bal := rewardBalance(t, s, tenantID, clientID); bal != 200 {
		t.Fatalf("balance after first redeem: want 200, got %d", bal)
	}
	covered, err = rw.Redeem(ctx, tenantID, clientID, "intent-b", 1000)
	if err != nil || covered != 200 {
		t.Fatalf("redeem rest: covered=%d err=%v", covered, err)
	}
	if bal := rewardBalance(t, s, tenantID, clientID); bal != 0 {
		t.Fatalf("balance after second redeem: want 0, got %d", bal)
	}

	// Refunding the second intent returns only its rewards.
	if err := rw.RefundRedemption(ctx, tenantID, "intent-b"); err != nil {
		t.Fatal(err)
	}
	if bal := rewardBalance(t, s, tenantID, clientID); bal != 200 {
		t.Fatalf("balance after refund-b: want 200, got %d", bal)
	}
	if err := rw.RefundRedemption(ctx, tenantID, "intent-a"); err != nil {
		t.Fatal(err)
	}
	if bal := rewardBalance(t, s, tenantID, clientID); bal != 300 {
		t.Fatalf("balance after refund-a: want 300, got %d", bal)
	}
}
