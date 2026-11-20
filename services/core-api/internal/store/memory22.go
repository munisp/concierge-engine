package store

import (
	"context"
	"sort"

	"concierge-engine/services/core-api/internal/model"
)

// Phase 63: demand-channel listings. Requests sourced by one partner,
// newest first — the partner portal's "my guests" feed. The mem request
// map is keyed by bare ID (like every other lookup), copies returned.
func (s *memRequests) ForSource(_ context.Context, tenantID, partnerID string, limit int) ([]model.ServiceRequest, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.ServiceRequest
	for _, r := range s.m.requests {
		if r.TenantID == tenantID && r.SourcePartnerID == partnerID {
			cp := *r
			cp.Items = append([]model.ServiceItem(nil), r.Items...)
			out = append(out, cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
