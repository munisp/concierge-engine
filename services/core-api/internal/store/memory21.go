package store

// Phase 62: in-memory chauffeur-link store (rider↔chauffeur favorite /
// blocked) plus the matcher's prior-trips count on the dispatch store.

import (
	"context"

	"concierge-engine/services/core-api/internal/model"
)

func (m *memory) ChauffeurLinks() ChauffeurLinkStore { return &memChauffeurLinks{m} }

type memChauffeurLinks struct{ m *memory }

func chauffeurLinkKey(tenantID, clientID, crewID string) string {
	return tenantID + "/" + clientID + "/" + crewID
}

func (s *memChauffeurLinks) Set(_ context.Context, l *model.ChauffeurLink) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *l
	s.m.chauffLinks[chauffeurLinkKey(l.TenantID, l.ClientID, l.CrewMemberID)] = &cp
	return nil
}

func (s *memChauffeurLinks) Clear(_ context.Context, tenantID, clientID, crewID string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	delete(s.m.chauffLinks, chauffeurLinkKey(tenantID, clientID, crewID))
	return nil
}

func (s *memChauffeurLinks) ListForClient(_ context.Context, tenantID, clientID string) ([]model.ChauffeurLink, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	out := []model.ChauffeurLink{}
	for _, l := range s.m.chauffLinks {
		if l.TenantID == tenantID && l.ClientID == clientID {
			out = append(out, *l)
		}
	}
	return out, nil
}

// CountClientTrips: completed assignments for this crew member whose
// booking belongs to this client.
func (s *memDispatch) CountClientTrips(_ context.Context, tenantID, clientID, crewMemberID string) (int, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	n := 0
	for _, a := range s.m.assignments {
		if a.TenantID != tenantID || a.CrewMemberID != crewMemberID || a.Status != "COMPLETED_TRIP" {
			continue
		}
		b, ok := s.m.bookings[a.BookingID]
		if !ok {
			continue
		}
		if r, ok := s.m.requests[b.RequestID]; ok && r.ClientID == clientID {
			n++
		}
	}
	return n, nil
}
