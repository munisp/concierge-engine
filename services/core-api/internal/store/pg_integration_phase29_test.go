//go:build pg

package store

// Phase 29: name_i18n / desc_i18n jsonb columns on catalog_services and
// service_areas round-trip through Postgres.

import (
	"testing"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGI18nColumns29(t *testing.T) {
	s := pgTestStore(t)
	tenantID := pgTenant(t, s, "i18n29")

	svc := &model.CatalogService{
		ID: newTestUUID(), TenantID: tenantID, Vertical: "PRIVATE_TOUR",
		Name: "Sintra Private Tour", BaseMinor: 35000, Currency: "EUR", Active: true,
		NameI18n: map[string]string{"pt-PT": "Tour Privado de Sintra"},
		DescI18n: map[string]string{
			"pt-PT": "Palácios, serra e costa num tour privado de dia inteiro.",
			"en":    "Palaces, mountains and coast on a private full-day tour.",
		},
	}
	if err := s.Catalog().Add(t.Context(), svc); err != nil {
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
	if got.NameI18n["pt-PT"] != "Tour Privado de Sintra" {
		t.Fatalf("name_i18n lost: %+v", got.NameI18n)
	}
	if got.DescI18n["en"] != "Palaces, mountains and coast on a private full-day tour." {
		t.Fatalf("desc_i18n lost: %+v", got.DescI18n)
	}

	area := &model.ServiceArea{
		ID: newTestUUID(), TenantID: tenantID, Name: "Lisbon",
		Polygon:  [][2]float64{{38.69, -9.23}, {38.80, -9.23}, {38.80, -9.09}, {38.69, -9.09}},
		NameI18n: map[string]string{"pt-PT": "Lisboa"},
	}
	if err := s.Geo().AddArea(t.Context(), area); err != nil {
		t.Fatal(err)
	}
	areas, err := s.Geo().Areas(t.Context(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(areas) != 1 || areas[0].NameI18n["pt-PT"] != "Lisboa" {
		t.Fatalf("area name_i18n lost: %+v", areas)
	}

	// Rows without i18n still scan (empty maps, not nil-pointer surprises).
	if err := s.Catalog().Add(t.Context(), &model.CatalogService{
		ID: newTestUUID(), TenantID: tenantID, Vertical: "CHAUFFEUR_POINT_TO_POINT",
		Name: "Plain", BaseMinor: 100, Currency: "EUR", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	services, err = s.Catalog().List(t.Context(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	for _, sv := range services {
		if sv.Name == "Plain" && (sv.NameI18n == nil || len(sv.NameI18n) != 0) {
			t.Fatalf("default i18n should be an empty map: %+v", sv.NameI18n)
		}
	}
}
