//go:build pg

// Postgres integration tests — Phase 28: site_config roundtrip + non-wipe,
// GetByDomain jsonb containment lookup. Same gate as pg_integration_test.go.
package store

import (
	"context"
	"testing"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPGSiteConfig(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-sitecfg")

	tn, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	tn.SiteConfig = model.SiteConfig{
		CustomDomains:      []string{"concierge.pgtest.example"},
		GoogleVerification: "goog-abc",
		Locale:             "en-GB",
		SocialLinks:        []string{"https://instagram.com/pgtest"},
	}
	if err := s.Tenants().UpdateConfig(ctx, tn); err != nil {
		t.Fatalf("update: %v", err)
	}
	back, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("reget: %v", err)
	}
	if len(back.SiteConfig.CustomDomains) != 1 || back.SiteConfig.Locale != "en-GB" ||
		back.SiteConfig.GoogleVerification != "goog-abc" || len(back.SiteConfig.SocialLinks) != 1 {
		t.Fatalf("site_config roundtrip: %+v", back.SiteConfig)
	}

	// Unrelated config patch does not wipe it.
	back.AcquisitionConfig = model.AcquisitionConfig{GoalClients: 40}
	if err := s.Tenants().UpdateConfig(ctx, back); err != nil {
		t.Fatalf("update2: %v", err)
	}
	back2, _ := s.Tenants().Get(ctx, tenant)
	if len(back2.SiteConfig.CustomDomains) != 1 || back2.AcquisitionConfig.GoalClients != 40 {
		t.Fatalf("cross-config wipe: %+v / %+v", back2.SiteConfig, back2.AcquisitionConfig)
	}

	// GetByDomain: jsonb array containment — positive and negative.
	byDomain, err := s.Tenants().GetByDomain(ctx, "concierge.pgtest.example")
	if err != nil || byDomain.ID != tenant {
		t.Fatalf("GetByDomain: %v %+v", err, byDomain)
	}
	if _, err := s.Tenants().GetByDomain(ctx, "nobody.example"); err != httpx.ErrNotFound {
		t.Fatalf("GetByDomain unknown host: want ErrNotFound, got %v", err)
	}

	// GetBySlug and List carry site_config (site surface resolves by slug).
	bySlug, err := s.Tenants().GetBySlug(ctx, "pg-sitecfg")
	if err != nil || bySlug.SiteConfig.Locale != "en-GB" {
		t.Fatalf("GetBySlug lost site_config: %v %+v", err, bySlug.SiteConfig)
	}
	all, err := s.Tenants().List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, lt := range all {
		if lt.ID == tenant {
			found = len(lt.SiteConfig.CustomDomains) == 1
		}
	}
	if !found {
		t.Fatal("List lost site_config")
	}
}
