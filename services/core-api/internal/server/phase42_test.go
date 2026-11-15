package server_test

// Phase 42 server tests: WhatsApp hygiene over HTTP — a dead number holds
// the marketing lane (wa_unreachable on the verdict), an unrelated receipt
// is harmless, and a client reply over the conversation API resets the WA
// sunset.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/model"
)

func TestWaUnreachableVerdict42(t *testing.T) {
	env := newEnv(t)

	var rr domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID, map[string]any{
		"phone": "+971500000042", "name": "WA Client",
	}, http.StatusOK, &rr)
	cl := *rr.Client
	env.do("PUT", "/v1/clients/"+cl.ID+"/consent", env.tenantID, map[string]any{
		"scope": "marketing", "granted": true,
	}, http.StatusOK, nil)

	// Baseline: allowed.
	var v domain.OutreachVerdict
	env.do("GET", "/v1/outreach/verdict?client_id="+cl.ID+"&purpose=marketing&channel=whatsapp", env.tenantID,
		nil, http.StatusOK, &v)
	if !v.Allowed {
		t.Fatalf("baseline: %+v", v)
	}

	// Three failed-delivery outcomes land on the attribution spine (the
	// receipt→send correlation itself is covered in domain + pg tests).
	for i := 0; i < 3; i++ {
		env.svc.Events.Track(context.Background(), &model.AttributionEvent{
			TenantID: env.tenantID, Kind: "wa_failed", Channel: "whatsapp",
			ClientID: cl.ID, Campaign: "marketing",
		})
	}

	env.do("GET", "/v1/outreach/verdict?client_id="+cl.ID+"&purpose=marketing&channel=whatsapp", env.tenantID,
		nil, http.StatusOK, &v)
	if v.Allowed || !v.WaUnreachable || !strings.Contains(v.Reason, "unreachable") {
		t.Fatalf("should hold unreachable: %+v", v)
	}

	// An unrelated receipt (a case reply, not an outreach send) is recorded
	// and harmless.
	env.do("POST", "/v1/channels/receipts", env.tenantID, map[string]any{
		"channel": "whatsapp", "external_ref": "wamid.unrelated", "status": "failed",
	}, http.StatusCreated, nil)

	// A confirmed delivery after the failures clears the hold.
	env.svc.Events.Track(context.Background(), &model.AttributionEvent{
		TenantID: env.tenantID, Kind: "wa_delivered", Channel: "whatsapp",
		ClientID: cl.ID,
	})
	env.do("GET", "/v1/outreach/verdict?client_id="+cl.ID+"&purpose=marketing&channel=whatsapp", env.tenantID,
		nil, http.StatusOK, &v)
	if !v.Allowed {
		t.Fatalf("delivered clears the hold: %+v", v)
	}
}

func TestWaSunsetReplyReset42(t *testing.T) {
	env := newEnv(t)

	var rr domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID, map[string]any{
		"phone": "+971500000043", "name": "Quiet Client",
	}, http.StatusOK, &rr)
	cl := *rr.Client
	env.do("PUT", "/v1/clients/"+cl.ID+"/consent", env.tenantID, map[string]any{
		"scope": "marketing", "granted": true,
	}, http.StatusOK, nil)

	// 8 all-time marketing WA sends, none replied to (recorded directly —
	// the send cadence is covered in the domain sunset test).
	for i := 0; i < domain.WaSunsetMinSends; i++ {
		if err := env.svc.Store.Outreach().AddSend(context.Background(), &model.OutreachSend{
			ID: fmt.Sprintf("send-%d", i), TenantID: env.tenantID, ClientID: cl.ID,
			Channel: "whatsapp", Purpose: "marketing", Status: "sent",
			Body: "offer", IdempotencyKey: fmt.Sprintf("wa42-srv-%d", i),
		}); err != nil {
			t.Fatalf("seed send %d: %v", i, err)
		}
	}

	var v domain.OutreachVerdict
	env.do("GET", "/v1/outreach/verdict?client_id="+cl.ID+"&purpose=marketing&channel=whatsapp", env.tenantID,
		nil, http.StatusOK, &v)
	if v.Allowed || !strings.Contains(v.Reason, "sunset") {
		t.Fatalf("should sunset: %+v", v)
	}

	// The client replies on their WA thread → sunset resets.
	var conv model.Conversation
	env.do("POST", "/v1/conversations", env.tenantID, map[string]any{
		"client_id": cl.ID, "channel": "whatsapp",
	}, http.StatusCreated, &conv)
	env.do("POST", "/v1/conversations/"+conv.ID+"/messages", env.tenantID, map[string]any{
		"author": "CLIENT", "body": "tell me more",
	}, http.StatusCreated, nil)

	env.do("GET", "/v1/outreach/verdict?client_id="+cl.ID+"&purpose=marketing&channel=whatsapp", env.tenantID,
		nil, http.StatusOK, &v)
	if !v.Allowed {
		t.Fatalf("reply resets the sunset: %+v", v)
	}
}
