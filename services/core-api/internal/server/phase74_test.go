package server_test

// Phase 74 over HTTP: the public pay page behind the pay/ capability
// token (no tenant header — the token is the credential), the honest
// degrade with no live rail, the checkout conflict without a rail, and
// the operator pay-link mint.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestFolioPayPage74(t *testing.T) {
	env := newEnv(t) // no PSP keys — no live rail

	var p model.Partner
	env.do("POST", "/v1/partners", env.tenantID, map[string]any{
		"name": "Pay Palace", "verticals": []string{"CHAUFFEUR_POINT_TO_POINT"},
		"payout_rail": "MANUAL", "status": "VETTED", "kind": "hotel",
	}, http.StatusCreated, &p)
	env.do("PUT", "/v1/partners/"+p.ID+"/bill-to-desk", env.tenantID,
		map[string]any{"enabled": true}, http.StatusOK, &p)
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "name": "Sedan|http74",
		"base_minor": 9000, "currency": "EUR", "per_km_minor": 0,
		"auto_quotable": true, "active": true,
	}, http.StatusCreated, nil)
	var keyRes struct {
		Secret string `json:"secret"`
	}
	env.do("POST", "/v1/partners/"+p.ID+"/keys", env.tenantID,
		map[string]string{"name": "desk-74"}, http.StatusCreated, &keyRes)
	var req model.ServiceRequest
	env.doBearer("POST", "/v1/partner/requests", keyRes.Secret, map[string]any{
		"guest_name": "Pay Guest", "guest_phone": "+31700000074",
		"items": []map[string]any{{
			"vertical": "CHAUFFEUR_POINT_TO_POINT", "pickup_address": "Palace",
			"dropoff_address": "Schiphol", "pickup_at": time.Now().Add(48 * time.Hour).Unix(),
			"passengers": 2,
		}}, "idempotency_key": "pay-http-a",
	}, http.StatusCreated, &req)
	var q model.Quote
	env.do("POST", "/v1/requests/"+req.ID+"/quote", env.tenantID, nil, http.StatusOK, &q)
	env.do("POST", "/v1/quotes/"+q.ID+"/confirm", env.tenantID, nil, http.StatusCreated, nil)
	var folios []model.Folio
	env.do("GET", "/v1/partners/"+p.ID+"/folios", env.tenantID, nil, http.StatusOK, &folios)
	var f model.Folio
	env.do("POST", "/v1/partners/"+p.ID+"/folios/"+folios[0].Period+"/EUR/issue", env.tenantID,
		nil, http.StatusOK, &f)

	// Pay link mints; unknown folio is 404.
	var link struct {
		URL string `json:"url"`
	}
	env.do("GET", "/v1/folios/"+f.ID+"/pay-link", env.tenantID, nil, http.StatusOK, &link)
	if !strings.Contains(link.URL, "/v1/pay/") {
		t.Fatalf("pay link: %s", link.URL)
	}
	env.do("GET", "/v1/folios/nope/pay-link", env.tenantID, nil, http.StatusNotFound, nil)

	// The page renders with no tenant header — and with no live rail it
	// says so honestly instead of faking a checkout.
	code, page := rawGetPublic71(t, env, link.URL)
	if code != http.StatusOK {
		t.Fatalf("pay page: %d", code)
	}
	for _, want := range []string{"Pay statement", "90.00 EUR",
		"not enabled", "bank transfer", "Pay Palace"} {
		if !strings.Contains(page, want) {
			t.Fatalf("page missing %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "Pay now") {
		t.Fatal("no rail but a checkout button rendered")
	}

	// Checkout without a live rail is a conflict, never a fake page.
	env.do("POST", link.URL+"/checkout", env.tenantID, nil, http.StatusConflict, nil)

	// Garbage token never opens a payment page.
	code, _ = rawGetPublic71(t, env, "/v1/pay/garbage")
	if code == http.StatusOK {
		t.Fatal("garbage token opened a pay page")
	}

	// Settled: the page reports PAID.
	env.do("POST", "/v1/folios/"+f.ID+"/settle", env.tenantID, nil, http.StatusOK, &f)
	code, page = rawGetPublic71(t, env, link.URL)
	if code != http.StatusOK || !strings.Contains(page, "PAID") {
		t.Fatalf("settled page: %d\n%s", code, page)
	}
}
