//go:build pg

// Postgres integration tests — Phase 26: tenant acquisition_config
// roundtrip through Get / GetBySlug / List / UpdateConfig, and non-wipe by
// an unrelated config update. Same gate as pg_integration_test.go.
package store

import (
	"context"
	"testing"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGAcquisitionConfig(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-acqcfg")

	tn, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	tn.AcquisitionConfig = model.AcquisitionConfig{
		FeePct: 0.12, Channels: []string{"seo", "partner"},
		GoalClients: 50, GoalDays: 60, CohortStartedAt: "2026-11-01T00:00:00Z",
	}
	if err := s.Tenants().UpdateConfig(ctx, tn); err != nil {
		t.Fatalf("update: %v", err)
	}

	back, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("reget: %v", err)
	}
	ac := back.AcquisitionConfig
	if ac.FeePct != 0.12 || ac.GoalClients != 50 || ac.GoalDays != 60 ||
		ac.CohortStartedAt != "2026-11-01T00:00:00Z" ||
		len(ac.Channels) != 2 || ac.Channels[1] != "partner" {
		t.Fatalf("acquisition_config roundtrip: %+v", ac)
	}

	// GetBySlug carries it too (the SEO site resolves tenants by slug).
	bySlug, err := s.Tenants().GetBySlug(ctx, "pg-acqcfg")
	if err != nil {
		t.Fatalf("getBySlug: %v", err)
	}
	if bySlug.AcquisitionConfig.GoalClients != 50 {
		t.Fatalf("GetBySlug lost acquisition_config: %+v", bySlug.AcquisitionConfig)
	}

	// List (the sweeper's path) carries it.
	tenants, err := s.Tenants().List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, row := range tenants {
		if row.ID == tenant {
			found = true
			if row.AcquisitionConfig.FeePct != 0.12 {
				t.Fatalf("List lost acquisition_config: %+v", row.AcquisitionConfig)
			}
		}
	}
	if !found {
		t.Fatal("tenant missing from List")
	}

	// A lifecycle-only update must not wipe acquisition config (and vice
	// versa is covered by the lifecycle test).
	tn2, _ := s.Tenants().Get(ctx, tenant)
	tn2.LifecycleConfig = model.LifecycleConfig{PostTripHours: 48}
	if err := s.Tenants().UpdateConfig(ctx, tn2); err != nil {
		t.Fatalf("update2: %v", err)
	}
	back2, _ := s.Tenants().Get(ctx, tenant)
	if back2.AcquisitionConfig.GoalClients != 50 {
		t.Fatalf("acquisition_config wiped by lifecycle update: %+v", back2.AcquisitionConfig)
	}
	if back2.LifecycleConfig.PostTripHours != 48 {
		t.Fatalf("lifecycle update lost: %+v", back2.LifecycleConfig)
	}
}
