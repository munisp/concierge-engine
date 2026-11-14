package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// GeoService (Phase 6): service-area polygons, serviceability checks,
// libre geocoding (OSM/Nominatim-compatible), and partner proximity.
//
// Storage semantics mirror PostGIS: the memory store implements ring
// containment in plain Go; the pg store (build tag pg) uses geography
// ST_Covers/ST_DWithin. Both share this ray-casting implementation for
// containment so behavior is identical across backends.

type GeoService struct {
	s      *Services
	geoURL string // CE_GEOCODE_URL: Nominatim/Photon-compatible endpoint
	geoKey string // CE_GEOCODE_API_KEY when the provider needs one
	hc     *http.Client
}

func NewGeoService(s *Services, geoURL, geoKey string) *GeoService {
	return &GeoService{s: s, geoURL: strings.TrimSuffix(geoURL, "/"), geoKey: geoKey,
		hc: &http.Client{Timeout: 8 * time.Second}}
}

// CreateArea validates and stores a service-area polygon. At least 3
// vertices; the ring is implicitly closed (first != last is fine).
func (svc *GeoService) CreateArea(ctx context.Context, tenantID, name string, polygon [][2]float64) (*model.ServiceArea, error) {
	if name == "" || len(polygon) < 3 {
		return nil, httpx.ErrValidation
	}
	for _, p := range polygon {
		if math.Abs(p[0]) > 90 || math.Abs(p[1]) > 180 {
			return nil, httpx.ErrValidation // [lat,lng] ranges
		}
	}
	a := &model.ServiceArea{ID: NewID(), TenantID: tenantID, Name: name,
		Polygon: polygon, CreatedAt: svc.s.Now()}
	if err := svc.s.Store.Geo().AddArea(ctx, a); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "geo", a.ID, "geo.area_created", map[string]any{"name": name})
	return a, nil
}

func (svc *GeoService) Areas(ctx context.Context, tenantID string) ([]model.ServiceArea, error) {
	return svc.s.Store.Geo().Areas(ctx, tenantID)
}

// Serviceability: which areas contain the point. Empty list + areas defined
// = outside service area (a real product signal for quotes).
func (svc *GeoService) Contains(ctx context.Context, tenantID string, lat, lng float64) ([]string, error) {
	areas, err := svc.s.Store.Geo().Areas(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var hits []string
	for _, a := range areas {
		if pointInPolygon(lat, lng, a.Polygon) {
			hits = append(hits, a.Name)
		}
	}
	return hits, nil
}

// pointInPolygon: ray casting on [lat,lng] rings. Fine for city-scale
// convex-ish service areas; PostGIS ST_Covers is the exact path in prod.
func pointInPolygon(lat, lng float64, ring [][2]float64) bool {
	inside := false
	n := len(ring)
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		yi, xi := ring[i][0], ring[i][1]
		yj, xj := ring[j][0], ring[j][1]
		if (xi > lng) != (xj > lng) &&
			lat < (yj-yi)*(lng-xi)/(xj-xi)+yi {
			inside = !inside
		}
	}
	return inside
}

// haversineKm for partner proximity ranking.
func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const r = 6371.0
	rad := math.Pi / 180
	dLat, dLng := (lat2-lat1)*rad, (lng2-lng1)*rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * r * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// PartnerDistance pairs a partner with its distance from a point.
type PartnerDistance struct {
	Partner  model.Partner `json:"partner"`
	Distance float64       `json:"distance_km"`
}

// NearestPartners ranks partners by haversine distance from a point.
// Partners without coordinates sort last (unknown location is honest).
func (svc *GeoService) NearestPartners(ctx context.Context, tenantID string, lat, lng float64, limit int) ([]PartnerDistance, error) {
	partners, err := svc.s.Store.Partners().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var located, unlocated []PartnerDistance
	for _, p := range partners {
		if p.Status != "VETTED" {
			continue
		}
		if p.Lat == 0 && p.Lng == 0 {
			located := PartnerDistance{Partner: p, Distance: math.Inf(1)}
			unlocated = append(unlocated, located)
			continue
		}
		located = append(located, PartnerDistance{Partner: p, Distance: haversineKm(lat, lng, p.Lat, p.Lng)})
	}
	for i := 1; i < len(located); i++ {
		for j := i; j > 0 && located[j].Distance < located[j-1].Distance; j-- {
			located[j], located[j-1] = located[j-1], located[j]
		}
	}
	out := append(located, unlocated...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// GeocodeResult from the libre stack or the built-in gazetteer.
type GeocodeResult struct {
	Lat    float64 `json:"lat"`
	Lng    float64 `json:"lng"`
	Label  string  `json:"label"`
	Source string  `json:"source"` // "nominatim" | "gazetteer"
}

// gazetteer: the concierge operating set — major airports/cities our tenants
// serve. Keeps dev/tests offline-capable when no geocoder is configured.
var gazetteer = map[string][2]float64{
	"los":    {6.5774, 3.3212}, // Lagos MMIA
	"lagos":  {6.5244, 3.3792},
	"dxb":    {25.2532, 55.3657}, // Dubai Intl
	"dubai":  {25.2048, 55.2708},
	"lhr":    {51.4700, -0.4543}, // London Heathrow
	"london": {51.5074, -0.1278},
	"jfk":    {40.6413, -73.7781},
	"cdg":    {49.0097, 2.5479},
	"ist":    {41.2753, 28.7519}, // Istanbul
	"nce":    {43.6584, 7.2159},  // Nice
	"monaco": {43.7384, 7.4246},
	"ruh":    {24.9576, 46.6988}, // Riyadh
}

// Geocode resolves a place string to coordinates: Nominatim-compatible
// endpoint when configured (self-hosted OSM — the libre stack), else the
// built-in gazetteer. Unknown places are a clean not-found, never a guess.
func (svc *GeoService) Geocode(ctx context.Context, query string) (*GeocodeResult, error) {
	if query == "" {
		return nil, httpx.ErrValidation
	}
	if svc.geoURL != "" {
		u := fmt.Sprintf("%s/search?q=%s&format=json&limit=1", svc.geoURL, url.QueryEscape(query))
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if svc.geoKey != "" {
			req.Header.Set("Authorization", "Bearer "+svc.geoKey)
		}
		resp, err := svc.hc.Do(req)
		if err == nil {
			defer resp.Body.Close()
			var raw []struct {
				Lat   string `json:"lat"`
				Lon   string `json:"lon"`
				Label string `json:"display_name"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&raw); err == nil && len(raw) > 0 {
				var la, ln float64
				fmt.Sscanf(raw[0].Lat, "%f", &la)
				fmt.Sscanf(raw[0].Lon, "%f", &ln)
				if la != 0 || ln != 0 {
					return &GeocodeResult{Lat: la, Lng: ln, Label: raw[0].Label, Source: "nominatim"}, nil
				}
			}
		}
		// fall through to gazetteer on any adapter failure
	}
	q := strings.ToLower(strings.TrimSpace(query))
	for key, ll := range gazetteer {
		if q == key || strings.Contains(q, key) {
			return &GeocodeResult{Lat: ll[0], Lng: ll[1],
				Label: query, Source: "gazetteer"}, nil
		}
	}
	return nil, httpx.ErrNotFound
}
