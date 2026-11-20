package server_test

// Phase 70 over HTTP: recording partial payments against an issued
// folio — verbatim ledger, overpayment rejection, auto-settlement at
// zero, and Settle recording only the remainder.

import (
	"net/http"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestFolioPayments70(t *testing.T) {
	env := newEnv(t)

	var p model.Partner
	env.do("POST", "/v1/partners", env.tenantID, map[string]any{
		"name": "Payments Grand", "verticals": []string{"CHAUFFEUR_POINT_TO_POINT"},
		"payout_rail": "MANUAL", "status": "VETTED", "kind": "hotel",
	}, http.StatusCreated, &p)
	env.do("PUT", "/v1/partners/"+p.ID+"/bill-to-desk", env.tenantID,
		map[string]any{"enabled": true}, http.StatusOK, &p)
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "name": "Sedan|http70",
		"base_minor": 9000, "currency": "EUR", "per_km_minor": 0,
		"auto_quotable": true, "active": true,
	}, http.StatusCreated, nil)
	var keyRes struct {
		Secret string `json:"secret"`
	}
	env.do("POST", "/v1/partners/"+p.ID+"/keys", env.tenantID,
		map[string]string{"name": "desk-70"}, http.StatusCreated, &keyRes)
	var req model.ServiceRequest
	env.doBearer("POST", "/v1/partner/requests", keyRes.Secret, map[string]any{
		"guest_name": "Pay Guest", "guest_phone": "+31700000070",
		"items": []map[string]any{{
			"vertical": "CHAUFFEUR_POINT_TO_POINT", "pickup_address": "Grand",
			"dropoff_address": "Schiphol", "pickup_at": time.Now().Add(48 * time.Hour).Unix(),
			"passengers": 2,
		}}, "idempotency_key": "pay-http-a",
	}, http.StatusCreated, &req)
	var q model.Quote
	env.do("POST", "/v1/requests/"+req.ID+"/quote", env.tenantID, nil, http.StatusOK, &q)
	env.do("POST", "/v1/quotes/"+q.ID+"/confirm", env.tenantID, nil, http.StatusCreated, nil)

	var folios []model.Folio
	env.do("GET", "/v1/partners/"+p.ID+"/folios", env.tenantID, nil, http.StatusOK, &folios)
	if len(folios) != 1 {
		t.Fatalf("folios: %+v", folios)
	}
	var f model.Folio
	env.do("POST", "/v1/partners/"+p.ID+"/folios/"+folios[0].Period+"/EUR/issue", env.tenantID,
		nil, http.StatusOK, &f)

	// Partial payment over HTTP: recorded verbatim, still ISSUED.
	env.do("POST", "/v1/folios/"+f.ID+"/payments", env.tenantID,
		map[string]any{"amount_minor": 4000, "method": "bank_transfer", "reference": "TRX-42"},
		http.StatusOK, &f)
	if f.State != "ISSUED" || len(f.Payments) != 1 || f.Payments[0].AmountMinor != 4000 ||
		f.Payments[0].Reference != "TRX-42" {
		t.Fatalf("partial: %+v", f)
	}

	// Overpayment → 409; non-positive → 400; unknown folio → 404.
	env.do("POST", "/v1/folios/"+f.ID+"/payments", env.tenantID,
		map[string]any{"amount_minor": 5001, "method": "cash"}, http.StatusConflict, nil)
	env.do("POST", "/v1/folios/"+f.ID+"/payments", env.tenantID,
		map[string]any{"amount_minor": 0, "method": "cash"}, http.StatusUnprocessableEntity, nil)
	env.do("POST", "/v1/folios/00000000-0000-0000-0000-000000000000/payments", env.tenantID,
		map[string]any{"amount_minor": 100, "method": "cash"}, http.StatusNotFound, nil)

	// Aging reflects the outstanding balance (5000), not the face value.
	var report model.FolioAgingReport
	env.do("GET", "/v1/folios/aging", env.tenantID, nil, http.StatusOK, &report)
	if len(report.Rows) != 1 || report.Rows[0].TotalMinor != 5000 ||
		report.Rows[0].CollectedMinor != 4000 {
		t.Fatalf("aging after partial: %+v", report)
	}

	// Final payment to zero: the folio settles itself over HTTP.
	env.do("POST", "/v1/folios/"+f.ID+"/payments", env.tenantID,
		map[string]any{"amount_minor": 5000, "method": "bank_transfer", "reference": "TRX-43"},
		http.StatusOK, &f)
	if f.State != "SETTLED" || len(f.Payments) != 2 {
		t.Fatalf("auto-settled: %+v", f)
	}
	env.do("POST", "/v1/folios/"+f.ID+"/payments", env.tenantID,
		map[string]any{"amount_minor": 100, "method": "cash"}, http.StatusConflict, nil)
	env.do("GET", "/v1/folios/aging", env.tenantID, nil, http.StatusOK, &report)
	if len(report.Rows) != 0 {
		t.Fatalf("settled must leave aging: %+v", report)
	}

	// Settle after a partial (second partner): records only the remainder.
	var p2 model.Partner
	env.do("POST", "/v1/partners", env.tenantID, map[string]any{
		"name": "Remainder Grand", "verticals": []string{"CHAUFFEUR_POINT_TO_POINT"},
		"payout_rail": "MANUAL", "status": "VETTED", "kind": "hotel",
	}, http.StatusCreated, &p2)
	env.do("PUT", "/v1/partners/"+p2.ID+"/bill-to-desk", env.tenantID,
		map[string]any{"enabled": true}, http.StatusOK, &p2)
	env.do("POST", "/v1/partners/"+p2.ID+"/keys", env.tenantID,
		map[string]string{"name": "desk-70b"}, http.StatusCreated, &keyRes)
	env.doBearer("POST", "/v1/partner/requests", keyRes.Secret, map[string]any{
		"guest_name": "Pay Guest Two", "guest_phone": "+31700000071",
		"items": []map[string]any{{
			"vertical": "CHAUFFEUR_POINT_TO_POINT", "pickup_address": "Grand",
			"dropoff_address": "Schiphol", "pickup_at": time.Now().Add(48 * time.Hour).Unix(),
			"passengers": 1,
		}}, "idempotency_key": "pay-http-b",
	}, http.StatusCreated, &req)
	env.do("POST", "/v1/requests/"+req.ID+"/quote", env.tenantID, nil, http.StatusOK, &q)
	env.do("POST", "/v1/quotes/"+q.ID+"/confirm", env.tenantID, nil, http.StatusCreated, nil)
	env.do("GET", "/v1/partners/"+p2.ID+"/folios", env.tenantID, nil, http.StatusOK, &folios)
	env.do("POST", "/v1/partners/"+p2.ID+"/folios/"+folios[0].Period+"/EUR/issue", env.tenantID,
		nil, http.StatusOK, &f)
	env.do("POST", "/v1/folios/"+f.ID+"/payments", env.tenantID,
		map[string]any{"amount_minor": 2500, "method": "cash"}, http.StatusOK, &f)
	env.do("POST", "/v1/folios/"+f.ID+"/settle", env.tenantID, nil, http.StatusOK, &f)
	if f.State != "SETTLED" || len(f.Payments) != 2 ||
		f.Payments[1].Method != "settle" || f.Payments[1].AmountMinor != 6500 {
		t.Fatalf("settle remainder: %+v", f.Payments)
	}
}
