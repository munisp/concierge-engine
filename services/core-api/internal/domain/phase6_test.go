// Phase 6 geo tests: service areas (containment), libre geocoding with
// gazetteer fallback, partner proximity ranking.
package domain

import (
	"context"
	"testing"

	"concierge-engine/services/core-api/internal/model"
)

var victoriaIsland = [][2]float64{ // rough Lagos VI box
	{6.41, 3.39}, {6.41, 3.44}, {6.45, 3.44}, {6.45, 3.39},
}

func TestGeoServiceAreas(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	a, err := s.Geo.CreateArea(ctx, tenantID, "Victoria Island", victoriaIsland, nil)
	if err != nil || a.ID == "" {
		t.Fatalf("create: %v", err)
	}
	// inside VI
	hits, err := s.Geo.Contains(ctx, tenantID, 6.4281, 3.4219)
	if err != nil || len(hits) != 1 || hits[0] != "Victoria Island" {
		t.Fatalf("containment: %+v %v", hits, err)
	}
	// Ikoyi-ish, outside the box
	hits, _ = s.Geo.Contains(ctx, tenantID, 6.4549, 3.4352)
	_ = hits                                            // may or may not be inside depending on box; use a clear outside:
	hits, _ = s.Geo.Contains(ctx, tenantID, 6.60, 3.35) // mainland
	if len(hits) != 0 {
		t.Fatalf("mainland point must be outside VI: %+v", hits)
	}
	// invalid polygon rejected
	if _, err := s.Geo.CreateArea(ctx, tenantID, "bad", [][2]float64{{1, 2}, {3, 4}}, nil); err == nil {
		t.Fatal("2-vertex polygon must fail validation")
	}
	if _, err := s.Geo.CreateArea(ctx, tenantID, "bad2", [][2]float64{{91, 0}, {0, 0}, {0, 1}}, nil); err == nil {
		t.Fatal("out-of-range lat must fail validation")
	}
}

func TestGeoCrossTenantIsolation(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	t1, t2 := seedTenant(t, s), seedTenant(t, s)
	if _, err := s.Geo.CreateArea(ctx, t1, "VI", victoriaIsland, nil); err != nil {
		t.Fatal(err)
	}
	areas, _ := s.Geo.Areas(ctx, t2)
	if len(areas) != 0 {
		t.Fatalf("tenant leak: %+v", areas)
	}
	hits, _ := s.Geo.Contains(ctx, t2, 6.4281, 3.4219)
	if len(hits) != 0 {
		t.Fatalf("cross-tenant containment leak: %+v", hits)
	}
}

func TestGeocodeGazetteerFallback(t *testing.T) {
	s := testServices(t, Deps{}) // no geocoder configured
	res, err := s.Geo.Geocode(context.Background(), "pickup at DXB terminal 3")
	if err != nil || res.Source != "gazetteer" || res.Lat == 0 {
		t.Fatalf("gazetteer: %+v %v", res, err)
	}
	if _, err := s.Geo.Geocode(context.Background(), "nowhere known zzz"); err == nil {
		t.Fatal("unknown place must not be guessed")
	}
}

func TestNearestPartners(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	for _, p := range []model.Partner{
		{ID: "far", TenantID: tenantID, Name: "Abuja fleet", Status: "VETTED", Lat: 9.0765, Lng: 7.3986},
		{ID: "near", TenantID: tenantID, Name: "Lekki cars", Status: "VETTED", Lat: 6.4474, Lng: 3.4723},
		{ID: "pending", TenantID: tenantID, Name: "Unvetted", Status: "PENDING", Lat: 6.43, Lng: 3.42},
		{ID: "noloc", TenantID: tenantID, Name: "No location", Status: "VETTED"},
	} {
		p := p
		if err := s.Store.Partners().Create(ctx, &p); err != nil {
			t.Fatal(err)
		}
	}
	out, err := s.Geo.NearestPartners(ctx, tenantID, 6.4281, 3.4219, 0)
	if err != nil || len(out) != 3 { // pending partner excluded
		t.Fatalf("nearest: %+v %v", out, err)
	}
	if out[0].Partner.ID != "near" {
		t.Fatalf("nearest first: %+v", out[0].Partner.ID)
	}
	if out[len(out)-1].Partner.ID != "noloc" { // unlocated sorts last
		t.Fatalf("unlocated must sort last: %+v", out)
	}
}
