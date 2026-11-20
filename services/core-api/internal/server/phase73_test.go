package server_test

// Phase 73 over HTTP: POST /v1/folios/{id}/credits records a credit
// note against an issued statement (over-credit 409), the public
// statement page renders the credit-note ledger and the adjusted
// totals, and a fully-credited folio settles itself.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestFolioCredits73(t *testing.T) {
	env := newEnv(t)

	var p model.Partner
	env.do("POST", "/v1/partners", env.tenantID, map[string]any{
		"name": "Credit Palace", "verticals": []string{"CHAUFFEUR_POINT_TO_POINT"},
		"payout_rail": "MANUAL", "status": "VETTED", "kind": "hotel",
	}, http.StatusCreated, &p)
	env.do("PUT", "/v1/partners/"+p.ID+"/bill-to-desk", env.tenantID,
		map[string]any{"enabled": true}, http.StatusOK, &p)
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "name": "Sedan|http73",
		"base_minor": 9000, "currency": "EUR", "per_km_minor": 0,
		"auto_quotable": true, "active": true,
	}, http.StatusCreated, nil)
	var keyRes struct {
		Secret string `json:"secret"`
	}
	env.do("POST", "/v1/partners/"+p.ID+"/keys", env.tenantID,
		map[string]string{"name": "desk-73"}, http.StatusCreated, &keyRes)
	var req model.ServiceRequest
	env.doBearer("POST", "/v1/partner/requests", keyRes.Secret, map[string]any{
		"guest_name": "Credit Guest", "guest_phone": "+31700000073",
		"items": []map[string]any{{
			"vertical": "CHAUFFEUR_POINT_TO_POINT", "pickup_address": "Palace",
			"dropoff_address": "Schiphol", "pickup_at": time.Now().Add(48 * time.Hour).Unix(),
			"passengers": 2,
		}}, "idempotency_key": "credit-http-a",
	}, http.StatusCreated, &req)
	var q model.Quote
	env.do("POST", "/v1/requests/"+req.ID+"/quote", env.tenantID, nil, http.StatusOK, &q)
	env.do("POST", "/v1/quotes/"+q.ID+"/confirm", env.tenantID, nil, http.StatusCreated, nil)
	var folios []model.Folio
	env.do("GET", "/v1/partners/"+p.ID+"/folios", env.tenantID, nil, http.StatusOK, &folios)
	var f model.Folio
	env.do("POST", "/v1/partners/"+p.ID+"/folios/"+folios[0].Period+"/EUR/issue", env.tenantID,
		nil, http.StatusOK, &f)

	// Amount must be positive.
	env.do("POST", "/v1/folios/"+f.ID+"/credits", env.tenantID,
		map[string]any{"amount_minor": 0, "reason": "nope"}, http.StatusUnprocessableEntity, nil)

	// Credit 20.00 of the 90.00 statement.
	var f1 model.Folio
	env.do("POST", "/v1/folios/"+f.ID+"/credits", env.tenantID,
		map[string]any{"amount_minor": 2000, "reason": "guest cancelled after issue"},
		http.StatusOK, &f1)
	if len(f1.Credits) != 1 || f1.Credits[0].Kind != "credit" ||
		f1.Credits[0].Reason != "guest cancelled after issue" {
		t.Fatalf("credit row: %+v", f1.Credits)
	}

	// Over-credit is a conflict.
	env.do("POST", "/v1/folios/"+f.ID+"/credits", env.tenantID,
		map[string]any{"amount_minor": 7001, "reason": "too much"}, http.StatusConflict, nil)

	// The statement page shows the credit note and the adjusted totals.
	var link struct {
		URL string `json:"url"`
	}
	env.do("GET", "/v1/folios/"+f.ID+"/statement-link", env.tenantID, nil, http.StatusOK, &link)
	code, page := rawGetPublic71(t, env, link.URL)
	if code != http.StatusOK {
		t.Fatalf("page: %d", code)
	}
	for _, want := range []string{"Credit notes", "guest cancelled after issue",
		"Less credits", "70.00 EUR"} {
		if !strings.Contains(page, want) {
			t.Fatalf("page missing %q:\n%s", want, page)
		}
	}

	// Credit the remaining 70.00 — the folio settles itself.
	var f2 model.Folio
	env.do("POST", "/v1/folios/"+f.ID+"/credits", env.tenantID,
		map[string]any{"amount_minor": 7000, "reason": "full goodwill"}, http.StatusOK, &f2)
	if f2.State != "SETTLED" || len(f2.Credits) != 2 {
		t.Fatalf("credit-settled: %+v", f2)
	}

	// A refund against the settled folio is a 409 — nothing was ever paid.
	env.do("POST", "/v1/folios/"+f.ID+"/credits", env.tenantID,
		map[string]any{"amount_minor": 100, "reason": "no money to return"}, http.StatusConflict, nil)

	// Unknown folio stays 404.
	env.do("POST", "/v1/folios/nope/credits", env.tenantID,
		map[string]any{"amount_minor": 100}, http.StatusNotFound, nil)
}
