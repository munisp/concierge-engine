// Phase 20 domain tests: promo-code campaigns (create/redeem, once per
// client, caps, expiry, validation, credit applies to payment links) and
// per-tenant marketing frequency-cap overrides.
package domain

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPromoRedeemAppliesCredit(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000030", false)
	book := rewardSetup(t, s, tenantID)

	c, err := s.CreatePromo(ctx, tenantID, CreatePromoInput{
		Code: "summer25", CreditMinor: 2500, Currency: "EUR",
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Code != "SUMMER25" {
		t.Fatalf("code normalized uppercase: %q", c.Code)
	}

	// Case-insensitive redemption.
	rw, err := s.RedeemPromo(ctx, tenantID, "summer25", clientID)
	if err != nil {
		t.Fatal(err)
	}
	if rw.Kind != "promo_credit" || rw.PromoCode != "SUMMER25" || rw.AmountMinor != 2500 {
		t.Fatalf("reward: %+v", rw)
	}
	if bal := rewardBalance(t, s, tenantID, clientID); bal != 2500 {
		t.Fatalf("balance: want 2500, got %d", bal)
	}

	// The credit comes off the client's next payment link.
	q := book(clientID, "p20-book-1")
	intent, err := s.Payments.CreatePaymentLinkV2(ctx, tenantID, CreatePaymentLinkInput{
		QuoteID: q.ID, Mode: "FULL", IdempotencyKey: "p20-pay-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if intent.RewardAppliedMinor != 2500 {
		t.Fatalf("promo credit applied: want 2500, got %d", intent.RewardAppliedMinor)
	}
}

func TestPromoOncePerClient(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000031", false)

	if _, err := s.CreatePromo(ctx, tenantID, CreatePromoInput{
		Code: "ONCE", CreditMinor: 1000, Currency: "EUR",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemPromo(ctx, tenantID, "ONCE", clientID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemPromo(ctx, tenantID, "ONCE", clientID); err != httpx.ErrConflict {
		t.Fatalf("second redemption must conflict, got %v", err)
	}
	if bal := rewardBalance(t, s, tenantID, clientID); bal != 1000 {
		t.Fatalf("double redemption credited: %d", bal)
	}
}

func TestPromoCapAndExpiry(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientA := outreachClient(t, s, tenantID, "+971500000032", false)
	clientB := outreachClient(t, s, tenantID, "+971500000033", false)

	// Cap: one redemption total.
	if _, err := s.CreatePromo(ctx, tenantID, CreatePromoInput{
		Code: "FIRST1", CreditMinor: 500, Currency: "EUR", MaxRedemptions: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemPromo(ctx, tenantID, "FIRST1", clientA); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemPromo(ctx, tenantID, "FIRST1", clientB); err != httpx.ErrConflict {
		t.Fatalf("exhausted campaign must conflict, got %v", err)
	}

	// Expired campaign: store-level backdate, domain must refuse.
	past := &model.PromoCampaign{
		ID: NewID(), TenantID: tenantID, Code: "OLD", CreditMinor: 500,
		Currency: "EUR", Active: true, CreatedAt: time.Now().Add(-72 * time.Hour).UTC(),
		ExpiresAt: time.Now().Add(-24 * time.Hour).UTC(),
	}
	if err := s.Store.Promos().Create(ctx, past); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemPromo(ctx, tenantID, "OLD", clientA); err != httpx.ErrConflict {
		t.Fatalf("expired campaign must conflict, got %v", err)
	}

	// Unknown code: not found.
	if _, err := s.RedeemPromo(ctx, tenantID, "NOPE", clientA); err != httpx.ErrNotFound {
		t.Fatalf("unknown code must 404, got %v", err)
	}
}

func TestPromoValidation(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	for _, in := range []CreatePromoInput{
		{Code: "X", CreditMinor: 500, Currency: "EUR"},          // too short
		{Code: "VALID", CreditMinor: 0, Currency: "EUR"},        // zero credit
		{Code: "VALID", CreditMinor: 500, Currency: "XX"},       // bad currency
	} {
		if _, err := s.CreatePromo(ctx, tenantID, in); err != httpx.ErrValidation {
			t.Fatalf("want validation for %+v, got %v", in, err)
		}
	}
	// Idempotent re-create returns the existing campaign.
	if _, err := s.CreatePromo(ctx, tenantID, CreatePromoInput{
		Code: "DUPE", CreditMinor: 500, Currency: "EUR",
	}); err != nil {
		t.Fatal(err)
	}
	again, err := s.CreatePromo(ctx, tenantID, CreatePromoInput{
		Code: "dupe", CreditMinor: 500, Currency: "EUR",
	})
	if err != nil || again.Code != "DUPE" {
		t.Fatalf("idempotent create: %+v %v", again, err)
	}
	promos, err := s.ListPromos(ctx, tenantID)
	if err != nil || len(promos) != 1 {
		t.Fatalf("list: %+v %v", promos, err)
	}
}

func TestMarketingCapOverride(t *testing.T) {
	calls := 0
	s := testServices(t, Deps{WhatsAppURL: waCountingStub(t, &calls).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000034", true)

	// Tenant lowers the weekly marketing cap to 1.
	tenant, err := s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	tenant.OutreachConfig.MarketingCap7d = 1
	if err := s.Store.Tenants().UpdateConfig(ctx, tenant); err != nil {
		t.Fatal(err)
	}

	v, err := s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing)
	if err != nil || !v.Allowed || v.MarketingCap != 1 {
		t.Fatalf("verdict with override: %+v %v", v, err)
	}
	if _, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachMarketing,
		Body: "offer", IdempotencyKey: "cap-ovr-1",
	}); err != nil {
		t.Fatal(err)
	}
	// Second marketing send is held at the tenant's cap of 1.
	v, err = s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing)
	if err != nil || v.Allowed {
		t.Fatalf("cap override not enforced: %+v %v", v, err)
	}
	if calls != 1 {
		t.Fatalf("adapter called %d times, want 1", calls)
	}
}
