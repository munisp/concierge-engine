package server_test

// Phase 63 over HTTP: partner book-on-behalf intake through the partner
// key surface, the admin kind classifier, and the per-channel margin
// report — including cross-surface auth rejection and direct-channel
// margin visibility.

import (
	"net/http"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestDemandChannels63(t *testing.T) {
	env := newEnv(t)

	// A hotel partner with a sourcing commission.
	var p model.Partner
	env.do("POST", "/v1/partners", env.tenantID, map[string]any{
		"name": "Grand Hotel Desk", "verticals": []string{"CHAUFFEUR_POINT_TO_POINT"},
		"payout_rail": "MANUAL", "status": "VETTED",
	}, http.StatusCreated, &p)
	if p.Kind != "tour_operator" { // default until classified
		t.Fatalf("default kind: %q", p.Kind)
	}
	env.do("PUT", "/v1/partners/"+p.ID+"/commission", env.tenantID,
		map[string]any{"commission_pct": 10}, http.StatusOK, nil)
	var kp model.Partner
	env.do("PUT", "/v1/partners/"+p.ID+"/kind", env.tenantID,
		map[string]any{"kind": "hotel"}, http.StatusOK, &kp)
	if kp.Kind != "hotel" {
		t.Fatalf("kind set: %+v", kp)
	}
	env.do("PUT", "/v1/partners/"+p.ID+"/kind", env.tenantID,
		map[string]any{"kind": "airline"}, http.StatusUnprocessableEntity, nil)

	// Partner key.
	var keyRes struct {
		Secret string `json:"secret"`
	}
	env.do("POST", "/v1/partners/"+p.ID+"/keys", env.tenantID,
		map[string]string{"name": "desk-sync"}, http.StatusCreated, &keyRes)

	// Book-on-behalf through the partner surface.
	item := map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "pickup_address": "Grand Hotel",
		"dropoff_address": "Schiphol", "pickup_at": time.Now().Add(48 * time.Hour).Unix(),
		"passengers": 2,
	}
	var req model.ServiceRequest
	env.doBearer("POST", "/v1/partner/requests", keyRes.Secret, map[string]any{
		"guest_name": "Dee Guest", "guest_phone": "+31640000001",
		"items": []map[string]any{item}, "idempotency_key": "desk-http-1",
	}, http.StatusCreated, &req)
	if req.SourceKind != "hotel" || req.SourcePartnerID != p.ID {
		t.Fatalf("sourced request: %+v", req)
	}

	// Replay is idempotent.
	var req2 model.ServiceRequest
	env.doBearer("POST", "/v1/partner/requests", keyRes.Secret, map[string]any{
		"guest_name": "Dee Guest", "guest_phone": "+31640000001",
		"items": []map[string]any{item}, "idempotency_key": "desk-http-1",
	}, http.StatusCreated, &req2)
	if req2.ID != req.ID {
		t.Fatalf("replay: %s vs %s", req2.ID, req.ID)
	}

	// The partner's feed shows their demand; a tenant-wide key is refused.
	var feed []map[string]any
	env.doBearer("GET", "/v1/partner/requests", keyRes.Secret, nil, http.StatusOK, &feed)
	if len(feed) != 1 || feed[0]["id"] != req.ID || feed[0]["guest_name"] != "Dee Guest" {
		t.Fatalf("feed: %+v", feed)
	}
	env.do("GET", "/v1/partner/requests", env.tenantID, nil, http.StatusForbidden, nil)

	// Validation surfaces through HTTP.
	env.doBearer("POST", "/v1/partner/requests", keyRes.Secret, map[string]any{
		"guest_name": "No Contact", "items": []map[string]any{item},
		"idempotency_key": "bad-1",
	}, http.StatusUnprocessableEntity, nil)

	// Drive the sourced request through the standard pipeline to a booking
	// and confirm the margin report attributes it to the hotel channel.
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "name": "Sedan|http63",
		"base_minor": 9000, "currency": "EUR", "per_km_minor": 0,
		"auto_quotable": true, "active": true,
	}, http.StatusCreated, nil)
	var quote model.Quote
	env.do("POST", "/v1/requests/"+req.ID+"/quote", env.tenantID, nil, http.StatusOK, &quote)
	var booking model.Booking
	env.do("POST", "/v1/quotes/"+quote.ID+"/confirm", env.tenantID, nil, http.StatusCreated, &booking)
	if booking.SourceKind != "hotel" || booking.SourcePartnerID != p.ID {
		t.Fatalf("booking source: %+v", booking)
	}

	// A direct booking for contrast: resolve a client, request with no
	// source fields — the platform labels it "direct".
	var res struct {
		Client model.Client `json:"client"`
	}
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"phone": "+31650000001", "name": "Direct Rider"}, http.StatusOK, &res)
	var dreq model.ServiceRequest
	env.do("POST", "/v1/requests", env.tenantID, map[string]any{
		"client_id": res.Client.ID, "idempotency_key": "direct-http-1",
		"items": []map[string]any{item},
	}, http.StatusCreated, &dreq)
	if dreq.SourceKind != "direct" {
		t.Fatalf("direct request source: %+v", dreq)
	}
	var dquote model.Quote
	env.do("POST", "/v1/requests/"+dreq.ID+"/quote", env.tenantID, nil, http.StatusOK, &dquote)
	var dbooking model.Booking
	env.do("POST", "/v1/quotes/"+dquote.ID+"/confirm", env.tenantID, nil, http.StatusCreated, &dbooking)
	if dbooking.SourceKind != "direct" || dbooking.SourcePartnerID != "" {
		t.Fatalf("direct booking source: %+v", dbooking)
	}

	// Margin report: hotel row carries commission, direct row is pure.
	var report model.SourceReport
	env.do("GET", "/v1/analytics/sources?days=30", env.tenantID, nil, http.StatusOK, &report)
	if len(report.Rows) != 2 {
		t.Fatalf("report rows: %+v", report.Rows)
	}
	var direct, hotel *model.SourceReportRow
	for i := range report.Rows {
		switch report.Rows[i].SourceKind {
		case "direct":
			direct = &report.Rows[i]
		case "hotel":
			hotel = &report.Rows[i]
		}
	}
	if direct == nil || hotel == nil {
		t.Fatalf("missing channel rows: %+v", report.Rows)
	}
	if direct.CommissionMinor != 0 || direct.NetMinor != direct.GrossMinor {
		t.Fatalf("direct row: %+v", direct)
	}
	if hotel.CommissionMinor <= 0 || hotel.NetMinor != hotel.GrossMinor-hotel.CommissionMinor {
		t.Fatalf("hotel row: %+v", hotel)
	}
}
