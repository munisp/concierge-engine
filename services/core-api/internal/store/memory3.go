// In-memory Phase 3 stores: API keys, webhook subscriptions/deliveries,
// cross-tenant referrals. Same tenant-scoping and idempotency discipline as
// memory.go/memory2.go; the Postgres implementation lives in postgres3.go.
package store

import (
	"context"
	"sort"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// ------------------------------------------------------------- API keys
type memApiKeys struct{ m *memory }

func (s *memApiKeys) Create(_ context.Context, k *model.ApiKey) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *k
	s.m.apiKeys[k.ID] = &cp
	return nil
}

func (s *memApiKeys) GetByPrefix(_ context.Context, prefix string) (*model.ApiKey, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	for _, k := range s.m.apiKeys {
		if k.Prefix == prefix {
			cp := *k
			return &cp, nil
		}
	}
	return nil, httpx.ErrNotFound
}

func (s *memApiKeys) List(_ context.Context, tenantID string) ([]model.ApiKey, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.ApiKey
	for _, k := range s.m.apiKeys {
		if k.TenantID == tenantID {
			out = append(out, *k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *memApiKeys) Revoke(_ context.Context, tenantID, id string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	k, ok := s.m.apiKeys[id]
	if !ok || k.TenantID != tenantID {
		return httpx.ErrNotFound
	}
	k.Revoked = true
	return nil
}

func (s *memApiKeys) TouchUsed(_ context.Context, id string, at time.Time) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if k, ok := s.m.apiKeys[id]; ok {
		k.LastUsedAt = at
	}
	return nil
}

// ------------------------------------------------------------- webhooks
type memWebhooks struct{ m *memory }

func (s *memWebhooks) CreateSubscription(_ context.Context, sub *model.WebhookSubscription) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *sub
	s.m.webhookSubs[sub.TenantID+"/"+sub.ID] = &cp
	return nil
}

func (s *memWebhooks) GetSubscription(_ context.Context, tenantID, id string) (*model.WebhookSubscription, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	sub, ok := s.m.webhookSubs[tenantID+"/"+id]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *sub
	return &cp, nil
}

func (s *memWebhooks) ListSubscriptions(_ context.Context, tenantID string) ([]model.WebhookSubscription, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.WebhookSubscription
	for _, sub := range s.m.webhookSubs {
		if sub.TenantID == tenantID {
			out = append(out, *sub)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *memWebhooks) UpdateSubscription(_ context.Context, sub *model.WebhookSubscription) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if _, ok := s.m.webhookSubs[sub.TenantID+"/"+sub.ID]; !ok {
		return httpx.ErrNotFound
	}
	cp := *sub
	s.m.webhookSubs[sub.TenantID+"/"+sub.ID] = &cp
	return nil
}

func (s *memWebhooks) EnqueueDelivery(_ context.Context, d *model.WebhookDelivery) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *d
	s.m.webhookDlvs[d.ID] = &cp
	return nil
}

func (s *memWebhooks) DueDeliveries(_ context.Context, now time.Time, limit int) ([]model.WebhookDelivery, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.WebhookDelivery
	for _, d := range s.m.webhookDlvs {
		if d.DeliveredAt.IsZero() && d.Attempts < d.MaxAttempts && !d.NextAttemptAt.After(now) {
			out = append(out, *d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NextAttemptAt.Before(out[j].NextAttemptAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *memWebhooks) UpdateDelivery(_ context.Context, d *model.WebhookDelivery) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if _, ok := s.m.webhookDlvs[d.ID]; !ok {
		return httpx.ErrNotFound
	}
	cp := *d
	s.m.webhookDlvs[d.ID] = &cp
	return nil
}

func (s *memWebhooks) ListDeliveries(_ context.Context, tenantID string, limit int) ([]model.WebhookDelivery, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.WebhookDelivery
	for _, d := range s.m.webhookDlvs {
		if d.TenantID == tenantID {
			out = append(out, *d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ------------------------------------------------------------- referrals
type memReferrals struct{ m *memory }

func (s *memReferrals) Create(_ context.Context, r *model.Referral) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	key := r.ReferringTenantID + "/" + r.IdempotencyKey
	if r.IdempotencyKey != "" {
		if existingID, ok := s.m.referralIdem[key]; ok {
			cp := *s.m.referrals[existingID]
			*r = cp // idempotent replay returns the original
			return nil
		}
	}
	cp := *r
	s.m.referrals[r.ID] = &cp
	if r.IdempotencyKey != "" {
		s.m.referralIdem[key] = r.ID
	}
	return nil
}

// Get enforces visibility on both sides: a referral is visible to the
// referring and the receiving tenant, nobody else.
func (s *memReferrals) Get(_ context.Context, tenantID, id string) (*model.Referral, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	r, ok := s.m.referrals[id]
	if !ok || (r.ReferringTenantID != tenantID && r.ReceivingTenantID != tenantID) {
		return nil, httpx.ErrNotFound
	}
	cp := *r
	return &cp, nil
}

func (s *memReferrals) List(_ context.Context, tenantID string) ([]model.Referral, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var out []model.Referral
	for _, r := range s.m.referrals {
		if r.ReferringTenantID == tenantID || r.ReceivingTenantID == tenantID {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *memReferrals) Update(_ context.Context, r *model.Referral) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if _, ok := s.m.referrals[r.ID]; !ok {
		return httpx.ErrNotFound
	}
	cp := *r
	s.m.referrals[r.ID] = &cp
	return nil
}
