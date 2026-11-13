// Stripe adapter tests: webhook signature verification (the money-truth
// ingress must reject forgeries), event parsing, Checkout Session creation
// against a fake Stripe, and the payment-link integration.
package domain

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func signStripe(secret string, payload []byte, ts time.Time) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d", ts.Unix())
	mac.Write([]byte("."))
	mac.Write(payload)
	return fmt.Sprintf("t=%d,v1=%s", ts.Unix(), hex.EncodeToString(mac.Sum(nil)))
}

func TestStripeSignatureVerification(t *testing.T) {
	secret := "whsec_test_secret"
	payload := []byte(`{"type":"checkout.session.completed"}`)
	now := time.Now()

	if !VerifyStripeSignature(signStripe(secret, payload, now), payload, secret, now) {
		t.Fatal("valid signature rejected")
	}
	if VerifyStripeSignature(signStripe(secret, payload, now), []byte(`{"type":"charge.refunded"}`), secret, now) {
		t.Fatal("tampered payload accepted")
	}
	if VerifyStripeSignature(signStripe("whsec_other", payload, now), payload, secret, now) {
		t.Fatal("wrong secret accepted")
	}
	if VerifyStripeSignature(signStripe(secret, payload, now.Add(-10*time.Minute)), payload, secret, now) {
		t.Fatal("stale timestamp accepted (replay)")
	}
	if VerifyStripeSignature(signStripe(secret, payload, now.Add(10*time.Minute)), payload, secret, now) {
		t.Fatal("future timestamp accepted")
	}
	if VerifyStripeSignature("", payload, secret, now) {
		t.Fatal("missing header accepted")
	}
	if VerifyStripeSignature(signStripe(secret, payload, now), payload, "", now) {
		t.Fatal("unsigned endpoint accepted a signature")
	}
}

func TestParseStripeEvent(t *testing.T) {
	payload := []byte(`{
		"type": "checkout.session.completed",
		"data": {"object": {"id": "cs_test_123", "metadata": {
			"tenant_id": "t-1", "intent_id": "pi-1", "quote_id": "q-1"}}}}`)
	ev, err := ParseStripeEvent(payload)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != "checkout.session.completed" || ev.SessionID != "cs_test_123" ||
		ev.TenantID != "t-1" || ev.IntentID != "pi-1" {
		t.Fatalf("event misparsed: %+v", ev)
	}
	if _, err := ParseStripeEvent([]byte(`{"data":{}}`)); err == nil {
		t.Fatal("typeless event accepted")
	}
}

func TestStripeCheckoutSessionAgainstFakeAPI(t *testing.T) {
	var gotForm url.Values
	var gotIdem, gotAuth string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/checkout/sessions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		gotIdem = r.Header.Get("Idempotency-Key")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"id": "cs_test_abc", "url": "https://checkout.stripe.com/c/pay/cs_test_abc"})
	}))
	defer fake.Close()

	c := NewStripeClient("sk_test_123", fake.URL, "", time.Now)
	intent := &model.PaymentIntent{
		ID: "pi-1", TenantID: "t-1", QuoteID: "q-1", Mode: "DEPOSIT_BALANCE",
		AmountMinor: 4500, Currency: "EUR", IdempotencyKey: "idem-1",
	}
	hostedURL, sessionID, err := c.CreateCheckoutSession(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if hostedURL != "https://checkout.stripe.com/c/pay/cs_test_abc" || sessionID != "cs_test_abc" {
		t.Fatalf("session not applied: %s %s", hostedURL, sessionID)
	}
	if gotIdem != "idem-1" {
		t.Fatalf("idempotency key not forwarded: %q", gotIdem)
	}
	if gotAuth != "Bearer sk_test_123" {
		t.Fatalf("auth header wrong: %q", gotAuth)
	}
	if gotForm.Get("line_items[0][price_data][unit_amount]") != "4500" ||
		gotForm.Get("line_items[0][price_data][currency]") != "eur" ||
		gotForm.Get("metadata[tenant_id]") != "t-1" ||
		gotForm.Get("metadata[intent_id]") != "pi-1" {
		t.Fatalf("form fields wrong: %v", gotForm)
	}

	// Disabled client refuses live calls.
	off := NewStripeClient("", "", "", time.Now)
	if _, _, err := off.CreateCheckoutSession(context.Background(), intent); err == nil {
		t.Fatal("disabled client attempted a live call")
	}
}

func TestPaymentLinkUsesStripeWhenEnabled(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"id": "cs_live_1", "url": "https://checkout.stripe.com/c/pay/cs_live_1"})
	}))
	defer fake.Close()

	s := testServices(t, Deps{StripeKey: "sk_test", StripeBase: fake.URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	if _, err := s.Catalog.Add(ctx, &model.CatalogService{
		TenantID: tenantID, Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Sedan|business",
		BaseMinor: 9000, Currency: "EUR", PerKMMinor: 250, AutoQuotable: true,
	}); err != nil {
		t.Fatal(err)
	}
	res, _ := s.Clients.Resolve(ctx, tenantID, "+31611112222", "", "Booker")
	sr, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
		TenantID: tenantID, ClientID: res.Client.ID, IdempotencyKey: "sr-stripe",
		Items: []model.ServiceItem{{
			Vertical:  "CHAUFFEUR_POINT_TO_POINT",
			PickupLat: 52.37, PickupLng: 4.90, DropoffLat: 52.31, DropoffLng: 4.76,
			PickupAt: 1733500000, Passengers: 1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	q, instant, err := s.Booking.QuoteRequest(ctx, tenantID, sr.ID)
	if err != nil || !instant {
		t.Fatalf("quote: %v instant=%v", err, instant)
	}
	intent, err := s.Payments.CreatePaymentLink(ctx, tenantID, q.ID, "FULL", "api", "pay-stripe-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(intent.HostedURL, "https://checkout.stripe.com/") {
		t.Fatalf("expected Stripe hosted URL, got %s", intent.HostedURL)
	}
	if intent.PSPRef != "cs_live_1" {
		t.Fatalf("session id not stored as psp ref: %s", intent.PSPRef)
	}
}

func TestPaymentLinkSandboxFallbackWithoutKey(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	if _, err := s.Catalog.Add(ctx, &model.CatalogService{
		TenantID: tenantID, Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Sedan|business",
		BaseMinor: 9000, Currency: "EUR", PerKMMinor: 250, AutoQuotable: true,
	}); err != nil {
		t.Fatal(err)
	}
	res, _ := s.Clients.Resolve(ctx, tenantID, "+31633334444", "", "Booker")
	sr, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
		TenantID: tenantID, ClientID: res.Client.ID, IdempotencyKey: "sr-sandbox",
		Items: []model.ServiceItem{{
			Vertical:  "CHAUFFEUR_POINT_TO_POINT",
			PickupLat: 52.37, PickupLng: 4.90, DropoffLat: 52.31, DropoffLng: 4.76,
			PickupAt: 1733500000, Passengers: 1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	q, _, err := s.Booking.QuoteRequest(ctx, tenantID, sr.ID)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := s.Payments.CreatePaymentLink(ctx, tenantID, q.ID, "FULL", "api", "pay-sandbox-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(intent.HostedURL, "https://pay.concierge.engine/p/") {
		t.Fatalf("sandbox URL expected, got %s", intent.HostedURL)
	}
}
