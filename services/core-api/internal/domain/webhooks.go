// Webhooks marketplace (Phase 3): tenant-managed event subscriptions with
// HMAC-signed, at-least-once delivery. Fan-out hooks the EventBus; a
// background dispatcher drains due deliveries with exponential backoff.
//
// Signature: X-Concierge-Signature: sha256=<hex hmac-sha256 of raw body with
// the subscription secret>. Receivers verify in constant time — the pattern
// matches the Stripe/Meta ingress verification we enforce on ourselves.
package domain

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type WebhookService struct {
	s  *Services
	hc *http.Client
}

func NewWebhookService(s *Services) *WebhookService {
	return &WebhookService{s: s, hc: &http.Client{Timeout: 10 * time.Second}}
}

func (svc *WebhookService) CreateSubscription(ctx context.Context, tenantID, url string, events []string) (*model.WebhookSubscription, error) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return nil, httpx.ErrValidation
	}
	if len(events) == 0 {
		return nil, httpx.ErrValidation
	}
	rawSecret := make([]byte, 24)
	if _, err := rand.Read(rawSecret); err != nil {
		return nil, err
	}
	sub := &model.WebhookSubscription{
		ID: NewID(), TenantID: tenantID, URL: url, Events: events,
		Secret: "whk_" + hex.EncodeToString(rawSecret), Active: true,
		CreatedAt: svc.s.Now(),
	}
	if err := svc.s.Store.Webhooks().CreateSubscription(ctx, sub); err != nil {
		return nil, err
	}
	svc.s.Audit.Log(ctx, tenantID, "api", "webhook.subscribed", "webhook", sub.ID,
		map[string]any{"url": url, "events": events})
	return sub, nil
}

func (svc *WebhookService) ListSubscriptions(ctx context.Context, tenantID string) ([]model.WebhookSubscription, error) {
	return svc.s.Store.Webhooks().ListSubscriptions(ctx, tenantID)
}

func (svc *WebhookService) SetActive(ctx context.Context, tenantID, id string, active bool) error {
	sub, err := svc.s.Store.Webhooks().GetSubscription(ctx, tenantID, id)
	if err != nil {
		return err
	}
	sub.Active = active
	return svc.s.Store.Webhooks().UpdateSubscription(ctx, sub)
}

func (svc *WebhookService) ListDeliveries(ctx context.Context, tenantID string, limit int) ([]model.WebhookDelivery, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	return svc.s.Store.Webhooks().ListDeliveries(ctx, tenantID, limit)
}

// FanOut enqueues a delivery per matching active subscription. Called from
// the EventBus hook on every emitted event — failures enqueue nothing (the
// outbox remains the durable event spine).
func (svc *WebhookService) FanOut(ctx context.Context, tenantID, eventType, eventID string, payload map[string]any) {
	subs, err := svc.s.Store.Webhooks().ListSubscriptions(ctx, tenantID)
	if err != nil {
		return
	}
	for i := range subs {
		sub := subs[i]
		if !sub.Active || !eventMatches(sub.Events, eventType) {
			continue
		}
		body, err := json.Marshal(map[string]any{
			"id": eventID, "type": eventType, "tenant_id": tenantID,
			"created_at": svc.s.Now().UTC(), "data": payload,
		})
		if err != nil {
			continue
		}
		_ = svc.s.Store.Webhooks().EnqueueDelivery(ctx, &model.WebhookDelivery{
			ID: NewID(), TenantID: tenantID, SubscriptionID: sub.ID,
			EventID: eventID, EventKind: eventType, Payload: string(body),
			MaxAttempts: 8, NextAttemptAt: svc.s.Now(), CreatedAt: svc.s.Now(),
		})
	}
}

// eventMatches supports exact kinds, "prefix.*" wildcards, and "*".
func eventMatches(patterns []string, kind string) bool {
	for _, p := range patterns {
		if p == "*" || p == kind {
			return true
		}
		if strings.HasSuffix(p, ".*") && strings.HasPrefix(kind, strings.TrimSuffix(p, "*")) {
			return true
		}
	}
	return false
}

// SignDelivery computes the X-Concierge-Signature value for a payload.
func SignDelivery(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// DispatchDue delivers up to limit due deliveries. 2xx marks delivered; any
// other outcome retries with exponential backoff until MaxAttempts, then the
// delivery is parked (visible via ListDeliveries for manual replay).
func (svc *WebhookService) DispatchDue(ctx context.Context, now time.Time, limit int) (int, error) {
	due, err := svc.s.Store.Webhooks().DueDeliveries(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	delivered := 0
	for i := range due {
		d := due[i]
		sub, err := svc.s.Store.Webhooks().GetSubscription(ctx, d.TenantID, d.SubscriptionID)
		if err != nil || !sub.Active {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.URL, bytes.NewReader([]byte(d.Payload)))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Concierge-Signature", SignDelivery(sub.Secret, []byte(d.Payload)))
		req.Header.Set("X-Concierge-Event", d.EventKind)
		req.Header.Set("X-Concierge-Delivery", d.ID)
		resp, err := svc.hc.Do(req)
		d.Attempts++
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			d.LastStatus = resp.StatusCode
			d.LastError = ""
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				d.DeliveredAt = now
				delivered++
			}
		} else {
			d.LastError = err.Error()
		}
		if d.DeliveredAt.IsZero() {
			// backoff: 2^attempts minutes, capped at 4h
			backoff := time.Duration(1<<min(d.Attempts, 8)) * time.Minute
			if backoff > 4*time.Hour {
				backoff = 4 * time.Hour
			}
			d.NextAttemptAt = now.Add(backoff)
		}
		_ = svc.s.Store.Webhooks().UpdateDelivery(ctx, &d)
	}
	return delivered, nil
}

// RunDispatcher drains deliveries on an interval until ctx is cancelled.
func (svc *WebhookService) RunDispatcher(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = svc.DispatchDue(ctx, svc.s.Now(), 100)
		}
	}
}
