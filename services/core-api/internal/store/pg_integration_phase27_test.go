//go:build pg

// Postgres integration tests — Phase 27: onboarding_config roundtrip +
// non-wipe, promo gift/switch_offer flags roundtrip, reviews store (one
// review per assignment, consented subset, RLS isolation). Same gate as
// pg_integration_test.go.
package store

import (
	"context"
	"testing"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPGOnboardingConfig(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-onboard")

	tn, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	tn.OnboardingConfig = model.OnboardingConfig{Track: "existing", Skipped: []string{"team", "areas"}}
	if err := s.Tenants().UpdateConfig(ctx, tn); err != nil {
		t.Fatalf("update: %v", err)
	}
	back, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("reget: %v", err)
	}
	if back.OnboardingConfig.Track != "existing" || len(back.OnboardingConfig.Skipped) != 2 {
		t.Fatalf("onboarding_config roundtrip: %+v", back.OnboardingConfig)
	}
	// Unrelated config patch does not wipe it.
	back.AcquisitionConfig = model.AcquisitionConfig{GoalClients: 25}
	if err := s.Tenants().UpdateConfig(ctx, back); err != nil {
		t.Fatalf("update2: %v", err)
	}
	back2, _ := s.Tenants().Get(ctx, tenant)
	if back2.OnboardingConfig.Track != "existing" || back2.AcquisitionConfig.GoalClients != 25 {
		t.Fatalf("cross-config wipe: %+v / %+v", back2.OnboardingConfig, back2.AcquisitionConfig)
	}
	// GetBySlug carries it (the onboarding wizard resolves by slug links).
	bySlug, err := s.Tenants().GetBySlug(ctx, "pg-onboard")
	if err != nil || bySlug.OnboardingConfig.Track != "existing" {
		t.Fatalf("GetBySlug lost onboarding_config: %v %+v", err, bySlug.OnboardingConfig)
	}
}

func TestPGPromoFlags(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-promoflags")

	c := &model.PromoCampaign{
		ID: newTestUUID(), TenantID: tenant, Code: "GIFT-X1", CreditMinor: 5000,
		Currency: "EUR", MaxRedemptions: 1, Active: true, Gift: true, SwitchOffer: true,
	}
	if err := s.Promos().Create(ctx, c); err != nil {
		t.Fatalf("create: %v", err)
	}
	back, err := s.Promos().GetByCode(ctx, tenant, "gift-x1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !back.Gift || !back.SwitchOffer || back.MaxRedemptions != 1 {
		t.Fatalf("flags roundtrip: %+v", back)
	}
	list, err := s.Promos().List(ctx, tenant)
	if err != nil || len(list) != 1 || !list[0].Gift || !list[0].SwitchOffer {
		t.Fatalf("list lost flags: %v %+v", err, list)
	}
}

func TestPGReviews(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenantA := pgTenant(t, s, "pg-rev-a")
	tenantB := pgTenant(t, s, "pg-rev-b")
	client := newTestUUID()
	if err := s.Clients().Upsert(ctx, &model.Client{
		ID: client, TenantID: tenantA, FullName: "Review Client",
		Phone: "+31699990001", KYCStatus: "none", Consent: map[string]any{},
	}); err != nil {
		t.Fatalf("client: %v", err)
	}

	r := &model.Review{
		ID: newTestUUID(), TenantID: tenantA, ClientID: client, AssignmentID: newTestUUID(),
		Rating: 5, Text: "Flawless.", PublishConsent: true,
	}
	if err := s.Reviews().Create(ctx, r); err != nil {
		t.Fatalf("create: %v", err)
	}
	// One review per assignment — replay conflicts.
	dup := *r
	dup.ID = newTestUUID()
	if err := s.Reviews().Create(ctx, &dup); err != httpx.ErrConflict {
		t.Fatalf("dup assignment: want ErrConflict, got %v", err)
	}
	// Non-consented review exists but stays off the public aggregate.
	r2 := &model.Review{
		ID: newTestUUID(), TenantID: tenantA, ClientID: client, AssignmentID: newTestUUID(),
		Rating: 2, Text: "Not great.", PublishConsent: false,
	}
	if err := s.Reviews().Create(ctx, r2); err != nil {
		t.Fatalf("create2: %v", err)
	}
	consented, err := s.Reviews().Consented(ctx, tenantA)
	if err != nil || len(consented) != 1 || consented[0].Rating != 5 {
		t.Fatalf("consented: %v %+v", err, consented)
	}
	all, err := s.Reviews().ListAll(ctx, tenantA, 10)
	if err != nil || len(all) != 2 {
		t.Fatalf("listAll: %v %d", err, len(all))
	}
	got, err := s.Reviews().GetByAssignment(ctx, tenantA, r.AssignmentID)
	if err != nil || got.Rating != 5 {
		t.Fatalf("getByAssignment: %v %+v", err, got)
	}
	// RLS: tenant B sees nothing of tenant A's reviews.
	allB, err := s.Reviews().ListAll(ctx, tenantB, 10)
	if err != nil || len(allB) != 0 {
		t.Fatalf("RLS leak: %v %d", err, len(allB))
	}
	consentedB, _ := s.Reviews().Consented(ctx, tenantB)
	if len(consentedB) != 0 {
		t.Fatalf("RLS leak (consented): %d", len(consentedB))
	}
}
