//go:build pg

package store

// Phase 33: CatalogStore.ListAll returns active AND inactive services — the
// media-delete scrub must reach every reference, not just the ones the
// public catalog list can see.

import (
	"testing"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGCatalogListAll33(t *testing.T) {
	s := pgTestStore(t)
	tenantID := pgTenant(t, s, "listall33")
	otherID := pgTenant(t, s, "listall33b")

	live := &model.CatalogService{
		ID: newTestUUID(), TenantID: tenantID, Vertical: "ground",
		Name: "Airport Transfer", BaseMinor: 9500, Currency: "EUR", Active: true,
	}
	retired := &model.CatalogService{
		ID: newTestUUID(), TenantID: tenantID, Vertical: "tour",
		Name: "Retired Tour", BaseMinor: 25000, Currency: "EUR", Active: true,
	}
	foreign := &model.CatalogService{
		ID: newTestUUID(), TenantID: otherID, Vertical: "ground",
		Name: "Other Tenant Service", BaseMinor: 100, Currency: "EUR", Active: true,
	}
	for _, c := range []*model.CatalogService{live, retired, foreign} {
		if err := s.Catalog().Add(t.Context(), c); err != nil {
			t.Fatal(err)
		}
	}
	retired.Active = false
	if err := s.Catalog().Update(t.Context(), retired); err != nil {
		t.Fatal(err)
	}

	// List: only the active row, tenant-scoped.
	l, err := s.Catalog().List(t.Context(), tenantID)
	if err != nil || len(l) != 1 || l[0].ID != live.ID {
		t.Fatalf("list: %v %v", l, err)
	}

	// ListAll: both rows, tenant-scoped, sorted by name.
	all, err := s.Catalog().ListAll(t.Context(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("listall should include the inactive row: %v", all)
	}
	if all[0].Name != "Airport Transfer" || all[1].Name != "Retired Tour" {
		t.Fatalf("listall ordering: %v", all)
	}
	for _, c := range all {
		if c.ID == foreign.ID {
			t.Fatalf("listall leaked another tenant's service: %v", all)
		}
	}
}
