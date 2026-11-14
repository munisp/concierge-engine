package store

// Phase 14: in-memory outreach sends, attribution list/funnel, and client
// referral-code lookup.

import (
	"context"
	"sort"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// ------------------------------------------------------------- outreach

type memOutreach struct{ m *memory }

func (s *memOutreach) AddSend(_ context.Context, o *model.OutreachSend) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	key := o.TenantID + "|" + o.IdempotencyKey
	if _, dup := s.m.outreachSends[key]; dup {
		return httpx.ErrConflict
	}
	cp := *o
	s.m.outreachSends[key] = &cp
	return nil
}

func (s *memOutreach) SendsForClient(_ context.Context, tenantID, clientID string, since time.Time) ([]model.OutreachSend, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.OutreachSend
	for _, o := range s.m.outreachSends {
		if o.TenantID == tenantID && o.ClientID == clientID && !o.CreatedAt.Before(since) {
			out = append(out, *o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *memOutreach) ListSends(_ context.Context, tenantID string, limit int) ([]model.OutreachSend, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.OutreachSend
	for _, o := range s.m.outreachSends {
		if o.TenantID == tenantID {
			out = append(out, *o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ------------------------------------------------------------- attribution

func (s *memAttribution) List(_ context.Context, tenantID, clientID string, limit int) ([]model.AttributionEvent, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.AttributionEvent
	for _, e := range s.m.attribution { // slice order == insertion order == oldest first
		if e.TenantID == tenantID && e.ClientID == clientID {
			out = append(out, e)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (s *memAttribution) Funnel(_ context.Context, tenantID string) ([]FunnelRow, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	type key struct{ kind, channel, campaign string }
	cells := map[key]*FunnelRow{}
	for _, e := range s.m.attribution {
		if e.TenantID != tenantID {
			continue
		}
		k := key{e.Kind, e.Channel, e.Campaign}
		row, ok := cells[k]
		if !ok {
			row = &FunnelRow{Kind: e.Kind, Channel: e.Channel, Campaign: e.Campaign}
			cells[k] = row
		}
		row.Count++
		row.ValueMinor += e.ValueMinor
	}
	out := make([]FunnelRow, 0, len(cells))
	for _, r := range cells {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Channel != out[j].Channel {
			return out[i].Channel < out[j].Channel
		}
		return out[i].Campaign < out[j].Campaign
	})
	return out, nil
}

// ------------------------------------------------------------- client referral code

func (s *memClients) FindByReferralCode(_ context.Context, tenantID, code string) (*model.Client, error) {
	prefix, ok := model.ReferralCodePrefix(code)
	if !ok {
		return nil, httpx.ErrNotFound
	}
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	for _, c := range s.m.clients {
		if c.TenantID != tenantID {
			continue
		}
		if strings.HasPrefix(strings.ToUpper(strings.ReplaceAll(c.ID, "-", "")), prefix) {
			cp := *c
			return &cp, nil
		}
	}
	return nil, httpx.ErrNotFound
}
