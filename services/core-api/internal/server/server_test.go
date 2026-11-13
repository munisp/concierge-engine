package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/model"
	"concierge-engine/services/core-api/internal/server"
	"concierge-engine/services/core-api/internal/store"
)

type testEnv struct {
	t        *testing.T
	server   *httptest.Server
	tenantID string
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	svc := domain.NewServices(store.NewMemory(), domain.Deps{})
	srv := httptest.NewServer(server.New(svc))
	t.Cleanup(srv.Close)
	env := &testEnv{t: t, server: srv}

	// platform admin: onboard a tenant (Taylor archetype: flagship)
	var tenant model.Tenant
	env.do("POST", "/v1/platform/tenants", "", model.Tenant{
		Slug: "taylor", Name: "Taylor Travel Management", Archetype: "flagship",
		PricingRules: model.PricingRules{Currency: "EUR", DepositPct: 0.3, QuoteTTLSeconds: 3600, PlatformFeePct: 0.03},
	}, http.StatusCreated, &tenant)
	env.tenantID = tenant.ID
	return env
}

func (e *testEnv) do(method, path, tenantID string, body any, want int, out any) {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, e.server.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if tenantID != "" {
		req.Header.Set("X-Tenant-ID", tenantID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		var env map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&env)
		e.t.Fatalf("%s %s: got %d want %d: %v", method, path, resp.StatusCode, want, env)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			e.t.Fatal(err)
		}
	}
}

func TestTenantIsolationEnforced(t *testing.T) {
	env := newEnv(t)
	// tenant routes without X-Tenant-ID are rejected
	env.do("GET", "/v1/catalog", "", nil, http.StatusForbidden, nil)
}

func TestIdentityResolutionAndPreferences(t *testing.T) {
	env := newEnv(t)

	var res domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"phone": "+31 6 1234 5678", "name": "M. Raap"}, http.StatusOK, &res)
	if !res.IsNew {
		t.Fatal("expected new client")
	}

	// same phone, reformatted -> same client (phone is the identity anchor)
	var res2 domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"phone": "+31612345678"}, http.StatusOK, &res2)
	if res2.IsNew || res2.Client.ID != res.Client.ID {
		t.Fatalf("identity resolution failed: %+v", res2)
	}

	// consent-scoped preference memory
	env.do("PUT", "/v1/clients/"+res.Client.ID+"/preferences/vehicle_class", env.tenantID,
		map[string]string{"value": "first", "consent_scope": "service", "recorded_by": "ai"}, http.StatusOK, nil)
	var prefs []model.Preference
	env.do("GET", "/v1/clients/"+res.Client.ID+"/preferences", env.tenantID, nil, http.StatusOK, &prefs)
	if len(prefs) != 1 || prefs[0].Value != "first" {
		t.Fatalf("preferences: %+v", prefs)
	}
	// DSAR cascade
	env.do("DELETE", "/v1/clients/"+res.Client.ID+"/preferences", env.tenantID, nil, http.StatusNoContent, nil)
	env.do("GET", "/v1/clients/"+res.Client.ID+"/preferences", env.tenantID, nil, http.StatusOK, &prefs)
	if len(prefs) != 0 {
		t.Fatal("DSAR cascade failed")
	}
}

func TestInstantQuoteAndBookingFlow(t *testing.T) {
	env := newEnv(t)

	// catalog: business sedan, per-km pricing
	env.do("POST", "/v1/catalog", env.tenantID, model.CatalogService{
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Business Sedan|business",
		BaseMinor: 9500, Currency: "EUR", PerKMMinor: 350, AutoQuotable: true,
	}, http.StatusCreated, nil)

	var res domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"email": "ea@family-office.example", "name": "EA Booker"}, http.StatusOK, &res)

	// create request (idempotent) — AMS airport -> Amsterdam centre (~15km)
	var sr model.ServiceRequest
	env.do("POST", "/v1/requests", env.tenantID, map[string]any{
		"client_id": res.Client.ID, "idempotency_key": "req-1",
		"items": []model.ServiceItem{{
			Vertical: "CHAUFFEUR_POINT_TO_POINT", VehicleClass: "business",
			PickupAddress: "AMS Schiphol", PickupLat: 52.3105, PickupLng: 4.7683,
			DropoffAddress: "Herengracht 1, Amsterdam", DropoffLat: 52.3676, DropoffLng: 4.9041,
			PickupAt: time.Now().Add(24 * time.Hour).Unix(), Passengers: 2,
		}},
	}, http.StatusCreated, &sr)

	// idempotent replay returns the same request
	var sr2 model.ServiceRequest
	env.do("POST", "/v1/requests", env.tenantID, map[string]any{
		"client_id": res.Client.ID, "idempotency_key": "req-1",
		"items": []model.ServiceItem{{
			Vertical: "CHAUFFEUR_POINT_TO_POINT", VehicleClass: "business",
			PickupAddress: "AMS Schiphol", PickupLat: 52.3105, PickupLng: 4.7683,
			DropoffAddress: "Herengracht 1, Amsterdam", DropoffLat: 52.3676, DropoffLng: 4.9041,
			PickupAt: time.Now().Add(24 * time.Hour).Unix(), Passengers: 2,
		}},
	}, http.StatusCreated, &sr2)
	if sr2.ID != sr.ID {
		t.Fatal("idempotency broken")
	}

	// instant quote
	var q model.Quote
	env.do("POST", "/v1/requests/"+sr.ID+"/quote", env.tenantID, nil, http.StatusOK, &q)
	if q.Origin != "INSTANT" || q.TotalMinor <= 9500 || q.Currency != "EUR" {
		t.Fatalf("quote: %+v", q)
	}
	if q.DepositMinor != int64(float64(q.TotalMinor)*0.3+0.5) && q.DepositMinor == 0 {
		t.Fatalf("deposit missing: %+v", q)
	}

	// payment link then PSP capture -> booking + split settlement
	var intent model.PaymentIntent
	env.do("POST", "/v1/payments/links", env.tenantID, map[string]any{
		"quote_id": q.ID, "mode": "FULL", "idempotency_key": "pay-1",
	}, http.StatusCreated, &intent)
	if intent.AmountMinor != q.TotalMinor {
		t.Fatalf("intent amount: %+v", intent)
	}

	var booking model.Booking
	env.do("POST", "/v1/quotes/"+q.ID+"/confirm", env.tenantID, nil, http.StatusCreated, &booking)

	env.do("POST", "/v1/payments/webhooks/stripe", env.tenantID, map[string]string{
		"intent_id": intent.ID, "event": "payment.captured", "psp_ref": "ch_123",
	}, http.StatusOK, nil)

	var postings []model.LedgerPosting
	env.do("GET", "/v1/payments/postings", env.tenantID, nil, http.StatusOK, &postings)
	if len(postings) != 2 { // net revenue + platform fee
		t.Fatalf("expected 2 split postings, got %d", len(postings))
	}
	var sum int64
	for _, p := range postings {
		sum += p.AmountMinor
	}
	if sum != intent.AmountMinor {
		t.Fatalf("split mismatch: %d != %d", sum, intent.AmountMinor)
	}
}

func TestStateMachineRejectsIllegalTransition(t *testing.T) {
	env := newEnv(t)
	var res domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"name": "X"}, http.StatusOK, &res)
	var sr model.ServiceRequest
	env.do("POST", "/v1/requests", env.tenantID, map[string]any{
		"client_id": res.Client.ID, "idempotency_key": "req-illegal",
		"items": []model.ServiceItem{{
			Vertical: "CHAUFFEUR_POINT_TO_POINT", PickupAddress: "A", PickupAt: time.Now().Unix(),
		}},
	}, http.StatusCreated, &sr)
	// NEW->CAPTURING happens at create; CAPTURING->CONFIRMED is illegal
	env.do("POST", "/v1/requests/"+sr.ID+"/transition", env.tenantID,
		map[string]string{"to": "CONFIRMED"}, http.StatusConflict, nil)
}

func TestHandoffQueue(t *testing.T) {
	env := newEnv(t)
	var conv model.Conversation
	env.do("POST", "/v1/conversations", env.tenantID, map[string]string{
		"channel": "WHATSAPP",
	}, http.StatusCreated, &conv)

	env.do("POST", "/v1/conversations/"+conv.ID+"/messages", env.tenantID, model.Message{
		Author: "CLIENT", Body: "I need the jet to Geneva tomorrow, money no object.",
	}, http.StatusCreated, nil)

	var handoff map[string]any
	env.do("POST", "/v1/handoffs", env.tenantID, map[string]any{
		"conversation_id": conv.ID, "trigger": "VALUE",
		"reason": "estimated value above tenant threshold",
	}, http.StatusCreated, &handoff)
	if handoff["id"] == nil {
		t.Fatal("no handoff id")
	}

	var open []map[string]any
	env.do("GET", "/v1/handoffs", env.tenantID, nil, http.StatusOK, &open)
	if len(open) != 1 {
		t.Fatalf("expected 1 open handoff, got %d", len(open))
	}

	env.do("POST", fmt.Sprintf("/v1/handoffs/%v/resolve", handoff["id"]), env.tenantID,
		map[string]string{"resolution": "RESOLVED", "owner": "staff:mathijs"}, http.StatusOK, nil)
	env.do("GET", "/v1/handoffs", env.tenantID, nil, http.StatusOK, &open)
	if len(open) != 0 {
		t.Fatal("handoff not resolved")
	}
}

func TestHumanQuoteFallbackAboveTier2Limit(t *testing.T) {
	env := newEnv(t)
	// default TIER-2 limit is 50000 minor; price a request above it
	env.do("POST", "/v1/catalog", env.tenantID, model.CatalogService{
		Vertical: "CHAUFFEUR_HOURLY", Name: "First Class|first",
		BaseMinor: 60000, Currency: "EUR", PerHourMinor: 15000, AutoQuotable: true,
	}, http.StatusCreated, nil)
	var res domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID, map[string]string{"name": "VIP"}, http.StatusOK, &res)
	var sr model.ServiceRequest
	env.do("POST", "/v1/requests", env.tenantID, map[string]any{
		"client_id": res.Client.ID, "idempotency_key": "req-big",
		"items": []model.ServiceItem{{
			Vertical: "CHAUFFEUR_HOURLY", VehicleClass: "first", Hours: 4,
			PickupAddress: "Amsterdam", PickupAt: time.Now().Unix(),
		}},
	}, http.StatusCreated, &sr)
	// 60000 + 4*? base alone exceeds 50000 -> human fallback (202)
	var out map[string]any
	env.do("POST", "/v1/requests/"+sr.ID+"/quote", env.tenantID, nil, http.StatusAccepted, &out)
	if out["origin"] != "HUMAN" {
		t.Fatalf("expected human fallback: %v", out)
	}
}
