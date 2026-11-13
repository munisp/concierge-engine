// Phase 2 domain tests: crypto envelope, chauffeur trip tokens, the Mojaloop
// payout saga (fulfil + timeout compensation), partner metrics, corporate
// invoicing and the sensitive-read audit log.
package domain

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
	"concierge-engine/services/core-api/internal/store"
)

func testServices(t *testing.T, deps Deps) *Services {
	t.Helper()
	return NewServices(store.NewMemory(), deps)
}

func seedTenant(t *testing.T, s *Services) string {
	t.Helper()
	tenant, err := s.Tenants.Create(context.Background(), &model.Tenant{
		ID: NewID(), Slug: "taylor-" + NewID()[:8], Name: "Taylor", Archetype: "flagship",
	})
	if err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return tenant.ID
}

// ------------------------------------------------------------- crypto
func TestFieldCipherRoundtrip(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	c, err := NewFieldCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := c.Encrypt("allergic to shellfish")
	if err != nil {
		t.Fatal(err)
	}
	if enc == "allergic to shellfish" {
		t.Fatal("expected ciphertext")
	}
	dec, err := c.Decrypt(enc)
	if err != nil {
		t.Fatal(err)
	}
	if dec != "allergic to shellfish" {
		t.Fatalf("roundtrip mismatch: %q", dec)
	}
	if got, _ := c.Decrypt("plain"); got != "plain" {
		t.Fatalf("plaintext passthrough broken: %q", got)
	}
	off, _ := NewFieldCipher("")
	if off.Enabled() {
		t.Fatal("expected disabled cipher")
	}
	if got, _ := off.Encrypt("x"); got != "x" {
		t.Fatal("disabled cipher must pass through")
	}
}

func TestVIPPreferenceEncryptedAtRest(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	s := testServices(t, Deps{FieldKeyB64: key})
	tenantID := seedTenant(t, s)
	ctx := context.Background()

	res, err := s.Clients.Resolve(ctx, tenantID, "+31612345678", "", "VIP Client")
	if err != nil {
		t.Fatal(err)
	}
	vip := res.Client
	vip.VIPFlags = []string{"vip"}
	if err := s.Store.Clients().Upsert(ctx, vip); err != nil {
		t.Fatal(err)
	}
	p, err := s.Clients.SavePreference(ctx, tenantID, vip.ID, "dietary", "no shellfish", "service", "", "ai")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Encrypted {
		t.Fatal("VIP preference must be flagged encrypted")
	}
	stored, err := s.Store.Clients().Preferences(ctx, tenantID, vip.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Value == "no shellfish" {
		t.Fatal("stored VIP preference must be ciphertext, not plaintext")
	}
	prefs, err := s.Clients.Preferences(ctx, tenantID, vip.ID)
	if err != nil {
		t.Fatal(err)
	}
	if prefs[0].Value != "no shellfish" {
		t.Fatalf("decrypt on read broken: %q", prefs[0].Value)
	}
}

// ------------------------------------------------------------- audit
func TestSensitiveReadsAreAudited(t *testing.T) {
	s := testServices(t, Deps{})
	tenantID := seedTenant(t, s)
	ctx := context.Background()
	res, _ := s.Clients.Resolve(ctx, tenantID, "+31600000000", "", "A Reader")
	if _, err := s.Clients.SavePreference(ctx, tenantID, res.Client.ID, "seat", "aisle", "service", "", "ai"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Clients.Get(ctx, tenantID, res.Client.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Clients.Preferences(ctx, tenantID, res.Client.ID); err != nil {
		t.Fatal(err)
	}
	entries, err := s.Audit.List(ctx, tenantID, 50)
	if err != nil {
		t.Fatal(err)
	}
	var sawClient, sawPrefs bool
	for _, e := range entries {
		if e.Action == "client.read" {
			sawClient = true
		}
		if e.Action == "preferences.read" {
			sawPrefs = true
		}
	}
	if !sawClient || !sawPrefs {
		t.Fatalf("sensitive reads not audited (client=%v prefs=%v)", sawClient, sawPrefs)
	}
}

// ------------------------------------------------------------- trip tokens
func TestChauffeurTripFlow(t *testing.T) {
	s := testServices(t, Deps{TripSecret: "test-secret"})
	tenantID := seedTenant(t, s)
	ctx := context.Background()

	res, _ := s.Clients.Resolve(ctx, tenantID, "+31611112222", "", "James de Vries")
	sr, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
		TenantID: tenantID, ClientID: res.Client.ID, IdempotencyKey: "trip-1",
		Items: []model.ServiceItem{{
			Vertical: "CHAUFFEUR_POINT_TO_POINT", PickupAddress: "Amstel Hotel",
			DropoffAddress: "Schiphol", PickupAt: 1733500000, Passengers: 2,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	booking := &model.Booking{ID: NewID(), TenantID: tenantID, RequestID: sr.ID, State: "CONFIRMED", CreatedAt: s.Now()}
	if err := s.Store.Bookings().Create(ctx, booking); err != nil {
		t.Fatal(err)
	}
	a, err := s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: booking.ID, PartnerID: "partner-x",
	})
	if err != nil {
		t.Fatal(err)
	}

	token := s.Trips.TokenFor(tenantID, a.ID)
	if _, _, err := s.Trips.ParseToken(token); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if _, _, err := s.Trips.ParseToken(token[:len(token)-2] + "zz"); err == nil {
		t.Fatal("tampered token accepted")
	}

	view, err := s.Trips.View(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if view.Pickup != "Amstel Hotel" || view.Passengers != 2 {
		t.Fatalf("trip view incomplete: %+v", view)
	}
	if view.ClientName != "James de V***" {
		t.Fatalf("client name not masked: %q", view.ClientName)
	}
	if err := s.Trips.Advance(ctx, token, "EN_ROUTE_TO_PICKUP"); err != nil {
		t.Fatalf("token status advance failed: %v", err)
	}
	if err := s.Trips.Advance(ctx, token, "COMPLETED_TRIP"); err == nil {
		t.Fatal("illegal transition via token accepted")
	}
	if err := s.Trips.Stamp(ctx, token, "pickup", 52.37, 4.90, 12.5); err != nil {
		t.Fatal(err)
	}
}

// ------------------------------------------------------------- payout saga
func fakeMojaloop(t *testing.T) *httptest.Server {
	t.Helper()
	var n int
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/quotes":
			n++
			_ = json.NewEncoder(w).Encode(MLQuoteResponse{
				QuoteID: fmt.Sprintf("q-%d", n), FXRate: 1.0, ReceiveMinor: 9500, FeesMinor: 300,
				ExpiresAt: "2030-01-01T00:00:00Z",
			})
		case "/v1/transfers":
			_ = json.NewEncoder(w).Encode(MLTransferResponse{TransferID: fmt.Sprintf("tr-%d", n), State: "PENDING"})
		default:
			http.NotFound(w, r)
		}
	}))
}

func vettedPartner(t *testing.T, s *Services, tenantID, rail string) *model.Partner {
	t.Helper()
	p, err := s.Partners.Create(context.Background(), &model.Partner{
		TenantID: tenantID, Name: "Safari Wings", PayoutRail: rail, PayoutRef: "fsiop-safari",
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.Partners.SetStatus(context.Background(), tenantID, p.ID, "VETTED")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPayoutSagaFulfilAndCompensate(t *testing.T) {
	adapter := fakeMojaloop(t)
	defer adapter.Close()
	s := testServices(t, Deps{MojaloopURL: adapter.URL, PublicBase: "http://core"})
	tenantID := seedTenant(t, s)
	ctx := context.Background()
	partner := vettedPartner(t, s, tenantID, "MOJALOOP")

	// Unvetted partners cannot be paid out.
	pending, _ := s.Partners.Create(ctx, &model.Partner{TenantID: tenantID, Name: "New Co"})
	if _, err := s.Payouts.Create(ctx, tenantID, pending.ID, "", 1000, "EUR", "po-x"); err == nil {
		t.Fatal("payout to non-vetted partner accepted")
	}

	// Full saga: PENDING -> QUOTED -> IN_FLIGHT -> SETTLED.
	p, err := s.Payouts.Create(ctx, tenantID, partner.ID, "b-1", 10000, "EUR", "po-1")
	if err != nil {
		t.Fatal(err)
	}
	if p.State != model.PayoutPending {
		t.Fatalf("state %q", p.State)
	}
	if p.Rail != "MOJALOOP" {
		t.Fatalf("rail not inherited from partner: %q", p.Rail)
	}
	// Idempotent create replay returns the same payout.
	replay, err := s.Payouts.Create(ctx, tenantID, partner.ID, "b-1", 10000, "EUR", "po-1")
	if err != nil || replay.ID != p.ID {
		t.Fatal("payout create not idempotent")
	}
	if _, err := s.Payouts.Execute(ctx, tenantID, p.ID); err == nil {
		t.Fatal("execute before quote accepted")
	}
	p, err = s.Payouts.Quote(ctx, tenantID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.QuoteID != "q-1" || p.ReceiveMinor != 9500 {
		t.Fatalf("quote not applied: %+v", p)
	}
	p, err = s.Payouts.Execute(ctx, tenantID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.State != model.PayoutInFlight || p.TransferID != "tr-1" {
		t.Fatalf("execute broken: %+v", p)
	}
	transfer1 := p.TransferID
	p, err = s.Payouts.HandleCallback(ctx, tenantID, transfer1, "transfer.fulfilled")
	if err != nil {
		t.Fatal(err)
	}
	if p.State != model.PayoutSettled {
		t.Fatalf("state %q", p.State)
	}
	// Absorbing state: replayed callback is a no-op, not an error.
	if _, err := s.Payouts.HandleCallback(ctx, tenantID, transfer1, "transfer.fulfilled"); err != nil {
		t.Fatal("callback replay not idempotent")
	}

	postings, err := s.Store.Payments().Postings(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	var reserve, fulfil, fxdiff bool
	for _, lp := range postings {
		switch lp.IdempotencyKey {
		case "payout:reserve:" + p.ID:
			reserve = lp.DebitCode == "PARTNER_PAYABLE" && lp.CreditCode == "IN_FLIGHT_SETTLEMENT" && lp.AmountMinor == 10000
		case "payout:fulfil:" + p.ID:
			fulfil = lp.CreditCode == "PARTNER_CLEARING" && lp.AmountMinor == 9500
		case "payout:fxdiff:" + p.ID:
			fxdiff = lp.CreditCode == "FX_GAIN_LOSS" && lp.AmountMinor == 500
		}
	}
	if !reserve || !fulfil || !fxdiff {
		t.Fatalf("ledger postings wrong (reserve=%v fulfil=%v fxdiff=%v)", reserve, fulfil, fxdiff)
	}

	// Timeout path: compensation restores the payable and pages ops.
	p2, err := s.Payouts.Create(ctx, tenantID, partner.ID, "b-2", 4000, "EUR", "po-2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Payouts.Quote(ctx, tenantID, p2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Payouts.Execute(ctx, tenantID, p2.ID); err != nil {
		t.Fatal(err)
	}
	p2, err = s.Payouts.HandleCallback(ctx, tenantID, "tr-2", "transfer.timeout")
	if err != nil {
		t.Fatal(err)
	}
	if p2.State != model.PayoutCompensated {
		t.Fatalf("state %q", p2.State)
	}
	postings, _ = s.Store.Payments().Postings(ctx, tenantID)
	var compensated bool
	for _, lp := range postings {
		if lp.IdempotencyKey == "payout:compensate:"+p2.ID &&
			lp.DebitCode == "IN_FLIGHT_SETTLEMENT" && lp.CreditCode == "PARTNER_PAYABLE" {
			compensated = true
		}
	}
	if !compensated {
		t.Fatal("compensation posting missing")
	}
	handoffs, err := s.Store.Handoffs().ListOpen(ctx, tenantID)
	if err != nil || len(handoffs) == 0 {
		t.Fatal("compensation did not page ops via HITL lane")
	}
}

// ------------------------------------------------------------- partners
func TestPartnerFillRateMetrics(t *testing.T) {
	s := testServices(t, Deps{})
	tenantID := seedTenant(t, s)
	ctx := context.Background()
	partner := vettedPartner(t, s, tenantID, "MANUAL")

	booking := &model.Booking{ID: NewID(), TenantID: tenantID, RequestID: "r-1", State: "CONFIRMED", CreatedAt: s.Now()}
	if err := s.Store.Bookings().Create(ctx, booking); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch.Assign(ctx, &model.Assignment{TenantID: tenantID, BookingID: booking.ID, PartnerID: partner.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch.Assign(ctx, &model.Assignment{TenantID: tenantID, BookingID: booking.ID, FleetResourceID: "fleet-1"}); err != nil {
		t.Fatal(err)
	}
	m, err := s.Partners.Metrics(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if m.TotalAssignments != 2 || m.PartnerAssignments != 1 || m.FillRate != 0.5 {
		t.Fatalf("fill rate wrong: %+v", m)
	}
	if m.VettedPartners != 1 {
		t.Fatalf("vetted count wrong: %+v", m)
	}
}

// ------------------------------------------------------------- corporate
func TestCorporateInvoiceRollup(t *testing.T) {
	s := testServices(t, Deps{})
	tenantID := seedTenant(t, s)
	ctx := context.Background()

	account, err := s.Corporate.CreateAccount(ctx, &model.CorporateAccount{
		TenantID: tenantID, Name: "Acme Holding", BillingEmail: "ap@acme.example",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Catalog -> request -> instant quote -> confirm (booking).
	if _, err := s.Catalog.Add(ctx, &model.CatalogService{
		TenantID: tenantID, Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Sedan|business",
		BaseMinor: 9000, Currency: "EUR", PerKMMinor: 250, AutoQuotable: true, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	res, _ := s.Clients.Resolve(ctx, tenantID, "+31699998888", "", "EA Booker")
	corpClient := res.Client
	corpClient.OrgID = account.ID
	if err := s.Store.Clients().Upsert(ctx, corpClient); err != nil {
		t.Fatal(err)
	}
	sr, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
		TenantID: tenantID, ClientID: corpClient.ID, IdempotencyKey: "corp-1",
		Items: []model.ServiceItem{{
			Vertical: "CHAUFFEUR_POINT_TO_POINT", PickupAddress: "A", DropoffAddress: "B",
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
	if _, err := s.Booking.Confirm(ctx, tenantID, q.ID); err != nil {
		t.Fatal(err)
	}

	start := s.Now().Add(-time.Hour)
	end := s.Now().Add(time.Hour)
	inv, err := s.Corporate.GenerateInvoice(ctx, tenantID, account.ID, start, end, "inv-2026-11")
	if err != nil {
		t.Fatal(err)
	}
	if inv.TotalMinor != q.TotalMinor {
		t.Fatalf("invoice total %d != quote total %d", inv.TotalMinor, q.TotalMinor)
	}
	// Idempotent per (account, period) key.
	again, err := s.Corporate.GenerateInvoice(ctx, tenantID, account.ID, start, end, "inv-2026-11")
	if err != nil || again.ID != inv.ID {
		t.Fatal("invoice generation not idempotent")
	}
	if _, err := s.Corporate.MarkInvoicePaid(ctx, tenantID, inv.ID); err != nil {
		t.Fatal(err)
	}
	postings, _ := s.Store.Payments().Postings(ctx, tenantID)
	var settled bool
	for _, lp := range postings {
		if lp.IdempotencyKey == "invoice:net:"+again.ID {
			settled = true
		}
	}
	if !settled {
		t.Fatal("invoice payment not settled into ledger")
	}
}

// ------------------------------------------------------------- supply
func TestCharterSupplyDegradesToMock(t *testing.T) {
	s := testServices(t, Deps{}) // no Avinode configured
	tenantID := seedTenant(t, s)
	options, err := s.Supply.SearchCharter(context.Background(), tenantID, "EHAM", "LFMN", s.Now(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(options) == 0 || options[0].Source != "mock" {
		t.Fatalf("expected mock supply, got %+v", options)
	}
}
