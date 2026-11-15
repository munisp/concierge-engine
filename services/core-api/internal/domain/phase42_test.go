package domain

// Phase 42: WhatsApp list hygiene — the unreachable-number hold (3+ failed
// deliveries, none confirmed since) and the reply-reset engagement sunset.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// waStubDistinct returns a fresh wamid per call so each send's receipts are
// individually correlatable.
func waStubDistinct(t *testing.T, calls *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		json.NewEncoder(w).Encode(map[string]any{"mode": "freeform",
			"body": map[string]any{"messages": []map[string]string{
				{"id": fmt.Sprintf("wamid.p42-%d", *calls)}}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func waSends(t *testing.T, s *Services, tenantID, clientID string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
			ClientID: clientID, Purpose: model.OutreachMarketing, Channel: "whatsapp",
			Body: "offer", IdempotencyKey: fmt.Sprintf("wa42-%d", i),
		})
		if err != nil || o.Status != "sent" {
			t.Fatalf("wa send %d: %+v %v", i, o, err)
		}
	}
}

func TestWaUnreachable42(t *testing.T) {
	now := time.Now() // anchored to real time: the memory store stamps real time
	calls := 0
	s := testServices(t, Deps{
		Now:         func() time.Time { return now },
		WhatsAppURL: waStubDistinct(t, &calls).URL,
	})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000042", true)

	waSends(t, s, tenantID, clientID, 3)
	if calls != 3 {
		t.Fatalf("adapter calls: %d", calls)
	}

	// Three failed deliveries, each correlating back to its send's wamid.
	for i := 1; i <= 3; i++ {
		if _, err := s.Receipts.Record(ctx, tenantID, "whatsapp",
			fmt.Sprintf("wamid.p42-%d", i), "failed", time.Time{}); err != nil {
			t.Fatalf("receipt %d: %v", i, err)
		}
	}

	now = now.Add(8 * 24 * time.Hour) // clear the 3/7d cap so hygiene decides
	v, err := s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, "whatsapp")
	if err != nil || v.Allowed || !v.WaUnreachable || !strings.Contains(v.Reason, "unreachable") {
		t.Fatalf("should be held unreachable: %+v %v", v, err)
	}

	// A send attempt is held and the adapter is not called.
	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachMarketing, Channel: "whatsapp",
		Body: "offer", IdempotencyKey: "wa42-held",
	})
	if err != nil || o.Status != "held" || calls != 3 {
		t.Fatalf("held send: %+v calls=%d %v", o, calls, err)
	}

	// Service lane is exempt.
	if v, _ := s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachService, "whatsapp"); !v.Allowed {
		t.Fatalf("service exempt: %+v", v)
	}

	// A delivered receipt after the latest failure proves the number alive.
	if _, err := s.Receipts.Record(ctx, tenantID, "whatsapp", "wamid.p42-3", "delivered", time.Time{}); err != nil {
		t.Fatalf("delivered receipt: %v", err)
	}
	v, _ = s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, "whatsapp")
	if !v.Allowed {
		t.Fatalf("delivered clears the hold: %+v", v)
	}

	// Receipts for non-outreach refs (case replies) record no attribution.
	if _, err := s.Receipts.Record(ctx, tenantID, "whatsapp", "wamid.unrelated", "failed", time.Time{}); err != nil {
		t.Fatalf("unrelated receipt must still record: %v", err)
	}
}

func TestWaSunset42(t *testing.T) {
	now := time.Now()
	calls := 0
	s := testServices(t, Deps{
		Now:         func() time.Time { return now },
		WhatsAppURL: waStubDistinct(t, &calls).URL,
	})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000043", true)

	for i := 0; i < WaSunsetMinSends; i++ {
		if i > 0 && i%OutreachMarketingWeeklyCap == 0 {
			now = now.Add(8 * 24 * time.Hour)
		}
		o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
			ClientID: clientID, Purpose: model.OutreachMarketing, Channel: "whatsapp",
			Body: "offer", IdempotencyKey: fmt.Sprintf("wasun-%d", i),
		})
		if err != nil || o.Status != "sent" {
			t.Fatalf("send %d: %+v %v", i, o, err)
		}
	}

	// Phase 43: the 9th is the win-back touch — the sunset holds, but the
	// exemption grants one touch per 90 days while consent is still on.
	v, err := s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, "whatsapp")
	if err != nil || !v.Allowed || !v.ReengagementTouch {
		t.Fatalf("9th send should be the re-engagement touch: %+v %v", v, err)
	}
	touch, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachMarketing, Channel: "whatsapp",
		Body: "offer", IdempotencyKey: "wasun-touch",
	})
	if err != nil || touch.Status != "sent" || !strings.HasPrefix(touch.Detail, "re-engagement touch") {
		t.Fatalf("touch send: %+v %v", touch, err)
	}
	if calls != 9 {
		t.Fatalf("touch went through the adapter: calls=%d", calls)
	}

	// Within the cooldown the sunset holds for real.
	now = now.Add(8 * 24 * time.Hour) // clear the weekly cap so sunset decides
	v, err = s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, "whatsapp")
	if err != nil || v.Allowed || !strings.Contains(v.Reason, "sunset") {
		t.Fatalf("10th send should sunset-hold: %+v %v", v, err)
	}
	// Service lane untouched.
	if v, _ := s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachService, "whatsapp"); !v.Allowed {
		t.Fatalf("service exempt: %+v", v)
	}

	// One client reply on the WA thread resets the sunset permanently.
	conv, err := s.Conversations.Open(ctx, tenantID, clientID, "whatsapp", "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s.Conversations.AppendMessage(ctx, &model.Message{
		TenantID: tenantID, ConversationID: conv.ID, Author: "CLIENT", Body: "interested!",
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	v, _ = s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, "whatsapp")
	if !v.Allowed || v.ReengagementTouch {
		t.Fatalf("one reply resets the sunset: %+v", v)
	}
}
