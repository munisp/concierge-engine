package store

// Phase 61: in-memory itinerary store. Keyed tenantID/id; copies in and
// out, matching every other memory store.

import (
	"context"
	"sort"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (m *memory) Itineraries() ItineraryStore { return &memItineraries{m} }

type memItineraries struct{ m *memory }

func (s *memItineraries) Create(_ context.Context, l *model.ItineraryLeg) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *l
	s.m.legs[l.TenantID+"/"+l.ID] = &cp
	return nil
}

func (s *memItineraries) Get(_ context.Context, tenantID, id string) (*model.ItineraryLeg, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	l, ok := s.m.legs[tenantID+"/"+id]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *l
	return &cp, nil
}

func (s *memItineraries) Update(_ context.Context, l *model.ItineraryLeg) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if _, ok := s.m.legs[l.TenantID+"/"+l.ID]; !ok {
		return httpx.ErrNotFound
	}
	cp := *l
	s.m.legs[l.TenantID+"/"+l.ID] = &cp
	return nil
}

func (s *memItineraries) ListForClient(_ context.Context, tenantID, clientID string, limit int) ([]model.ItineraryLeg, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.ItineraryLeg
	for _, l := range s.m.legs {
		if l.TenantID == tenantID && l.ClientID == clientID {
			out = append(out, *l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SchedStart.Before(out[j].SchedStart) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *memItineraries) ForBooking(_ context.Context, tenantID, bookingID string) ([]model.ItineraryLeg, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.ItineraryLeg
	for _, l := range s.m.legs {
		if l.TenantID == tenantID && l.BookingID == bookingID {
			out = append(out, *l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SchedStart.Before(out[j].SchedStart) })
	return out, nil
}

func (s *memItineraries) FlightLegsDue(_ context.Context, tenantID string, from, to time.Time) ([]model.ItineraryLeg, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.ItineraryLeg
	for _, l := range s.m.legs {
		if l.TenantID != tenantID || l.Kind != model.LegFlight {
			continue
		}
		if l.Status == model.LegArrived || l.Status == model.LegCompleted || l.Status == model.LegCancelled {
			continue
		}
		if l.SchedStart.Before(from) || l.SchedStart.After(to) {
			continue
		}
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SchedStart.Before(out[j].SchedStart) })
	return out, nil
}
