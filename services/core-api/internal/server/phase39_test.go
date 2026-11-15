package server_test

// Phase 39 server tests: the email lane over HTTP and the public
// unsubscribe endpoint (token-authenticated, tenant-header exempt).

import (
	"net/http"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/model"
)

func TestOutreachSendEmailChannel39(t *testing.T) {
	env := newEnv(t)

	var rr domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID, map[string]any{
		"phone": "+971500000099", "email": "vp.travel@acme.example", "name": "Pat Buyer",
	}, http.StatusOK, &rr)
	cl := *rr.Client

	// Email send without subject → 422.
	env.do("POST", "/v1/outreach/send", env.tenantID, map[string]any{
		"client_id": cl.ID, "purpose": "service", "channel": "email",
		"body": "update", "idempotency_key": "srv-e1",
	}, http.StatusUnprocessableEntity, nil)

	// Happy path: recorded on the email lane, honest degrade without adapter.
	var send model.OutreachSend
	env.do("POST", "/v1/outreach/send", env.tenantID, map[string]any{
		"client_id": cl.ID, "purpose": "service", "channel": "email",
		"subject": "Chauffeur assigned", "body": "James is assigned.",
		"idempotency_key": "srv-e2",
	}, http.StatusCreated, &send)
	if send.Channel != "email" || send.Subject != "Chauffeur assigned" {
		t.Fatalf("send: %+v", send)
	}
	if send.Status != "failed" || !strings.Contains(send.Detail, "CE_EMAIL_ADAPTER_URL") {
		t.Fatalf("degrade: %+v", send)
	}

	// Verdict endpoint understands the channel parameter.
	var v map[string]any
	env.do("GET", "/v1/outreach/verdict?client_id="+cl.ID+"&purpose=marketing&channel=email", env.tenantID,
		nil, http.StatusOK, &v)
	if v["marketing_consent"] != false {
		t.Fatalf("verdict: %v", v)
	}
}

func TestUnsubscribeEndpoint39(t *testing.T) {
	env := newEnvDeps(t, domain.Deps{TripSecret: "test-secret", PublicBase: "https://core.example"})

	var rr domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID, map[string]any{
		"phone": "+971500000099", "email": "vp.travel@acme.example", "name": "Pat Buyer",
	}, http.StatusOK, &rr)
	cl := *rr.Client
	env.do("PUT", "/v1/clients/"+cl.ID+"/consent", env.tenantID, map[string]any{
		"scope": "marketing", "granted": true,
	}, http.StatusOK, nil)

	// Mint the token the way Send does (same family, same secret).
	tok := env.svc.Trips.UnsubTokenFor(env.tenantID, cl.ID)

	// No tenant header — the token is the credential (tenantctx-exempt).
	req, _ := http.NewRequest("GET", env.server.URL+"/v1/unsub/"+tok, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unsub status %d", res.StatusCode)
	}
	buf := make([]byte, 4096)
	n, _ := res.Body.Read(buf)
	body := string(buf[:n])
	if !strings.Contains(body, "unsubscribed") || !strings.Contains(body, "Taylor Travel Management") {
		t.Fatalf("confirmation page: %q", body)
	}

	// Consent flipped; marketing email now held.
	var v map[string]any
	env.do("GET", "/v1/outreach/verdict?client_id="+cl.ID+"&purpose=marketing&channel=email", env.tenantID,
		nil, http.StatusOK, &v)
	if v["allowed"] != false {
		t.Fatalf("verdict after unsub: %v", v)
	}

	// Tampered token refused.
	req2, _ := http.NewRequest("GET", env.server.URL+"/v1/unsub/"+tok[:len(tok)-2]+"xx", nil)
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusForbidden {
		t.Fatalf("tampered token status %d", res2.StatusCode)
	}
}
