// End-to-end Stripe webhook test: signed events settle intents, forged or
// unsigned events are rejected, and the tenant is routed from signed metadata.
package server_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

const stripeTestSecret = "whsec_server_test"

func stripeHeader(secret string, payload []byte) string {
	ts := time.Now().Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d", ts)
	mac.Write([]byte("."))
	mac.Write(payload)
	return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

func TestStripeWebhookEndToEnd(t *testing.T) {
	svc := domain.NewServices(store.NewMemory(), domain.Deps{StripeWHSec: stripeTestSecret})
	srv := httptest.NewServer(server.New(svc))
	defer srv.Close()
	env := &testEnv{t: t, server: srv}

	var tenant model.Tenant
	env.do("POST", "/v1/platform/tenants", "", model.Tenant{
		Slug: "taylor-stripe", Name: "Taylor", Archetype: "flagship",
	}, http.StatusCreated, &tenant)
	env.tenantID = tenant.ID

	env.do("POST", "/v1/catalog", env.tenantID, model.CatalogService{
		Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Sedan|business",
		BaseMinor: 9000, Currency: "EUR", PerKMMinor: 250, AutoQuotable: true,
	}, http.StatusCreated, nil)

	var res domain.ResolveResult
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"phone": "+31655556666", "name": "EA Booker"}, http.StatusOK, &res)

	var sr model.ServiceRequest
	env.do("POST", "/v1/requests", env.tenantID, map[string]any{
		"client_id": res.Client.ID, "idempotency_key": "sr-wh",
		"items": []model.ServiceItem{{
			Vertical:  "CHAUFFEUR_POINT_TO_POINT",
			PickupLat: 52.37, PickupLng: 4.90, DropoffLat: 52.31, DropoffLng: 4.76,
			PickupAt: time.Now().Add(24 * time.Hour).Unix(), Passengers: 1,
		}},
	}, http.StatusCreated, &sr)

	var q model.Quote
	env.do("POST", "/v1/requests/"+sr.ID+"/quote", env.tenantID, nil, http.StatusOK, &q)

	var intent model.PaymentIntent
	env.do("POST", "/v1/payments/links", env.tenantID, map[string]string{
		"quote_id": q.ID, "mode": "FULL", "idempotency_key": "pay-wh-1",
	}, http.StatusCreated, &intent)

	postWebhook := func(payload []byte, sigHeader string, want int) {
		t.Helper()
		req, _ := http.NewRequest("POST", srv.URL+"/v1/payments/stripe/webhook", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if sigHeader != "" {
			req.Header.Set("Stripe-Signature", sigHeader)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("webhook: got %d want %d", resp.StatusCode, want)
		}
	}

	event := []byte(fmt.Sprintf(`{
		"type": "checkout.session.completed",
		"data": {"object": {"id": "cs_wh_1", "metadata": {
			"tenant_id": %q, "intent_id": %q, "quote_id": %q}}}}`,
		env.tenantID, intent.ID, q.ID))

	// Unsigned and wrongly-signed events never reach settlement.
	postWebhook(event, "", http.StatusForbidden)
	postWebhook(event, stripeHeader("whsec_wrong", event), http.StatusForbidden)

	// A correctly signed event settles: intent CAPTURED + split postings.
	postWebhook(event, stripeHeader(stripeTestSecret, event), http.StatusOK)

	var got model.PaymentIntent
	env.do("GET", "/v1/payments/intents/"+intent.ID, env.tenantID, nil, http.StatusOK, &got)
	if got.State != "CAPTURED" || got.PSPRef != "cs_wh_1" {
		t.Fatalf("intent not settled: %+v", got)
	}
	var postings []model.LedgerPosting
	env.do("GET", "/v1/payments/postings", env.tenantID, nil, http.StatusOK, &postings)
	var netOK, feeOK bool
	for _, p := range postings {
		if p.DebitCode == "CLIENT_CLEARING" && p.CreditCode == "TENANT_REVENUE" {
			netOK = true
		}
		if p.DebitCode == "CLIENT_CLEARING" && p.CreditCode == "PLATFORM_FEES" {
			feeOK = true
		}
	}
	if !netOK || !feeOK {
		t.Fatalf("split settlement postings missing: %+v", postings)
	}

	// Tampered body with an otherwise valid header is a replay/forgery.
	forged := bytes.Replace(event, []byte(intent.ID), []byte("pi-forged"), 1)
	postWebhook(forged, stripeHeader(stripeTestSecret, event), http.StatusForbidden)
}
