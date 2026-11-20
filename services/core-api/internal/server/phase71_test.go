package server_test

// Phase 71 over HTTP: the public printable statement page behind the
// stmt/ capability token (no tenant header — the token is the
// credential), the operator send-statement action with the verbatim
// deliveries ledger, and the statement-link mint.

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// rawGetPublic71 fetches a path with no tenant/api headers at all — the
// HMAC token in the URL is the only credential.
func rawGetPublic71(t *testing.T, env *testEnv, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(env.server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestStatementDelivery71(t *testing.T) {
	env := newEnv(t)

	var p model.Partner
	env.do("POST", "/v1/partners", env.tenantID, map[string]any{
		"name": "Statement Grand", "verticals": []string{"CHAUFFEUR_POINT_TO_POINT"},
		"payout_rail": "MANUAL", "status": "VETTED", "kind": "hotel",
		"email": "desk@statementgrand.example.com",
	}, http.StatusCreated, &p)
	if p.Email != "desk@statementgrand.example.com" {
		t.Fatalf("partner email persisted: %+v", p)
	}
	env.do("PUT", "/v1/partners/"+p.ID+"/bill-to-desk", env.tenantID,
		map[string]any{"enabled": true}, http.StatusOK, &p)
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "name": "Sedan|http71",
		"base_minor": 9000, "currency": "EUR", "per_km_minor": 0,
		"auto_quotable": true, "active": true,
	}, http.StatusCreated, nil)
	var keyRes struct {
		Secret string `json:"secret"`
	}
	env.do("POST", "/v1/partners/"+p.ID+"/keys", env.tenantID,
		map[string]string{"name": "desk-71"}, http.StatusCreated, &keyRes)
	var req model.ServiceRequest
	env.doBearer("POST", "/v1/partner/requests", keyRes.Secret, map[string]any{
		"guest_name": "Statement Guest", "guest_phone": "+31700000072",
		"items": []map[string]any{{
			"vertical": "CHAUFFEUR_POINT_TO_POINT", "pickup_address": "Grand",
			"dropoff_address": "Schiphol", "pickup_at": time.Now().Add(48 * time.Hour).Unix(),
			"passengers": 2,
		}}, "idempotency_key": "stmt-http-a",
	}, http.StatusCreated, &req)
	var q model.Quote
	env.do("POST", "/v1/requests/"+req.ID+"/quote", env.tenantID, nil, http.StatusOK, &q)
	env.do("POST", "/v1/quotes/"+q.ID+"/confirm", env.tenantID, nil, http.StatusCreated, nil)
	var folios []model.Folio
	env.do("GET", "/v1/partners/"+p.ID+"/folios", env.tenantID, nil, http.StatusOK, &folios)
	var f model.Folio
	env.do("POST", "/v1/partners/"+p.ID+"/folios/"+folios[0].Period+"/EUR/issue", env.tenantID,
		nil, http.StatusOK, &f)

	// Statement link mints; the page renders with no tenant header at
	// all (tenantctx exempts /v1/statement/).
	var link struct {
		URL string `json:"url"`
	}
	env.do("GET", "/v1/folios/"+f.ID+"/statement-link", env.tenantID, nil, http.StatusOK, &link)
	if !strings.Contains(link.URL, "/v1/statement/") {
		t.Fatalf("statement link: %s", link.URL)
	}
	code, body := rawGetPublic71(t, env, link.URL[strings.Index(link.URL, "/v1/"):])
	if code != http.StatusOK {
		t.Fatalf("public page: %d %s", code, body)
	}
	for _, want := range []string{"Statement", "Statement Grand", "90.00 EUR", "DUE", "Charges", "save this page as PDF"} {
		if !strings.Contains(body, want) {
			t.Fatalf("statement page missing %q:\n%s", want, body)
		}
	}

	// A garbage token is refused, not rendered.
	if code, _ := rawGetPublic71(t, env, "/v1/statement/garbage.token"); code == http.StatusOK {
		t.Fatal("garbage token must not render a statement")
	}

	// Send-statement: no phone, no adapter — the email lane records an
	// honest skip verbatim (email on file but CE_EMAIL_ADAPTER_URL unset).
	env.do("POST", "/v1/folios/"+f.ID+"/send-statement", env.tenantID, nil, http.StatusOK, &f)
	if len(f.Deliveries) != 1 || f.Deliveries[0].Channel != "email" ||
		f.Deliveries[0].Status != "skipped:no-adapter" || f.Deliveries[0].Kind != "statement" {
		t.Fatalf("delivery ledger: %+v", f.Deliveries)
	}

	// State guards over HTTP: settle → 409 on send; unknown folio → 404.
	env.do("POST", "/v1/folios/"+f.ID+"/settle", env.tenantID, nil, http.StatusOK, &f)
	env.do("POST", "/v1/folios/"+f.ID+"/send-statement", env.tenantID, nil, http.StatusConflict, nil)
	env.do("POST", "/v1/folios/00000000-0000-0000-0000-000000000000/send-statement", env.tenantID,
		nil, http.StatusNotFound, nil)
	env.do("GET", "/v1/folios/00000000-0000-0000-0000-000000000000/statement-link", env.tenantID,
		nil, http.StatusNotFound, nil)

	// The settled page still resolves, marked PAID.
	code, body = rawGetPublic71(t, env, link.URL[strings.Index(link.URL, "/v1/"):])
	if code != http.StatusOK || !strings.Contains(body, "PAID") || !strings.Contains(body, "Paid in full") {
		t.Fatalf("settled page: %d %s", code, body)
	}
}
