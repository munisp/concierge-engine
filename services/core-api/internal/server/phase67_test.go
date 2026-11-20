package server_test

// Phase 67 over HTTP: the derived aging view, the on-demand dunning run,
// and the manual reminder with its 24h guard.

import (
	"net/http"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestFolioAgingDunning67(t *testing.T) {
	env := newEnv(t)

	var p model.Partner
	env.do("POST", "/v1/partners", env.tenantID, map[string]any{
		"name": "Dunning Grand", "verticals": []string{"CHAUFFEUR_POINT_TO_POINT"},
		"payout_rail": "MANUAL", "status": "VETTED", "kind": "hotel", "phone": "+31695550777",
	}, http.StatusCreated, &p)
	env.do("PUT", "/v1/partners/"+p.ID+"/bill-to-desk", env.tenantID,
		map[string]any{"enabled": true}, http.StatusOK, &p)

	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "name": "Sedan|http67",
		"base_minor": 9000, "currency": "EUR", "per_km_minor": 0,
		"auto_quotable": true, "active": true,
	}, http.StatusCreated, nil)
	var keyRes struct {
		Secret string `json:"secret"`
	}
	env.do("POST", "/v1/partners/"+p.ID+"/keys", env.tenantID,
		map[string]string{"name": "desk-67"}, http.StatusCreated, &keyRes)
	var req model.ServiceRequest
	env.doBearer("POST", "/v1/partner/requests", keyRes.Secret, map[string]any{
		"guest_name": "Desk Guest", "guest_phone": "+31700000067",
		"items": []map[string]any{{
			"vertical": "CHAUFFEUR_POINT_TO_POINT", "pickup_address": "Grand",
			"dropoff_address": "Schiphol", "pickup_at": time.Now().Add(48 * time.Hour).Unix(),
			"passengers": 2,
		}}, "idempotency_key": "dn-http-a",
	}, http.StatusCreated, &req)
	var q model.Quote
	env.do("POST", "/v1/requests/"+req.ID+"/quote", env.tenantID, nil, http.StatusOK, &q)
	env.do("POST", "/v1/quotes/"+q.ID+"/confirm", env.tenantID, nil, http.StatusCreated, nil)

	// Issue the period folio so there is an unpaid statement.
	var folios []model.Folio
	env.do("GET", "/v1/partners/"+p.ID+"/folios", env.tenantID, nil, http.StatusOK, &folios)
	if len(folios) != 1 {
		t.Fatalf("folios: %+v", folios)
	}
	var f model.Folio
	env.do("POST", "/v1/partners/"+p.ID+"/folios/"+folios[0].Period+"/EUR/issue", env.tenantID,
		nil, http.StatusOK, &f)
	if f.State != "ISSUED" {
		t.Fatalf("issued: %+v", f)
	}

	// Aging: one row, inside terms (issued seconds ago), EUR 90.00 current.
	var report model.FolioAgingReport
	env.do("GET", "/v1/folios/aging", env.tenantID, nil, http.StatusOK, &report)
	if report.TermsDays != 14 || len(report.Rows) != 1 {
		t.Fatalf("aging: %+v", report)
	}
	row := report.Rows[0]
	if row.PartnerID != p.ID || row.PartnerName != "Dunning Grand" || row.Currency != "EUR" ||
		row.OpenFolios != 1 || row.CurrentMinor != 9000 || row.TotalMinor != 9000 ||
		row.Overdue1To7Minor != 0 || row.OldestOverdueDays > 0 {
		t.Fatalf("aging row: %+v", row)
	}

	// On-demand sweep: nothing due yet inside terms.
	var res struct {
		Sent    int `json:"sent"`
		Skipped int `json:"skipped"`
	}
	env.do("POST", "/v1/dunning/run", env.tenantID, nil, http.StatusOK, &res)
	if res.Sent != 0 || res.Skipped != 0 {
		t.Fatalf("dunning run inside terms: %+v", res)
	}

	// Manual nudge: the operator may remind early — the touch is recorded
	// verbatim (no adapter in this env → skipped:no-adapter, never "sent").
	env.do("POST", "/v1/folios/"+f.ID+"/remind", env.tenantID, nil, http.StatusOK, &f)
	if len(f.Reminders) != 1 || f.Reminders[0].N != 1 || f.Reminders[0].Status != "skipped:no-adapter" {
		t.Fatalf("manual reminder: %+v", f.Reminders)
	}
	// …and the 24h guard stops a second touch.
	env.do("POST", "/v1/folios/"+f.ID+"/remind", env.tenantID, nil, http.StatusConflict, nil)
	env.do("POST", "/v1/folios/00000000-0000-0000-0000-000000000000/remind", env.tenantID,
		nil, http.StatusNotFound, nil)

	// A settled statement can no longer be dunned and leaves the aging view.
	env.do("POST", "/v1/folios/"+f.ID+"/settle", env.tenantID, nil, http.StatusOK, &f)
	env.do("POST", "/v1/folios/"+f.ID+"/remind", env.tenantID, nil, http.StatusConflict, nil)
	env.do("GET", "/v1/folios/aging", env.tenantID, nil, http.StatusOK, &report)
	if len(report.Rows) != 0 {
		t.Fatalf("settled folio must leave aging: %+v", report)
	}
}
