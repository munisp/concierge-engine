// Phase 3 domain tests: public API keys (auth, revocation, scopes), the
// webhooks marketplace (fan-out, HMAC-signed dispatch, backoff), the
// cross-tenant referral network (offer→accept→settle, commission posting,
// metrics), the Amadeus GDS adapter (live parse + token caching + mock
// degrade), new-entrant instant-on, and residency validation.
package domain

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// ------------------------------------------------------------- API keys

func TestApiKeyLifecycle(t *testing.T) {
	s := testServices(t, Deps{})
	tenantID := seedTenant(t, s)
	ctx := context.Background()

	key, secret, err := s.ApiKeys.Create(ctx, tenantID, "partner-integration", []string{"read", "write"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(secret, "cek_") || len(secret) != 52 {
		t.Fatalf("secret shape wrong: %q", secret)
	}
	if key.SecretHash == "" || key.SecretHash == secret {
		t.Fatal("secret must be hashed, never stored raw")
	}

	gotTenant, scopes, err := s.ApiKeys.Authenticate(ctx, "Bearer "+secret)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if gotTenant != tenantID || !HasScope(scopes, "read") || !HasScope(scopes, "write") {
		t.Fatalf("bad auth result: %s %v", gotTenant, scopes)
	}

	if _, _, err := s.ApiKeys.Authenticate(ctx, "Bearer "+secret[:len(secret)-2]+"xx"); err != httpx.ErrForbidden {
		t.Fatalf("wrong secret must be forbidden, got %v", err)
	}
	if _, _, err := s.ApiKeys.Authenticate(ctx, "Basic abc"); err != httpx.ErrForbidden {
		t.Fatalf("non-cek scheme must be forbidden, got %v", err)
	}

	if err := s.ApiKeys.Revoke(ctx, tenantID, key.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := s.ApiKeys.Authenticate(ctx, "Bearer "+secret); err != httpx.ErrForbidden {
		t.Fatalf("revoked key must be forbidden, got %v", err)
	}

	if _, _, err := s.ApiKeys.Create(ctx, tenantID, "bad", []string{"admin"}); err != httpx.ErrValidation {
		t.Fatalf("unknown scope must be validation error, got %v", err)
	}
}

// ------------------------------------------------------------- webhooks

func TestWebhookFanOutAndSignedDispatch(t *testing.T) {
	s := testServices(t, Deps{})
	tenantID := seedTenant(t, s)
	ctx := context.Background()

	var gotSig, gotEvent, gotBody string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Concierge-Signature")
		gotEvent = r.Header.Get("X-Concierge-Event")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	sub, err := s.Webhooks.CreateSubscription(ctx, tenantID, receiver.URL, []string{"client.*"})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if !strings.HasPrefix(sub.Secret, "whk_") {
		t.Fatal("subscription must have a whk_ signing secret")
	}

	// FanOut runs synchronously via the EventBus hook installed in NewServices.
	s.Events.Emit(ctx, tenantID, "client", "c1", "client.created", map[string]any{"name": "Ada"})
	s.Events.Emit(ctx, tenantID, "quote", "q1", "quote.created", nil) // not subscribed

	due, err := s.Store.Webhooks().DueDeliveries(ctx, s.Now(), 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 1 || due[0].EventKind != "client.created" {
		t.Fatalf("expected exactly one matching delivery, got %+v", due)
	}

	delivered, err := s.Webhooks.DispatchDue(ctx, s.Now(), 10)
	if err != nil || delivered != 1 {
		t.Fatalf("dispatch: %d, %v", delivered, err)
	}
	if gotEvent != "client.created" {
		t.Fatalf("receiver got event %q", gotEvent)
	}
	if gotSig != SignDelivery(sub.Secret, []byte(gotBody)) {
		t.Fatal("signature mismatch — receiver could not verify")
	}
	var payload struct {
		Type     string         `json:"type"`
		TenantID string         `json:"tenant_id"`
		Data     map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil || payload.TenantID != tenantID || payload.Type != "client.created" {
		t.Fatalf("payload malformed: %v %s", err, gotBody)
	}
}

func TestWebhookBackoffOnFailure(t *testing.T) {
	s := testServices(t, Deps{})
	tenantID := seedTenant(t, s)
	ctx := context.Background()

	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fail.Close()

	if _, err := s.Webhooks.CreateSubscription(ctx, tenantID, fail.URL, []string{"*"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	s.Events.Emit(ctx, tenantID, "booking", "b1", "booking.confirmed", nil)

	delivered, err := s.Webhooks.DispatchDue(ctx, s.Now(), 10)
	if err != nil || delivered != 0 {
		t.Fatalf("expected 0 delivered, got %d, %v", delivered, err)
	}
	dlvs, _ := s.Webhooks.ListDeliveries(ctx, tenantID, 10)
	if len(dlvs) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(dlvs))
	}
	d := dlvs[0]
	if d.Attempts != 1 || d.LastStatus != 500 || !d.DeliveredAt.IsZero() {
		t.Fatalf("attempt record wrong: %+v", d)
	}
	if !d.NextAttemptAt.After(s.Now()) {
		t.Fatal("failure must schedule a backoff retry")
	}
	// Not due again immediately.
	due, _ := s.Store.Webhooks().DueDeliveries(ctx, s.Now(), 10)
	if len(due) != 0 {
		t.Fatal("backoff delivery must not be due immediately")
	}
}

// ------------------------------------------------------------- referrals

func seedTenantOptIn(t *testing.T, s *Services, slug string, optIn bool) string {
	t.Helper()
	tenant, err := s.Tenants.Create(context.Background(), &model.Tenant{
		ID: NewID(), Slug: slug + "-" + NewID()[:8], Name: slug,
		AcceptsReferrals: optIn,
	})
	if err != nil {
		t.Fatalf("seed tenant %s: %v", slug, err)
	}
	return tenant.ID
}

func TestReferralLifecycle(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	referrer := seedTenantOptIn(t, s, "referrer", false)
	receiver := seedTenantOptIn(t, s, "receiver", true)
	stranger := seedTenantOptIn(t, s, "stranger", false)

	// Receiver not opted in -> conflict.
	ref, err := s.Referrals.Offer(ctx, referrer, stranger, "Ada", "ada@x.com", "AMS->LHR + hotel", 150000, "EUR", "k1")
	if err != httpx.ErrConflict {
		t.Fatalf("offer to non-opted-in tenant must conflict, got %v", err)
	}

	ref, err = s.Referrals.Offer(ctx, referrer, receiver, "Ada", "ada@x.com", "AMS->LHR + hotel", 150000, "EUR", "k2")
	if err != nil {
		t.Fatalf("offer: %v", err)
	}
	if ref.State != model.ReferralOffered || ref.FeePct != 0.10 {
		t.Fatalf("offer state/fee wrong: %+v", ref)
	}

	// Only the receiver can accept.
	if _, err := s.Referrals.Accept(ctx, referrer, ref.ID, "req-1"); err != httpx.ErrForbidden {
		t.Fatalf("referrer must not accept, got %v", err)
	}
	// Bilateral visibility: stranger sees nothing.
	if _, err := s.Store.Referrals().Get(ctx, stranger, ref.ID); err == nil {
		t.Fatal("stranger tenant must not read the referral (bilateral visibility)")
	}
	list, _ := s.Referrals.List(ctx, stranger)
	if len(list) != 0 {
		t.Fatal("stranger tenant must not list the referral")
	}

	if _, err := s.Referrals.Accept(ctx, receiver, ref.ID, "req-1"); err != nil {
		t.Fatalf("accept: %v", err)
	}

	// Settle: 10% of 5000.00 EUR = 500.00 EUR commission. Ledger is absent in
	// tests; the posting queues via outbox (money truth stays authoritative).
	settled, err := s.Referrals.Settle(ctx, receiver, ref.ID, "bk-1", 500000)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if settled.State != model.ReferralSettled || settled.CommissionMinor != 50000 {
		t.Fatalf("settle wrong: %+v", settled)
	}
	// Absorbing: replay is a no-op with the same commission.
	again, err := s.Referrals.Settle(ctx, receiver, ref.ID, "bk-1", 500000)
	if err != nil || again.CommissionMinor != 50000 {
		t.Fatalf("settle must be absorbing, got %+v, %v", again, err)
	}

	m, err := s.Referrals.Metrics(ctx, receiver)
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if m.Accepted != 1 || m.Settled != 1 || m.CommissionPaid != 50000 || m.AcceptRate != 1.0 {
		t.Fatalf("receiver metrics wrong: %+v", m)
	}
	mr, _ := s.Referrals.Metrics(ctx, referrer)
	if mr.Offered != 1 || mr.CommissionEarned != 50000 {
		t.Fatalf("referrer metrics wrong: %+v", mr)
	}
}

func TestReferralDecline(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	referrer := seedTenantOptIn(t, s, "ref", false)
	receiver := seedTenantOptIn(t, s, "recv", true)

	ref, err := s.Referrals.Offer(ctx, referrer, receiver, "Bo", "bo@x.com", "CDG transfer", 80000, "EUR", "k3")
	if err != nil {
		t.Fatalf("offer: %v", err)
	}
	d, err := s.Referrals.Decline(ctx, receiver, ref.ID, "no coverage")
	if err != nil || d.State != model.ReferralDeclined {
		t.Fatalf("decline: %+v, %v", d, err)
	}
	if _, err := s.Referrals.Settle(ctx, receiver, ref.ID, "bk-x", 1000); err != httpx.ErrConflict {
		t.Fatalf("declined referral must not settle, got %v", err)
	}
}

// ------------------------------------------------------------- GDS

func amadeusFlightResponse() string {
	return `{"data":[{"id":"1","price":{"total":"412.50","currency":"EUR"},
		"itineraries":[{"segments":[
		  {"departure":{"iataCode":"AMS","at":"2026-12-01T08:15:00"},
		   "arrival":{"iataCode":"LHR","at":"2026-12-01T09:05:00"},
		   "carrierCode":"KL","number":"1001"}]}]}]}`
}

func TestGDSMockDegrade(t *testing.T) {
	// No credentials -> deterministic mock, with the real search params.
	s := testServices(t, Deps{})
	tenantID := seedTenant(t, s)
	offers, source, err := s.GDS.SearchFlights(context.Background(), tenantID, "AMS", "LHR", "2026-12-01", 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if source != "mock" || len(offers) != 2 {
		t.Fatalf("expected 2 mock offers, got %d (%s)", len(offers), source)
	}
	if offers[0].Origin != "AMS" || offers[0].Destination != "LHR" || offers[0].TotalMinor != 97800 {
		t.Fatalf("mock must echo real params: %+v", offers[0])
	}
}

func TestGDSAmadeusLive(t *testing.T) {
	var tokenCalls int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/oauth2/token"):
			atomic.AddInt32(&tokenCalls, 1)
			w.Write([]byte(`{"access_token":"tok-1","expires_in":1799}`))
		case strings.HasSuffix(r.URL.Path, "/flight-offers"):
			if r.Header.Get("Authorization") != "Bearer tok-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Write([]byte(amadeusFlightResponse()))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer fake.Close()

	s := testServices(t, Deps{AmadeusBase: fake.URL, AmadeusID: "id", AmadeusSecret: "sec"})
	tenantID := seedTenant(t, s)
	ctx := context.Background()

	offers, source, err := s.GDS.SearchFlights(ctx, tenantID, "AMS", "LHR", "2026-12-01", 1)
	if err != nil {
		t.Fatalf("live search: %v", err)
	}
	if source != "amadeus" || len(offers) != 1 {
		t.Fatalf("expected 1 amadeus offer, got %d (%s)", len(offers), source)
	}
	o := offers[0]
	if o.Origin != "AMS" || o.Destination != "LHR" || o.FlightNo != "KL1001" || o.TotalMinor != 41250 || o.Currency != "EUR" {
		t.Fatalf("parse wrong: %+v", o)
	}
	// Second search reuses the cached token.
	if _, _, err := s.GDS.SearchFlights(ctx, tenantID, "AMS", "LHR", "2026-12-02", 1); err != nil {
		t.Fatalf("second search: %v", err)
	}
	if atomic.LoadInt32(&tokenCalls) != 1 {
		t.Fatalf("token must be cached; got %d token calls", tokenCalls)
	}
}

func TestGDSLiveFailureFallsBackToMock(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth2/token") {
			w.Write([]byte(`{"access_token":"tok-1","expires_in":1799}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fake.Close()

	s := testServices(t, Deps{AmadeusBase: fake.URL, AmadeusID: "id", AmadeusSecret: "sec"})
	tenantID := seedTenant(t, s)
	offers, source, err := s.GDS.SearchFlights(context.Background(), tenantID, "AMS", "JFK", "2026-12-05", 3)
	if err != nil {
		t.Fatalf("degraded search must not error: %v", err)
	}
	if source != "mock" || len(offers) != 2 {
		t.Fatalf("expected mock fallback, got %s (%d offers)", source, len(offers))
	}
	if offers[0].Origin != "AMS" || offers[0].Destination != "JFK" || offers[0].TotalMinor != 146700 {
		t.Fatalf("fallback must preserve real search params: %+v", offers[0])
	}
}

func TestGDSAttach(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	b := &model.Booking{ID: NewID(), TenantID: tenantID, RequestID: "r1", QuoteID: "q1", State: "CONFIRMED"}
	if err := s.Store.Bookings().Create(ctx, b); err != nil {
		t.Fatalf("seed booking: %v", err)
	}
	if err := s.GDS.AttachOffer(ctx, tenantID, b.ID, "flight", "amz-offer-1", 41250, "EUR"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := s.GDS.AttachOffer(ctx, tenantID, b.ID, "yacht", "x", 1, "EUR"); err == nil {
		t.Fatal("unknown offer type must be rejected")
	}
	if err := s.GDS.AttachOffer(ctx, tenantID, "nope", "flight", "x", 1, "EUR"); err == nil {
		t.Fatal("attach to missing booking must fail")
	}
}

// ------------------------------------------------------------- instant-on & residency

func TestInstantOn(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()

	tenant, seeded, err := s.InstantOn.Provision(ctx, "Concierge Co", "concierge-co", "", "")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if tenant.Archetype != "new_entrant" || tenant.Region != "EU" {
		t.Fatalf("instant-on tenant wrong: %+v", tenant)
	}
	if tenant.PricingRules.Currency != "EUR" || tenant.PricingRules.DepositPct != 0.30 {
		t.Fatalf("pricing defaults wrong: %+v", tenant.PricingRules)
	}
	if seeded != len(instantOnCatalog) {
		t.Fatalf("expected %d seeded services, got %d", len(instantOnCatalog), seeded)
	}
	cat, err := s.Catalog.List(ctx, tenant.ID)
	if err != nil || len(cat) != seeded {
		t.Fatalf("catalog: %d, %v", len(cat), err)
	}
	for _, c := range cat {
		if !c.Active {
			t.Fatalf("seeded service %s must be active (visible to quote engine)", c.Name)
		}
	}
	if _, _, err := s.InstantOn.Provision(ctx, "", "x", "", ""); err != httpx.ErrValidation {
		t.Fatalf("missing name must be validation, got %v", err)
	}
}

func TestResidencyValidation(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	if _, err := s.Tenants.Create(ctx, &model.Tenant{Slug: "bad-region-x", Name: "X", Region: "US"}); err != httpx.ErrValidation {
		t.Fatalf("unsupported region must be validation, got %v", err)
	}
	uae, err := s.Tenants.Create(ctx, &model.Tenant{Slug: "dubai-ops-x", Name: "Dubai Ops", Region: "UAE"})
	if err != nil || uae.Region != "UAE" {
		t.Fatalf("UAE residency: %v", err)
	}
}
