//go:build pg

// Postgres integration tests — Phase 19: tenant reward_program config
// roundtrip, reward expiry sweep (lazy EXPIRED marking), and reward history
// list. Same gate as pg_integration_test.go (CE_PG_TEST_DSN).
package store

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGRewardProgramConfig(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-reward-prog")

	tn, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("tenant get: %v", err)
	}
	if tn.RewardProgram.RefereePct != 0 || tn.RewardProgram.Disabled {
		t.Fatalf("default program must be zero-value: %+v", tn.RewardProgram)
	}
	tn.RewardProgram = model.RewardProgram{
		ReferrerPct: 0.07, RefereePct: 0.03, MaxRewardMinor: 5000, ExpiryDays: 90,
	}
	if err := s.Tenants().UpdateConfig(ctx, tn); err != nil {
		t.Fatalf("tenant update: %v", err)
	}
	tn, err = s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("tenant reget: %v", err)
	}
	if tn.RewardProgram.ReferrerPct != 0.07 || tn.RewardProgram.RefereePct != 0.03 ||
		tn.RewardProgram.MaxRewardMinor != 5000 || tn.RewardProgram.ExpiryDays != 90 {
		t.Fatalf("reward_program roundtrip: %+v", tn.RewardProgram)
	}
}

func TestPGRewardExpiryAndList(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-reward-exp")

	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Expiry Client",
		KYCStatus: "none", Consent: map[string]any{}}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatalf("client: %v", err)
	}
	bookingA := pgQuoteBornBooking(t, s, tenant, cl.ID, "pg-exp-r1")
	bookingB := pgQuoteBornBooking(t, s, tenant, cl.ID, "pg-exp-r2")

	rw := s.Rewards()
	// Lapsed credit: expires_at in the past.
	lapsed := &model.ClientReward{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		SourceBookingID: bookingA, Kind: "referee_welcome", AmountMinor: 400, Currency: "EUR",
		State: model.RewardAccrued, CreatedAt: time.Now().Add(-48 * time.Hour).UTC(),
		ExpiresAt: time.Now().Add(-24 * time.Hour).UTC()}
	if err := rw.Accrue(ctx, lapsed); err != nil {
		t.Fatalf("accrue lapsed: %v", err)
	}
	// Live credit: expires tomorrow.
	live := &model.ClientReward{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		SourceBookingID: bookingB, Kind: "referral_reward", AmountMinor: 600, Currency: "EUR",
		State: model.RewardAccrued, CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().Add(24 * time.Hour).UTC()}
	if err := rw.Accrue(ctx, live); err != nil {
		t.Fatalf("accrue live: %v", err)
	}

	// Balance sweeps and excludes the lapsed credit.
	bal, err := rw.Balance(ctx, tenant, cl.ID)
	if err != nil || bal != 600 {
		t.Fatalf("expiry-aware balance: want 600, got %d (%v)", bal, err)
	}
	// History shows the sweep: EXPIRED vs ACCRUED, expires_at roundtripped.
	rewards, err := rw.List(ctx, tenant, cl.ID)
	if err != nil || len(rewards) != 2 {
		t.Fatalf("list: %+v %v", rewards, err)
	}
	states := map[string]model.ClientReward{}
	for _, r := range rewards {
		states[r.ID] = r
	}
	if states[lapsed.ID].State != model.RewardExpired {
		t.Fatalf("lapsed reward not swept: %+v", states[lapsed.ID])
	}
	if states[live.ID].State != model.RewardAccrued || states[live.ID].ExpiresAt.IsZero() {
		t.Fatalf("live reward: %+v", states[live.ID])
	}
	// Redeem skips the expired reward.
	covered, err := rw.Redeem(ctx, tenant, cl.ID, "intent-pg-exp", 10000)
	if err != nil || covered != 600 {
		t.Fatalf("redeem: want 600, got %d (%v)", covered, err)
	}
}
