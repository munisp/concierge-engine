// Phase 15 domain tests: CRM-360 (profile update, consent write-path gating
// outreach, search, merge, 360 aggregation), international payments (PSP
// routing, Paystack initialize+webhook, refunds, currency hygiene, methods
// discovery), integration hardening (replay, rotation, test event, catalog).
package domain

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// ------------------------------------------------------------- CRM-360

func TestUpdateProfile(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000200", false)

	name, locale, tz := "Ada Lovelace", "en-NG", "Africa/Lagos"
	c, err := s.Clients.UpdateProfile(ctx, tenantID, clientID, UpdateProfileInput{
		FullName: &name, Locale: &locale, Timezone: &tz,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.FullName != "Ada Lovelace" || c.Locale != "en-NG" || c.Timezone != "Africa/Lagos" {
		t.Fatalf("profile not applied: %+v", c)
	}
	// Roundtrip through the store.
	got, err := s.Store.Clients().Get(ctx, tenantID, clientID)
	if err != nil || got.Locale != "en-NG" || got.Timezone != "Africa/Lagos" {
		t.Fatalf("store roundtrip: %+v %v", got, err)
	}
	// No-op update touches nothing.
	c2, err := s.Clients.UpdateProfile(ctx, tenantID, clientID, UpdateProfileInput{Locale: &locale})
	if err != nil || c2.FullName != "Ada Lovelace" {
		t.Fatalf("no-op update: %+v %v", c2, err)
	}
}

func TestConsentWritePathGatesOutreach(t *testing.T) {
	calls := 0
	s := testServices(t, Deps{WhatsAppURL: waCountingStub(t, &calls).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000201", false)

	// Denied without consent...
	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachMarketing, Body: "offer", IdempotencyKey: "c1"})
	if err != nil || o.Status != "held" {
		t.Fatalf("pre-consent: %+v %v", o, err)
	}
	// ...granted via the Phase 15 consent API...
	if _, err := s.Clients.SetConsent(ctx, tenantID, clientID, "marketing", true); err != nil {
		t.Fatal(err)
	}
	v, err := s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing)
	if err != nil || !v.Allowed || !v.MarketingConsent {
		t.Fatalf("post-consent verdict: %+v %v", v, err)
	}
	// ...revoked again.
	if _, err := s.Clients.SetConsent(ctx, tenantID, clientID, "marketing", false); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing)
	if v.Allowed {
		t.Fatal("revoked consent must re-gate outreach")
	}
}

func TestClientSearchAndMerge(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	// Same human, two channel identities (WhatsApp + web capture).
	winner, _ := s.Clients.Resolve(ctx, tenantID, "+971500000202", "", "Ada")
	loser, _ := s.Clients.Resolve(ctx, tenantID, "", "ada@example.com", "Ada Lovelace")

	found, err := s.Clients.Search(ctx, tenantID, "ada", 10)
	if err != nil || len(found) != 2 {
		t.Fatalf("search: %+v %v", found, err)
	}
	if _, err := s.Clients.Search(ctx, tenantID, "", 10); err == nil {
		t.Fatal("empty query must be rejected")
	}

	// Loser has a case and a request; winner has a conversation.
	if _, err := s.Cases.Open(ctx, tenantID, loser.Client.ID, "lost bag", "bag missing", "m-case-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
		TenantID: tenantID, ClientID: loser.Client.ID, IdempotencyKey: "m-req-1",
		Items: []model.ServiceItem{{Vertical: "CHAUFFEUR_POINT_TO_POINT", PickupAddress: "A"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Conversations.Open(ctx, tenantID, winner.Client.ID, "WHATSAPP", "wamid.m1"); err != nil {
		t.Fatal(err)
	}

	if err := s.Clients.Merge(ctx, tenantID, winner.Client.ID, loser.Client.ID); err != nil {
		t.Fatal(err)
	}
	// Loser gone; records re-pointed.
	if _, err := s.Store.Clients().Get(ctx, tenantID, loser.Client.ID); err == nil {
		t.Fatal("loser must be deleted")
	}
	cases, _ := s.Store.Cases().ForClient(ctx, tenantID, winner.Client.ID, 10)
	if len(cases) != 1 {
		t.Fatalf("case not re-pointed: %+v", cases)
	}
	reqs, _ := s.Store.Requests().ForClient(ctx, tenantID, winner.Client.ID, 10)
	if len(reqs) != 1 {
		t.Fatalf("request not re-pointed: %+v", reqs)
	}
	convs, _ := s.Store.Conversations().ForClient(ctx, tenantID, winner.Client.ID, 10)
	if len(convs) != 1 {
		t.Fatalf("conversation missing: %+v", convs)
	}
	// Search now finds only the winner.
	found, _ = s.Clients.Search(ctx, tenantID, "ada", 10)
	if len(found) != 1 {
		t.Fatalf("post-merge search: %+v", found)
	}
}

func TestClient360(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000203", false)

	if _, err := s.Clients.SavePreference(ctx, tenantID, clientID, "seat", "window", "service", "", "test"); err != nil {
		t.Fatal(err)
	}
	s.Events.Track(ctx, &model.AttributionEvent{TenantID: tenantID, Kind: "page_view",
		Channel: "seo", Campaign: "launch", ClientID: clientID})
	s.Events.Track(ctx, &model.AttributionEvent{TenantID: tenantID, Kind: "booking",
		Channel: "seo", Campaign: "launch", ClientID: clientID, ValueMinor: 9000})
	if _, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
		TenantID: tenantID, ClientID: clientID, IdempotencyKey: "v360-1",
		Items: []model.ServiceItem{{Vertical: "CHAUFFEUR_POINT_TO_POINT", PickupAddress: "A"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cases.Open(ctx, tenantID, clientID, "question", "billing question", "v360-c1"); err != nil {
		t.Fatal(err)
	}

	v, err := s.Clients.View360(ctx, tenantID, clientID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Client.ID != clientID || v.ReferralCode == "" {
		t.Fatalf("identity: %+v", v.Client)
	}
	if len(v.Preferences) != 1 || len(v.Requests) != 1 || len(v.Cases) != 1 {
		t.Fatalf("sections: prefs=%d reqs=%d cases=%d",
			len(v.Preferences), len(v.Requests), len(v.Cases))
	}
	if v.FirstTouch == nil || v.FirstTouch.Channel != "seo" {
		t.Fatalf("first touch: %+v", v.FirstTouch)
	}
	if v.SpendMinor != 9000 {
		t.Fatalf("spend: %d", v.SpendMinor)
	}
}

// ------------------------------------------------------------- international payments

func paystackStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/transaction/initialize" {
			json.NewEncoder(w).Encode(map[string]any{
				"status": true,
				"data": map[string]string{
					"authorization_url": "https://checkout.paystack.com/abc123",
					"reference":         "ref-initialized",
				}})
			return
		}
		if r.URL.Path == "/refund" {
			json.NewEncoder(w).Encode(map[string]any{"status": true})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPSPRouting(t *testing.T) {
	s := testServices(t, Deps{})
	s.Paystack = NewPaystackClient("sk_test", paystackStub(t).URL)
	s.Stripe = NewStripeClient("sk_test", "https://api.stripe.com", "", time.Now)

	if got := s.Payments.choosePSP("NGN", ""); got != "paystack" {
		t.Fatalf("NGN must route to paystack, got %s", got)
	}
	if got := s.Payments.choosePSP("EUR", ""); got != "stripe" {
		t.Fatalf("EUR must route to stripe, got %s", got)
	}
	if got := s.Payments.choosePSP("EUR", "paystack"); got != "paystack" {
		t.Fatalf("explicit override: %s", got)
	}
	// Neither live → sandbox.
	s2 := testServices(t, Deps{})
	s2.Paystack = NewPaystackClient("", "")
	s2.Stripe = NewStripeClient("", "", "", time.Now)
	if got := s2.Payments.choosePSP("NGN", ""); got != "sandbox" {
		t.Fatalf("no PSP configured must degrade to sandbox, got %s", got)
	}
}

func TestPaystackEndToEnd(t *testing.T) {
	s := testServices(t, Deps{})
	key := "sk_test_paystack"
	s.Paystack = NewPaystackClient(key, paystackStub(t).URL)
	s.Stripe = NewStripeClient("", "", "", time.Now)
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+2348012345678", false)

	if _, err := s.Catalog.Add(ctx, &model.CatalogService{
		TenantID: tenantID, Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Sedan|business",
		BaseMinor: 9000, Currency: "NGN", PerKMMinor: 250, AutoQuotable: true, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	sr, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
		TenantID: tenantID, ClientID: clientID, IdempotencyKey: "ps-1",
		Items: []model.ServiceItem{{
			Vertical: "CHAUFFEUR_POINT_TO_POINT", PickupAddress: "Lekki", DropoffAddress: "VI",
			PickupLat: 6.44, PickupLng: 3.47, DropoffLat: 6.43, DropoffLng: 3.42,
			PickupAt: 1733500000, Passengers: 1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	q, instant, err := s.Booking.QuoteRequest(ctx, tenantID, sr.ID)
	if err != nil || !instant {
		t.Fatalf("quote: %v %v", err, instant)
	}
	intent, err := s.Payments.CreatePaymentLinkV2(ctx, tenantID, CreatePaymentLinkInput{
		QuoteID: q.ID, Mode: "FULL", IdempotencyKey: "ps-pay-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if intent.PSP != "paystack" || intent.HostedURL != "https://checkout.paystack.com/abc123" {
		t.Fatalf("paystack link: %+v", intent)
	}

	// Signed webhook captures the intent and settles the ledger splits.
	body := []byte(fmt.Sprintf(
		`{"event":"charge.success","data":{"reference":%q,"metadata":{"tenant_id":%q}}}`,
		intent.ID, tenantID))
	mac := hmac.New(sha512.New, []byte(key))
	mac.Write(body)
	if !s.Paystack.VerifyWebhook(hex.EncodeToString(mac.Sum(nil)), body) {
		t.Fatal("valid signature rejected")
	}
	if s.Paystack.VerifyWebhook("deadbeef", body) {
		t.Fatal("forged signature accepted")
	}
	if err := s.Payments.HandlePaystackWebhook(ctx, tenantID, "charge.success", intent.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Payments.GetIntent(ctx, tenantID, intent.ID)
	if got.State != "CAPTURED" {
		t.Fatalf("intent state: %s", got.State)
	}
	postings, _ := s.Payments.Postings(ctx, tenantID)
	if len(postings) != 2 {
		t.Fatalf("settlement splits: %+v", postings)
	}
}

func TestRefundFlow(t *testing.T) {
	s := testServices(t, Deps{})
	s.Paystack = NewPaystackClient("sk_test", paystackStub(t).URL)
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	intent := &model.PaymentIntent{
		ID: NewID(), TenantID: tenantID, QuoteID: "q-1", Mode: "FULL",
		AmountMinor: 5000, Currency: "NGN", PSP: "paystack", PSPRef: "ref-1",
		State: "CAPTURED", IdempotencyKey: "rf-1", CreatedAt: s.Now(),
	}
	if err := s.Store.Payments().CreateIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	got, err := s.Payments.Refund(ctx, tenantID, intent.ID)
	if err != nil || got.State != "REFUND_PENDING" {
		t.Fatalf("refund: %+v %v", got, err)
	}
	// Replay is a no-op, not a second refund.
	again, err := s.Payments.Refund(ctx, tenantID, intent.ID)
	if err != nil || again.State != "REFUND_PENDING" {
		t.Fatalf("refund replay: %+v %v", again, err)
	}
	// Non-captured money cannot be refunded.
	intent2 := &model.PaymentIntent{
		ID: NewID(), TenantID: tenantID, Mode: "FULL", AmountMinor: 100,
		Currency: "NGN", PSP: "paystack", State: "LINK_SENT",
		IdempotencyKey: "rf-2", CreatedAt: s.Now(),
	}
	if err := s.Store.Payments().CreateIntent(ctx, intent2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Payments.Refund(ctx, tenantID, intent2.ID); err != httpx.ErrConflict {
		t.Fatalf("uncaptured refund must conflict, got %v", err)
	}
}

func TestPaymentMethodsDiscovery(t *testing.T) {
	s := testServices(t, Deps{})
	s.Paystack = NewPaystackClient("sk_test", paystackStub(t).URL)
	s.Stripe = NewStripeClient("", "", "", time.Now)

	methods, err := s.Payments.PaymentMethods("NGN")
	if err != nil {
		t.Fatal(err)
	}
	if len(methods) != 2 || methods[0].PSP != "paystack" || !methods[0].Live {
		t.Fatalf("NGN methods: %+v", methods)
	}
	if methods[1].PSP != "stripe" || methods[1].Live {
		t.Fatalf("stripe must be listed but not live: %+v", methods[1])
	}
	if _, err := s.Payments.PaymentMethods("US"); err != httpx.ErrValidation {
		t.Fatalf("malformed currency: %v", err)
	}
	if MinorExponent("JPY") != 0 || MinorExponent("USD") != 2 || MinorExponent("NGN") != 2 {
		t.Fatal("minor exponents wrong")
	}
	if err := validateCurrency("usd"); err != nil {
		t.Fatalf("lowercase must normalize: %v", err)
	}
	if err := validateCurrency("U1D"); err != httpx.ErrValidation {
		t.Fatalf("digits must be rejected: %v", err)
	}
}

// ------------------------------------------------------------- events & integrations

func TestWebhookReplayRotateTest(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	sub, err := s.Webhooks.CreateSubscription(ctx, tenantID, "https://receiver.example/hooks", []string{"booking.*"})
	if err != nil {
		t.Fatal(err)
	}

	// Test event enqueues a synthetic delivery.
	d, err := s.Webhooks.TestSubscription(ctx, tenantID, sub.ID)
	if err != nil || d.EventKind != "webhook.test" {
		t.Fatalf("test event: %+v %v", d, err)
	}

	// Replay resets a parked delivery.
	d.Attempts = 8
	d.LastError = "connection refused"
	if err := s.Store.Webhooks().UpdateDelivery(ctx, d); err != nil {
		t.Fatal(err)
	}
	replayed, err := s.Webhooks.ReplayDelivery(ctx, tenantID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Attempts != 0 || replayed.LastError != "" || !replayed.DeliveredAt.IsZero() {
		t.Fatalf("replay must reset: %+v", replayed)
	}

	// Rotation changes the secret; the old one is gone.
	old := sub.Secret
	rotated, err := s.Webhooks.RotateSecret(ctx, tenantID, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Secret == old || !strings.HasPrefix(rotated.Secret, "whk_") {
		t.Fatalf("rotation: %s vs %s", old, rotated.Secret)
	}
	reloaded, _ := s.Store.Webhooks().GetSubscription(ctx, tenantID, sub.ID)
	if reloaded.Secret != rotated.Secret {
		t.Fatal("rotated secret not persisted")
	}
}

func TestEventCatalog(t *testing.T) {
	if len(EventCatalog) < 40 {
		t.Fatalf("catalog too thin: %d entries", len(EventCatalog))
	}
	seen := map[string]bool{}
	for _, e := range EventCatalog {
		if e.Kind == "" || e.Aggregate == "" || e.Description == "" {
			t.Fatalf("incomplete entry: %+v", e)
		}
		if seen[e.Kind] {
			t.Fatalf("duplicate kind %q", e.Kind)
		}
		seen[e.Kind] = true
	}
	for _, must := range []string{"booking.confirmed", "payment.captured", "case.sla_breach",
		"outreach.sent", "client.merged", "referral.converted", "webhook.test"} {
		if !seen[must] {
			t.Fatalf("catalog missing %q", must)
		}
	}
}
