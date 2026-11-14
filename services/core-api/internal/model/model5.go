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

// MessageReceipt: a delivery/read status callback from the channel
// (Meta statuses[] or Infobip delivery reports), keyed by the channel's
// message id. SLA evidence: proves an operator reply actually reached the
// client's handset. Phase 11.
type MessageReceipt struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Channel     string    `json:"channel"`      // "whatsapp" | ...
	ExternalRef string    `json:"external_ref"` // channel message id (wamid / Infobip messageId)
	Status      string    `json:"status"`       // sent | delivered | read | failed
	At          time.Time `json:"at"`
	CreatedAt   time.Time `json:"created_at"`
}
