package store

// Phase 15: in-memory CRM-360 surface — client search, duplicate merge,
// client-scoped listings (requests/bookings/cases/conversations), webhook
// delivery lookup for replay tooling.

import (
	"context"
	"sort"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// ------------------------------------------------------------- clients

func (s *memClients) Search(_ context.Context, tenantID, query string, limit int) ([]model.Client, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, httpx.ErrValidation
	}
	if limit <= 0 {
		limit = 25
	}
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.Client
	for _, c := range s.m.clients {
		if c.TenantID != tenantID {
			continue
		}
		if strings.Contains(strings.ToLower(c.FullName), q) ||
			strings.Contains(strings.ToLower(c.Email), q) ||
			strings.Contains(c.Phone, q) {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Merge re-points every client-scoped record from loser to winner, then
// deletes the loser. Bookings follow automatically via their request.
func (s *memClients) Merge(_ context.Context, tenantID, winnerID, loserID string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if _, ok := s.m.clients[tenantID+"/"+winnerID]; !ok {
		return httpx.ErrNotFound
	}
	if _, ok := s.m.clients[tenantID+"/"+loserID]; !ok {
		return httpx.ErrNotFound
	}
	for _, r := range s.m.requests {
		if r.TenantID == tenantID && r.ClientID == loserID {
			r.ClientID = winnerID
		}
	}
	for _, c := range s.m.cases {
		if c.TenantID == tenantID && c.ClientID == loserID {
			c.ClientID = winnerID
		}
	}
	for _, cv := range s.m.conversations {
		if cv.TenantID == tenantID && cv.ClientID == loserID {
			cv.ClientID = winnerID
		}
	}
	for _, p := range s.m.preferences {
		if p.TenantID == tenantID && p.ClientID == loserID {
			p.ClientID = winnerID
		}
	}
	for i := range s.m.attribution {
		if s.m.attribution[i].TenantID == tenantID && s.m.attribution[i].ClientID == loserID {
			s.m.attribution[i].ClientID = winnerID
		}
	}
	for _, o := range s.m.outreachSends {
		if o.TenantID == tenantID && o.ClientID == loserID {
			o.ClientID = winnerID
		}
	}
	delete(s.m.clients, tenantID+"/"+loserID)
	return nil
}

// ------------------------------------------------------------- client-scoped listings

func (s *memRequests) ForClient(_ context.Context, tenantID, clientID string, limit int) ([]model.ServiceRequest, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.ServiceRequest
	for _, r := range s.m.requests {
		if r.TenantID == tenantID && r.ClientID == clientID {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *memBookings) ForClient(_ context.Context, tenantID, clientID string, limit int) ([]model.Booking, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	// Bookings carry no client_id — resolve through their request.
	requestIDs := map[string]bool{}
	for _, r := range s.m.requests {
		if r.TenantID == tenantID && r.ClientID == clientID {
			requestIDs[r.ID] = true
		}
	}
	var out []model.Booking
	for _, b := range s.m.bookings {
		if b.TenantID == tenantID && requestIDs[b.RequestID] {
			out = append(out, *b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *memCases) ForClient(_ context.Context, tenantID, clientID string, limit int) ([]model.Case, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.Case
	for _, c := range s.m.cases {
		if c.TenantID == tenantID && c.ClientID == clientID {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *memConversations) ForClient(_ context.Context, tenantID, clientID string, limit int) ([]model.Conversation, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.Conversation
	for _, c := range s.m.conversations {
		if c.TenantID == tenantID && c.ClientID == clientID {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ------------------------------------------------------------- webhook delivery lookup

func (s *memWebhooks) GetDelivery(_ context.Context, tenantID, id string) (*model.WebhookDelivery, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	d, ok := s.m.webhookDlvs[id]
	if !ok || d.TenantID != tenantID {
		return nil, httpx.ErrNotFound
	}
	cp := *d
	return &cp, nil
}
