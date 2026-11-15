package server_test

// Phase 41 server tests: the suppression-list endpoints (create / replay /
// list / delete / validation) and a send held against a suppressed address
// over HTTP.

import (
	"net/http"
	"testing"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/model"
)

func TestEmailSuppressionEndpoints41(t *testing.T) {
	env := newEnv(t)

	// Validation.
	env.do("POST", "/v1/email/suppressions", env.tenantID, map[string]any{
		"email": "not-an-email", "reason": "manual",
	}, http.StatusUnprocessableEntity, nil)
	env.do("POST", "/v1/email/suppressions", env.tenantID, map[string]any{
		"email": "a@b.c", "reason": "soft_bounce",
	}, http.StatusUnprocessableEntity, nil)

	// Create (normalizes case/whitespace).
	var row model.EmailSuppression
	env.do("POST", "/v1/email/suppressions", env.tenantID, map[string]any{
		"email": " Dead.Address@acme.example ", "reason": "hard_bounce", "source_ref": "<msg-1@mail.taylor.example>",
	}, http.StatusCreated, &row)
	if row.Email != "dead.address@acme.example" || row.Reason != "hard_bounce" {
		t.Fatalf("row: %+v", row)
	}

	// Idempotent replay returns 200 with the original row.
	var replay model.EmailSuppression
	env.do("POST", "/v1/email/suppressions", env.tenantID, map[string]any{
		"email": "dead.address@acme.example", "reason": "hard_bounce",
	}, http.StatusOK, &replay)
	if replay.ID != row.ID {
		t.Fatalf("replay must return the original: %+v vs %+v", replay, row)
	}

	// List.
	var list struct {
		Suppressions []model.EmailSuppression `json:"suppressions"`
	}
	env.do("GET", "/v1/email/suppressions", env.tenantID, nil, http.StatusOK, &list)
	if len(list.Suppressions) != 1 {
		t.Fatalf("list: %+v", list)
	}

	// Delete (operator recovery), then 404 on a second delete.
	env.do("DELETE", "/v1/email/suppressions/dead.address@acme.example", env.tenantID, nil, http.StatusOK, nil)
	env.do("DELETE", "/v1/email/suppressions/dead.address@acme.example", env.tenantID, nil, http.StatusNotFound, nil)
}

func TestSendHeldForSuppressedAddress41(t *testing.T) {
	env := newEnv(t)

	var rr domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID, map[string]any{
		"phone": "+971500000077", "email": "gone@acme.example", "name": "Gone Client",
	}, http.StatusOK, &rr)
	cl := *rr.Client
	env.do("PUT", "/v1/clients/"+cl.ID+"/consent", env.tenantID, map[string]any{
		"scope": "marketing", "granted": true,
	}, http.StatusOK, nil)

	// The ESP reports the address dead.
	env.do("POST", "/v1/email/suppressions", env.tenantID, map[string]any{
		"email": "gone@acme.example", "reason": "hard_bounce", "source_ref": "<bounce-1@esp>",
	}, http.StatusCreated, nil)

	// Verdict reports the suppression…
	var v domain.OutreachVerdict
	env.do("GET", "/v1/outreach/verdict?client_id="+cl.ID+"&purpose=marketing&channel=email", env.tenantID,
		nil, http.StatusOK, &v)
	if v.Allowed || !v.EmailSuppressed {
		t.Fatalf("verdict: %+v", v)
	}

	// …and a send is held, not delivered.
	var send model.OutreachSend
	env.do("POST", "/v1/outreach/send", env.tenantID, map[string]any{
		"client_id": cl.ID, "purpose": "marketing", "channel": "email",
		"subject": "Offer", "body": "offer", "idempotency_key": "supp-srv-1",
	}, http.StatusCreated, &send)
	if send.Status != "held" {
		t.Fatalf("send should be held: %+v", send)
	}

	// WhatsApp verdict for the same client is unaffected.
	env.do("GET", "/v1/outreach/verdict?client_id="+cl.ID+"&purpose=marketing&channel=whatsapp", env.tenantID,
		nil, http.StatusOK, &v)
	if !v.Allowed {
		t.Fatalf("whatsapp verdict: %+v", v)
	}
}
