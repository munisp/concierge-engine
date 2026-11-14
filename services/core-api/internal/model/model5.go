package model

import "time"

// ServiceArea: a named polygon (lat/lng ring, GeoJSON order [lng,lat] at the
// API edge; stored as WKT/geography in PostGIS) defining where a tenant
// operates — airports, city zones, partner coverage. Phase 6 geo wave.
type ServiceArea struct {
	ID        string       `json:"id"`
	TenantID  string       `json:"tenant_id"`
	Name      string       `json:"name"`
	Polygon   [][2]float64 `json:"polygon"` // [lat,lng] pairs, implicitly closed
	CreatedAt time.Time    `json:"created_at"`
}
