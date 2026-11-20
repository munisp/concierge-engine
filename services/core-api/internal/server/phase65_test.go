package server_test

// Phase 65 over HTTP: rate-card CRUD and a sourced quote repriced with an
// explicit partner-rate line; direct demand and invalid cards surface
// through the API exactly as the domain rules state.

import (
	"net/http"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestRateCards65(t *testing.T) {
	env := newEnv(t)

	// A hotel partner with a sourcing commission.
	var p model.Partner
	env.do("POST", "/v1/partners", env.tenantID, map[string]any{
		"name": "Rate Card Grand", "verticals": []string{"CHAUFFEUR_POINT_TO_POINT"},
		"payout_rail": "MANUAL", "status": "VETTED", "kind": "hotel",
	}, http.StatusCreated, &p)
	env.do("PUT", "/v1/partners/"+p.ID+"/commission", env.tenantID,
		map[string]any{"commission_pct": 10}, http.StatusOK, nil)

	// CRUD: empty list, set, idempotent upsert, validation, delete.
	var cards []model.RateCard
	env.do("GET", "/v1/partners/"+p.ID+"/rate-cards", env.tenantID, nil, http.StatusOK, &cards)
	if len(cards) != 0 {
		t.Fatalf("fresh cards: %+v", cards)
	}
	env.do("PUT", "/v1/partners/"+p.ID+"/rate-cards", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "kind": "pct_discount", "value": 10,
	}, http.StatusOK, nil)
	env.do("PUT", "/v1/partners/"+p.ID+"/rate-cards", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "kind": "pct_discount", "value": 10,
	}, http.StatusOK, nil)
	env.do("GET", "/v1/partners/"+p.ID+"/rate-cards", env.tenantID, nil, http.StatusOK, &cards)
	if len(cards) != 1 || cards[0].Kind != "pct_discount" || cards[0].Value != 10 {
		t.Fatalf("cards after upsert: %+v", cards)
	}
	env.do("PUT", "/v1/partners/"+p.ID+"/rate-cards", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "kind": "pct_discount", "value": 75,
	}, http.StatusUnprocessableEntity, nil)
	env.do("PUT", "/v1/partners/00000000-0000-0000-0000-000000000000/rate-cards", env.tenantID,
		map[string]any{"vertical": "CHAUFFEUR_POINT_TO_POINT", "kind": "pct_discount", "value": 10},
		http.StatusNotFound, nil)

	// Sourced demand reprices; the adjustment line names the partner.
	env.do("POST", "/v1/catalog", env.tenantID, map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "name": "Sedan|http65",
		"base_minor": 9000, "currency": "EUR", "per_km_minor": 0,
		"auto_quotable": true, "active": true,
	}, http.StatusCreated, nil)
	var keyRes struct {
		Secret string `json:"secret"`
	}
	env.do("POST", "/v1/partners/"+p.ID+"/keys", env.tenantID,
		map[string]string{"name": "desk-65"}, http.StatusCreated, &keyRes)
	item := map[string]any{
		"vertical": "CHAUFFEUR_POINT_TO_POINT", "pickup_address": "Grand",
		"dropoff_address": "Schiphol", "pickup_at": time.Now().Add(48 * time.Hour).Unix(),
		"passengers": 2,
	}
	var req model.ServiceRequest
	env.doBearer("POST", "/v1/partner/requests", keyRes.Secret, map[string]any{
		"guest_name": "Card Guest", "guest_phone": "+31680000001",
		"items": []map[string]any{item}, "idempotency_key": "rc-http-1",
	}, http.StatusCreated, &req)
	var quote model.Quote
	env.do("POST", "/v1/requests/"+req.ID+"/quote", env.tenantID, nil, http.StatusOK, &quote)
	if quote.TotalMinor != 8100 {
		t.Fatalf("repriced total: %d lines %+v", quote.TotalMinor, quote.Lines)
	}
	found := false
	for _, l := range quote.Lines {
		if l.AmountMinor == -900 && l.Description == "Partner rate — Rate Card Grand (−10%)" {
			found = true
		}
	}
	if !found {
		t.Fatalf("partner-rate line missing: %+v", quote.Lines)
	}

	// Delete removes the pricing effect on the next quote.
	env.do("DELETE", "/v1/partners/"+p.ID+"/rate-cards/CHAUFFEUR_POINT_TO_POINT", env.tenantID,
		nil, http.StatusNoContent, nil)
	env.do("DELETE", "/v1/partners/"+p.ID+"/rate-cards/CHAUFFEUR_POINT_TO_POINT", env.tenantID,
		nil, http.StatusNotFound, nil)
	var req2 model.ServiceRequest
	env.doBearer("POST", "/v1/partner/requests", keyRes.Secret, map[string]any{
		"guest_name": "Card Guest", "guest_phone": "+31680000001",
		"items": []map[string]any{item}, "idempotency_key": "rc-http-2",
	}, http.StatusCreated, &req2)
	var quote2 model.Quote
	env.do("POST", "/v1/requests/"+req2.ID+"/quote", env.tenantID, nil, http.StatusOK, &quote2)
	if quote2.TotalMinor != 9000 {
		t.Fatalf("post-delete total: %d", quote2.TotalMinor)
	}
}
