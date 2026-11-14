// Phase 19 domain tests: reward sharing program — two-sided accrual
// (referrer + referee welcome credit), per-tenant caps, disabled programs,
// expiry sweeps, and the client-facing reward summary.
package domain

import (
	"context"
	"math"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func setRewardProgram(t *testing.T, s *Services, tenantID string, prog model.RewardProgram) {
	t.Helper()
	ctx := context.Background()
	tenant, err := s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	tenant.RewardProgram = prog
	if err := s.Store.Tenants().UpdateConfig(ctx, tenant); err != nil {
		t.Fatal(err)
	}
}

// referredPair wires a referee to a referrer's code and returns both ids.
func referredPair(t *testing.T, s *Services, tenantID, refPhone, refdPhone string) (referrerID, refereeID string) {
	t.Helper()
	referrerID = outreachClient(t, s, tenantID, refPhone, false)
	refereeID = outreachClient(t, s, tenantID, refdPhone, false)
	s.Events.Track(context.Background(), &model.AttributionEvent{
		TenantID: tenantID, Kind: "page_view", Channel: "referral",
		Campaign: "ref:" + model.ClientReferralCode(referrerID), ClientID: refereeID,
	})
	return referrerID, refereeID
}

func TestRefereeWelcomeCredit(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	referrerID, refereeID := referredPair(t, s, tenantID, "+971500000020", "+971500000021")
	book := rewardSetup(t, s, tenantID)
	setRewardProgram(t, s, tenantID, model.RewardProgram{RefereePct: 0.10})

	q := book(refereeID, "p19-book-1")
	wantReferrer := int64(math.Round(float64(q.TotalMinor) * 0.05))
	wantReferee := int64(math.Round(float64(q.TotalMinor) * 0.10))
	if bal := rewardBalance(t, s, tenantID, referrerID); bal != wantReferrer {
		t.Fatalf("referrer balance: want %d, got %d", wantReferrer, bal)
	}
	if bal := rewardBalance(t, s, tenantID, refereeID); bal != wantReferee {
		t.Fatalf("referee welcome credit: want %d (10%% of %d), got %d", wantReferee, q.TotalMinor, bal)
	}

	// The welcome credit applies to the referee's own next payment link.
	q2 := book(refereeID, "p19-book-2")
	intent, err := s.Payments.CreatePaymentLinkV2(ctx, tenantID, CreatePaymentLinkInput{
		QuoteID: q2.ID, Mode: "FULL", IdempotencyKey: "p19-pay-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if intent.RewardAppliedMinor != wantReferee {
		t.Fatalf("welcome credit applied: want %d, got %d", wantReferee, intent.RewardAppliedMinor)
	}

	// Kinds are distinguishable in the history.
	summary, err := s.RewardSummary(ctx, tenantID, refereeID)
	if err != nil {
		t.Fatal(err)
	}
	rewards := summary["rewards"].([]model.ClientReward)
	if len(rewards) != 1 || rewards[0].Kind != "referee_welcome" {
		t.Fatalf("referee history: %+v", rewards)
	}
	if summary["balance_minor"].(int64) != wantReferee {
		t.Fatalf("summary balance: %v", summary["balance_minor"])
	}
}

func TestRewardProgramCap(t *testing.T) {
	s := testServices(t, Deps{})
	tenantID := seedTenant(t, s)
	referrerID, refereeID := referredPair(t, s, tenantID, "+971500000022", "+971500000023")
	book := rewardSetup(t, s, tenantID)
	setRewardProgram(t, s, tenantID, model.RewardProgram{
		ReferrerPct: 0.50, MaxRewardMinor: 300,
	})

	book(refereeID, "p19-cap-1")
	// 50% of a ~10k booking would be ~5000 — the cap holds it to 300.
	if bal := rewardBalance(t, s, tenantID, referrerID); bal != 300 {
		t.Fatalf("capped reward: want 300, got %d", bal)
	}
}

func TestRewardProgramDisabled(t *testing.T) {
	s := testServices(t, Deps{})
	tenantID := seedTenant(t, s)
	referrerID, refereeID := referredPair(t, s, tenantID, "+971500000024", "+971500000025")
	book := rewardSetup(t, s, tenantID)
	setRewardProgram(t, s, tenantID, model.RewardProgram{Disabled: true, RefereePct: 0.10})

	book(refereeID, "p19-off-1")
	if bal := rewardBalance(t, s, tenantID, referrerID); bal != 0 {
		t.Fatalf("disabled program must not credit referrer: %d", bal)
	}
	if bal := rewardBalance(t, s, tenantID, refereeID); bal != 0 {
		t.Fatalf("disabled program must not credit referee: %d", bal)
	}
}

func TestRewardExpirySweep(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000026", false)
	rw := s.Store.Rewards()

	expired := &model.ClientReward{
		ID: NewID(), TenantID: tenantID, ClientID: clientID, SourceBookingID: NewID(),
		Kind: "referral_reward", AmountMinor: 500, Currency: "EUR",
		State: model.RewardAccrued, CreatedAt: time.Now().Add(-48 * time.Hour).UTC(),
		ExpiresAt: time.Now().Add(-24 * time.Hour).UTC(), // lapsed yesterday
	}
	if err := rw.Accrue(ctx, expired); err != nil {
		t.Fatal(err)
	}
	live := &model.ClientReward{
		ID: NewID(), TenantID: tenantID, ClientID: clientID, SourceBookingID: NewID(),
		Kind: "referral_reward", AmountMinor: 200, Currency: "EUR",
		State: model.RewardAccrued, CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().Add(24 * time.Hour).UTC(),
	}
	if err := rw.Accrue(ctx, live); err != nil {
		t.Fatal(err)
	}

	// Balance is expiry-aware: the lapsed reward doesn't count.
	if bal := rewardBalance(t, s, tenantID, clientID); bal != 200 {
		t.Fatalf("expiry-aware balance: want 200, got %d", bal)
	}
	// The sweep marked it EXPIRED — visible in history, never redeemable.
	rewards, err := rw.List(ctx, tenantID, clientID)
	if err != nil || len(rewards) != 2 {
		t.Fatalf("list: %+v %v", rewards, err)
	}
	states := map[string]string{}
	for _, r := range rewards {
		states[r.ID] = r.State
	}
	if states[expired.ID] != model.RewardExpired || states[live.ID] != model.RewardAccrued {
		t.Fatalf("sweep states: %+v", states)
	}
	// Redeem skips the expired reward entirely.
	covered, err := rw.Redeem(ctx, tenantID, clientID, "intent-exp", 10000)
	if err != nil || covered != 200 {
		t.Fatalf("redeem: want 200, got %d (%v)", covered, err)
	}
}

func TestRewardSummaryEmpty(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000027", false)

	summary, err := s.RewardSummary(ctx, tenantID, clientID)
	if err != nil {
		t.Fatal(err)
	}
	if summary["balance_minor"].(int64) != 0 {
		t.Fatalf("empty balance: %v", summary["balance_minor"])
	}
	if rewards := summary["rewards"].([]model.ClientReward); len(rewards) != 0 {
		t.Fatalf("empty history: %+v", rewards)
	}
}
