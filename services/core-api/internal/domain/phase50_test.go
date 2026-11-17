package domain

// Phase 50: webhook delivery is the platform's main SSRF surface — a tenant
// sets the URL, we fetch it. These tests pin the guarded-by-default
// behavior and the explicit test/dev escape hatch.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/store"
)

func TestWebhookDeliverySSRFGuard(t *testing.T) {
	// NOTE: deliberately NOT testServices — the guard must stay ON here.
	s := NewServices(store.NewMemory(), Deps{})
	tenantID := seedTenant(t, s)
	ctx := context.Background()

	reached := 0
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached++
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	sub, err := s.Webhooks.CreateSubscription(ctx, tenantID, receiver.URL, []string{"client.*"})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	s.Events.Emit(ctx, tenantID, "client", "c1", "client.created", nil)

	delivered, err := s.Webhooks.DispatchDue(ctx, s.Now(), 10)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if delivered != 0 || reached != 0 {
		t.Fatalf("guarded delivery to loopback must not happen: delivered=%d reached=%d", delivered, reached)
	}

	dels, err := s.Webhooks.ListDeliveries(ctx, tenantID, 10)
	if err != nil || len(dels) != 1 {
		t.Fatalf("deliveries: %v %+v", err, dels)
	}
	if !strings.Contains(dels[0].LastError, "ssrf") {
		t.Fatalf("failure reason must name the ssrf guard, got %q", dels[0].LastError)
	}
	if dels[0].Attempts != 1 {
		t.Fatalf("blocked delivery counts as an attempt (retry will re-block), got %d", dels[0].Attempts)
	}

	// The escape hatch delivers to the same receiver — proving the block is
	// the guard, not some unrelated transport failure.
	s.Webhooks.AllowPrivateTargets = true
	if _, err := s.Webhooks.ReplayDelivery(ctx, tenantID, dels[0].ID); err != nil {
		t.Fatalf("replay: %v", err)
	}
	delivered, err = s.Webhooks.DispatchDue(ctx, s.Now(), 10)
	if err != nil || delivered != 1 || reached != 1 {
		t.Fatalf("after allow-private replay: delivered=%d reached=%d err=%v", delivered, reached, err)
	}
	_ = sub
}
