//go:build pg

package store

// Phase 32: site_config design-layer fields (design, hidden_sections,
// gallery_ids) round-trip through the tenants jsonb column, and the new
// CatalogStore.Get returns inactive services (the reactivation path).

import (
	"testing"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPGSiteConfigDesign32(t *testing.T) {
	s := pgTestStore(t)
	tenantID := pgTenant(t, s, "design32")

	tn, err := s.Tenants().Get(t.Context(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	tn.SiteConfig.Design = "editorial"
	tn.SiteConfig.HiddenSections = []string{"reviews", "areas"}
	tn.SiteConfig.GalleryIDs = []string{newTestUUID(), newTestUUID()}
	tn.SiteConfig.HeroImageID = newTestUUID()
	if err := s.Tenants().UpdateConfig(t.Context(), tn); err != nil {
		t.Fatal(err)
	}

	got, err := s.Tenants().Get(t.Context(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	sc := got.SiteConfig
	if sc.Design != "editorial" {
		t.Fatalf("design: %q", sc.Design)
	}
	if len(sc.HiddenSections) != 2 || sc.HiddenSections[0] != "reviews" || sc.HiddenSections[1] != "areas" {
		t.Fatalf("hidden_sections: %v", sc.HiddenSections)
	}
	if len(sc.GalleryIDs) != 2 {
		t.Fatalf("gallery_ids: %v", sc.GalleryIDs)
	}
	if sc.HeroImageID != tn.SiteConfig.HeroImageID {
		t.Fatalf("hero_image_id: %q", sc.HeroImageID)
	}
}

func TestPGCatalogGet32(t *testing.T) {
	s := pgTestStore(t)
	tenantID := pgTenant(t, s, "catget32")
	otherID := pgTenant(t, s, "catget32b")

	svc := &model.CatalogService{
		ID: newTestUUID(), TenantID: tenantID, Vertical: "ground",
		Name: "Airport Transfer", BaseMinor: 9500, Currency: "EUR", Active: true,
	}
	if err := s.Catalog().Add(t.Context(), svc); err != nil {
		t.Fatal(err)
	}

	// Deactivate, then Get must still find it (List filters on Active).
	svc.Active = false
	svc.BaseMinor = 12000
	if err := s.Catalog().Update(t.Context(), svc); err != nil {
		t.Fatal(err)
	}
	if l, err := s.Catalog().List(t.Context(), tenantID); err != nil || len(l) != 0 {
		t.Fatalf("inactive must not list: %v %v", l, err)
	}
	got, err := s.Catalog().Get(t.Context(), tenantID, svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Active || got.BaseMinor != 12000 {
		t.Fatalf("get: %+v", got)
	}

	// Tenant-scoped + missing ids.
	if _, err := s.Catalog().Get(t.Context(), otherID, svc.ID); err != httpx.ErrNotFound {
		t.Fatalf("cross-tenant get: %v", err)
	}
	if _, err := s.Catalog().Get(t.Context(), tenantID, newTestUUID()); err != httpx.ErrNotFound {
		t.Fatalf("missing get: %v", err)
	}
}
