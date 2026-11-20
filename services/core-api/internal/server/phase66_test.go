package server_test

// Phase 66 over HTTP: bill-to-desk toggle, folio accrual on sourced
// confirms, issue/settle lifecycle, and statement retrieval.

import (
	"net/http"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestFolios66(t *testing.T) {
	env := newEnv(t)

	var p model.Partner
	env.do("POST", "/v1/partners", env.tenantID, map[string]any{
		"name": "Desk Bill Grand", "verticals": []string{"CHAUFFEUR_POINT_TO_POINT"},
		"payout_rail": "MANUAL", "status": "VETTED", "kind": "hotel",
	}, http.StatusCreated, &p)
	if p.BillToDesk {
		t.Fatal("bill_to_desk defaults off")
	}

	// Toggle on; the flag round-trips.
	env.do("PUT", "/v1/partners/"+p.ID+"/bill-to-desk", env.tenantID,
		map[string]any{"enabled": true}, http.StatusOK, &p)
	if !p.BillToDesk {
		t.Fatal("toggle did not stick")
	}
	env.do("PUT", "/v1/partners/00000000-0000-0000-0000-000000000000/bill-to-desk", env.tenantID,
		map[string]any{"enabled": true}, http.StatusNotFound, nil)

	// Catalog + partner key + two sourced bookings.
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "name": "Sedan|http66",
		"base_minor": 9000, "currency": "EUR", "per_km_minor": 0,
		"auto_quotable": true, "active": true,
	}, http.StatusCreated, nil)
	var keyRes struct {
		Secret string `json:"secret"`
	}
	env.do("POST", "/v1/partners/"+p.ID+"/keys", env.tenantID,
		map[string]string{"name": "desk-66"}, http.StatusCreated, &keyRes)
	item := map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "pickup_address": "Grand",
		"dropoff_address": "Schiphol", "pickup_at": time.Now().Add(48 * time.Hour).Unix(),
		"passengers": 2,
	}
	confirmOne := func(idem, phone string) {
		var req model.ServiceRequest
		env.doBearer("POST", "/v1/partner/requests", keyRes.Secret, map[string]any{
			"guest_name": "Desk Guest", "guest_phone": phone,
			"items": []map[string]any{item}, "idempotency_key": idem,
		}, http.StatusCreated, &req)
		var q model.Quote
		env.do("POST", "/v1/requests/"+req.ID+"/quote", env.tenantID, nil, http.StatusOK, &q)
		env.do("POST", "/v1/quotes/"+q.ID+"/confirm", env.tenantID, nil, http.StatusCreated, nil)
	}
	confirmOne("fl-a", "+31700000001")
	confirmOne("fl-b", "+31700000002")

	// Folio accrued: one OPEN folio for the month, two lines, EUR 180.00.
	var folios []model.Folio
	env.do("GET", "/v1/partners/"+p.ID+"/folios", env.tenantID, nil, http.StatusOK, &folios)
	if len(folios) != 1 {
		t.Fatalf("folios: %+v", folios)
	}
	f := folios[0]
	if f.State != "OPEN" || f.TotalMinor != 18000 || f.Currency != "EUR" || len(f.Lines) != 2 {
		t.Fatalf("folio: %+v", f)
	}
	period := f.Period

	// Issue: freezes and becomes payable; replay converges.
	env.do("POST", "/v1/partners/"+p.ID+"/folios/"+period+"/EUR/issue", env.tenantID,
		nil, http.StatusOK, &f)
	if f.State != "ISSUED" || f.TotalMinor != 18000 {
		t.Fatalf("issued: %+v", f)
	}
	env.do("POST", "/v1/partners/"+p.ID+"/folios/"+period+"/EUR/issue", env.tenantID,
		nil, http.StatusOK, &f)
	env.do("POST", "/v1/partners/"+p.ID+"/folios/2025-01/EUR/issue", env.tenantID,
		nil, http.StatusNotFound, nil)
	env.do("POST", "/v1/partners/"+p.ID+"/folios/badperiod/EUR/issue", env.tenantID,
		nil, http.StatusUnprocessableEntity, nil)

	// Statement view carries the lines.
	var full model.Folio
	env.do("GET", "/v1/folios/"+f.ID, env.tenantID, nil, http.StatusOK, &full)
	if len(full.Lines) != 2 || full.Lines[0].GuestName != "Desk Guest" {
		t.Fatalf("statement: %+v", full)
	}

	// Settle: ledger posted, terminal, idempotent.
	env.do("POST", "/v1/folios/"+f.ID+"/settle", env.tenantID, nil, http.StatusOK, &full)
	if full.State != "SETTLED" || full.SettledAt.IsZero() {
		t.Fatalf("settled: %+v", full)
	}
	env.do("POST", "/v1/folios/"+f.ID+"/settle", env.tenantID, nil, http.StatusOK, &full)
	if full.State != "SETTLED" {
		t.Fatalf("settle replay: %+v", full)
	}

	// Post-settle accrual for the same period is blocked with provenance —
	// the booking is real, the folio stays frozen, the operator bills it.
	confirmOne("fl-c", "+31700000003")
	env.do("GET", "/v1/folios/"+f.ID, env.tenantID, nil, http.StatusOK, &full)
	if full.TotalMinor != 18000 || len(full.Lines) != 2 {
		t.Fatalf("settled folio frozen: %+v", full)
	}
}
