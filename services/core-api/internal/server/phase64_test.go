package server_test

// Phase 64 over HTTP: operator reattribution of off-channel demand to the
// partner who really sourced it — the request and its booking move to the
// channel, and the sourcing commission accrues once.

import (
	"net/http"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestChannelReattribution64(t *testing.T) {
	env := newEnv(t)

	// A hotel partner with a sourcing commission.
	var p model.Partner
	env.do("POST", "/v1/partners", env.tenantID, map[string]any{
		"name": "Email Desk Hotel", "verticals": []string{"CHAUFFEUR_POINT_TO_POINT"},
		"payout_rail": "MANUAL", "status": "VETTED", "kind": "hotel",
	}, http.StatusCreated, &p)
	env.do("PUT", "/v1/partners/"+p.ID+"/commission", env.tenantID,
		map[string]any{"commission_pct": 15}, http.StatusOK, nil)

	// Demand arrives "via email": direct request → quote → confirm.
	var res struct {
		Client model.Client `json:"client"`
	}
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"email": "guest@hotel.example", "name": "Email Guest"}, http.StatusOK, &res)
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "name": "Sedan|http64",
		"base_minor": 9000, "currency": "EUR", "per_km_minor": 0,
		"auto_quotable": true, "active": true,
	}, http.StatusCreated, nil)
	item := map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "pickup_address": "Hotel",
		"dropoff_address": "Airport", "pickup_at": time.Now().Add(48 * time.Hour).Unix(),
		"passengers": 1,
	}
	var req model.ServiceRequest
	env.do("POST", "/v1/requests", env.tenantID, map[string]any{
		"client_id": res.Client.ID, "idempotency_key": "email64-http",
		"items": []map[string]any{item},
	}, http.StatusCreated, &req)
	if req.SourceKind != "direct" {
		t.Fatalf("precondition: %+v", req)
	}
	var quote model.Quote
	env.do("POST", "/v1/requests/"+req.ID+"/quote", env.tenantID, nil, http.StatusOK, &quote)
	var booking model.Booking
	env.do("POST", "/v1/quotes/"+quote.ID+"/confirm", env.tenantID, nil, http.StatusCreated, &booking)

	// The operator learns the hotel sent this guest — reattribute.
	var updated model.ServiceRequest
	env.do("POST", "/v1/requests/"+req.ID+"/source", env.tenantID,
		map[string]any{"partner_id": p.ID}, http.StatusOK, &updated)
	if updated.SourceKind != "hotel" || updated.SourcePartnerID != p.ID {
		t.Fatalf("reattributed: %+v", updated)
	}

	// The margin report now shows the booking under the hotel channel
	// with its commission — the direct row is empty again.
	var report model.SourceReport
	env.do("GET", "/v1/analytics/sources?days=30", env.tenantID, nil, http.StatusOK, &report)
	var hotelRow *model.SourceReportRow
	for i := range report.Rows {
		if report.Rows[i].SourceKind == "hotel" {
			hotelRow = &report.Rows[i]
		}
		if report.Rows[i].SourceKind == "direct" {
			t.Fatalf("direct row should be empty after reattribution: %+v", report.Rows[i])
		}
	}
	if hotelRow == nil || hotelRow.Bookings != 1 || hotelRow.CommissionMinor != 1350 {
		t.Fatalf("hotel row after reattribution: %+v", report.Rows)
	}

	// Idempotent replay.
	env.do("POST", "/v1/requests/"+req.ID+"/source", env.tenantID,
		map[string]any{"partner_id": p.ID}, http.StatusOK, &updated)

	// Unknown partner 404s; unknown request 404s.
	env.do("POST", "/v1/requests/"+req.ID+"/source", env.tenantID,
		map[string]any{"partner_id": "00000000-0000-0000-0000-000000000000"}, http.StatusNotFound, nil)
	env.do("POST", "/v1/requests/00000000-0000-0000-0000-000000000000/source", env.tenantID,
		map[string]any{"partner_id": p.ID}, http.StatusNotFound, nil)

	// Clear back to direct. Fresh decode var: the clear response omits
	// source_partner_id (omitempty), so decoding into the reused struct
	// would retain the stale hotel ID from the previous response.
	var cleared model.ServiceRequest
	env.do("POST", "/v1/requests/"+req.ID+"/source", env.tenantID,
		map[string]any{"partner_id": ""}, http.StatusOK, &cleared)
	if cleared.SourceKind != "direct" || cleared.SourcePartnerID != "" {
		t.Fatalf("cleared: %+v", cleared)
	}
}
