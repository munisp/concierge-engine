package store

import (
	"context"

	"concierge-engine/services/core-api/internal/model"
)

// In-memory geo store: mirrors the PostGIS semantics (ring containment) in
// plain Go so tests and dev need no database.
type memGeo struct{ m *memory }

func (g *memGeo) AddArea(ctx context.Context, a *model.ServiceArea) error {
	g.m.mu.Lock()
	defer g.m.mu.Unlock()
	g.m.geoAreas[a.TenantID+"|"+a.ID] = a
	return nil
}

func (g *memGeo) Areas(ctx context.Context, tenantID string) ([]model.ServiceArea, error) {
	g.m.mu.RLock()
	defer g.m.mu.RUnlock()
	out := []model.ServiceArea{}
	for k, a := range g.m.geoAreas {
		if k[:len(tenantID)+1] == tenantID+"|" {
			out = append(out, *a)
		}
	}
	return out, nil
}
