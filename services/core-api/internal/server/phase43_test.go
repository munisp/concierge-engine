package server_test

// Phase 43 server tests: the public reopt/ re-subscribe endpoint
// (token-authenticated, tenant-header exempt) and the re-engagement verdict
// flag over HTTP.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/model"
)

func TestReoptEndpoint43(t *testing.T) {
	env := newEnvDeps(t, domain.Deps{TripSecret: "test-secret", PublicBase: "https://core.example"})

	var rr domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID, map[string]any{
		"phone": "+971500000099", "email": "vp.travel@acme.example", "name": "Pat Buyer",
	}, http.StatusOK, &rr)
	cl := *rr.Client
	// Consent off — the reopt link is the way back.
	env.do("PUT", "/v1/clients/"+cl.ID+"/consent", env.tenantID, map[string]any{
		"scope": "marketing", "granted": false,
	}, http.StatusOK, nil)

	tok := env.svc.Trips.ReoptTokenFor(env.tenantID, cl.ID)

	// No tenant header — the token is the credential (tenantctx-exempt).
	req, _ := http.NewRequest("GET", env.server.URL+"/v1/reopt/"+tok, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("reopt status %d", res.StatusCode)
	}
	buf := make([]byte, 4096)
	n, _ := res.Body.Read(buf)
	body := string(buf[:n])
	if !strings.Contains(body, "back on the list") || !strings.Contains(body, "Taylor Travel Management") {
		t.Fatalf("confirmation page: %q", body)
	}

	// Consent flipped; marketing verdict opens.
	var v domain.OutreachVerdict
	env.do("GET", "/v1/outreach/verdict?client_id="+cl.ID+"&purpose=marketing&channel=email", env.tenantID,
		nil, http.StatusOK, &v)
	if !v.Allowed {
		t.Fatalf("verdict after reopt: %+v", v)
	}

	// Idempotent replay reports already-subscribed.
	req2, _ := http.NewRequest("GET", env.server.URL+"/v1/reopt/"+tok, nil)
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	n2, _ := res2.Body.Read(buf)
	if res2.StatusCode != http.StatusOK || !strings.Contains(string(buf[:n2]), "already switched on") {
		t.Fatalf("replay: status %d body %q", res2.StatusCode, string(buf[:n2]))
	}

	// Tampered and cross-family tokens are refused.
	req3, _ := http.NewRequest("GET", env.server.URL+"/v1/reopt/"+tok[:len(tok)-2]+"xx", nil)
	res3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	res3.Body.Close()
	if res3.StatusCode != http.StatusForbidden && res3.StatusCode != http.StatusBadRequest {
		t.Fatalf("tampered token: status %d", res3.StatusCode)
	}
	req4, _ := http.NewRequest("GET", env.server.URL+"/v1/reopt/"+env.svc.Trips.UnsubTokenFor(env.tenantID, cl.ID), nil)
	res4, err := http.DefaultClient.Do(req4)
	if err != nil {
		t.Fatal(err)
	}
	res4.Body.Close()
	if res4.StatusCode != http.StatusForbidden {
		t.Fatalf("unsub token must not reopt: status %d", res4.StatusCode)
	}
}

func TestReengagementVerdictEndpoint43(t *testing.T) {
	env := newEnvDeps(t, domain.Deps{TripSecret: "test-secret", PublicBase: "https://core.example"})

	var rr domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID, map[string]any{
		"phone": "+971500000045", "name": "Quiet Client",
	}, http.StatusOK, &rr)
	cl := *rr.Client
	env.do("PUT", "/v1/clients/"+cl.ID+"/consent", env.tenantID, map[string]any{
		"scope": "marketing", "granted": true,
	}, http.StatusOK, nil)

	// Sunset-level history, no engagement.
	for i := 0; i < domain.WaSunsetMinSends; i++ {
		if err := env.svc.Store.Outreach().AddSend(context.Background(), &model.OutreachSend{
			ID: fmt.Sprintf("re43-%d", i), TenantID: env.tenantID, ClientID: cl.ID,
			Channel: "whatsapp", Purpose: "marketing", Status: "sent",
			Body: "offer", IdempotencyKey: fmt.Sprintf("re43-%d", i),
		}); err != nil {
			t.Fatalf("seed send %d: %v", i, err)
		}
	}

	var v domain.OutreachVerdict
	env.do("GET", "/v1/outreach/verdict?client_id="+cl.ID+"&purpose=marketing&channel=whatsapp", env.tenantID,
		nil, http.StatusOK, &v)
	if !v.Allowed || !v.ReengagementTouch {
		t.Fatalf("first sunset verdict grants the touch: %+v", v)
	}

	// The granted touch on the ledger (sent) engages the cooldown.
	if err := env.svc.Store.Outreach().AddSend(context.Background(), &model.OutreachSend{
		ID: "re43-touch", TenantID: env.tenantID, ClientID: cl.ID,
		Channel: "whatsapp", Purpose: "marketing", Status: "sent",
		Body: "offer", Detail: "re-engagement touch — sunset-held client, one touch per 90d",
		IdempotencyKey: "re43-touch", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed touch: %v", err)
	}
	env.do("GET", "/v1/outreach/verdict?client_id="+cl.ID+"&purpose=marketing&channel=whatsapp", env.tenantID,
		nil, http.StatusOK, &v)
	if v.Allowed || !strings.Contains(v.Reason, "sunset") {
		t.Fatalf("cooldown should hold: %+v", v)
	}
}
