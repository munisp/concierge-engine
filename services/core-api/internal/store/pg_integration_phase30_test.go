//go:build pg

package store

// Phase 30: site_config.theme jsonb round-trip + CatalogStore.Update
// (the write path the Site Studio's apply endpoint depends on).

import (
	"testing"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGSiteTheme30(t *testing.T) {
	s := pgTestStore(t)
	tenantID := pgTenant(t, s, "theme30")

	ten, err := s.Tenants().Get(t.Context(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if ten.SiteConfig.Theme != nil {
		t.Fatalf("fresh tenant has theme: %+v", ten.SiteConfig.Theme)
	}
	ten.SiteConfig.Theme = &model.SiteTheme{
		Preset: "custom", Primary: "#101010", Accent: "#ff0055", Font: "sans", Corners: "sharp",
	}
	if err := s.Tenants().UpdateConfig(t.Context(), ten); err != nil {
		t.Fatal(err)
	}
	got, err := s.Tenants().Get(t.Context(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	th := got.SiteConfig.Theme
	if th == nil || th.Preset != "custom" || th.Accent != "#ff0055" || th.Corners != "sharp" {
		t.Fatalf("theme lost in pg round-trip: %+v", th)
	}
	// brand_i18n from Phase 29 must survive the same update path.
	if got.SiteConfig.BrandI18n == nil && ten.SiteConfig.BrandI18n != nil {
		t.Fatal("brand_i18n wiped by theme update")
	}
}

func TestPGCatalogUpdate30(t *testing.T) {
	s := pgTestStore(t)
	tenantID := pgTenant(t, s, "catupd30")

	svc := &model.CatalogService{
		ID: newTestUUID(), TenantID: tenantID, Vertical: "PRIVATE_TOUR",
		Name: "Sintra Tour", BaseMinor: 35000, Currency: "EUR", Active: true,
	}
	if err := s.Catalog().Add(t.Context(), svc); err != nil {
		t.Fatal(err)
	}

	// The studio apply path: set a localized description, flip price.
	svc.DescI18n = map[string]string{"en": "Palaces and coast on a private full-day tour."}
	svc.BaseMinor = 39000
	if err := s.Catalog().Update(t.Context(), svc); err != nil {
		t.Fatal(err)
	}
	services, err := s.Catalog().List(t.Context(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("want 1 service, got %d", len(services))
	}
	got := services[0]
	if got.BaseMinor != 39000 || got.DescI18n["en"] == "" || got.Name != "Sintra Tour" {
		t.Fatalf("update not applied: %+v", got)
	}

	// Update of a foreign/unknown ID is a clean not-found, never a silent ok.
	ghost := &model.CatalogService{ID: newTestUUID(), TenantID: tenantID, Name: "ghost"}
	if err := s.Catalog().Update(t.Context(), ghost); err == nil {
		t.Fatal("expected not-found updating unknown service")
	}
	other := &model.CatalogService{ID: svc.ID, TenantID: newTestUUID(), Name: "stolen"}
	if err := s.Catalog().Update(t.Context(), other); err == nil {
		t.Fatal("expected not-found updating another tenant's service")
	}
	// ...and the original row is untouched by the cross-tenant attempt.
	services, _ = s.Catalog().List(t.Context(), tenantID)
	if services[0].Name != "Sintra Tour" {
		t.Fatalf("cross-tenant update leaked: %+v", services[0])
	}
}
