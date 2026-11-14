// Phase 14 domain tests: outreach gates (consent, frequency cap,
// idempotency, honest degrade), client referral codes, first-touch
// attribution inheritance, referral conversion, funnel aggregation.
package domain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/model"
)

// waCountingStub is a fake channel adapter counting /v1/send calls.
func waCountingStub(t *testing.T, calls *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		json.NewEncoder(w).Encode(map[string]any{"mode": "freeform",
			"body": map[string]any{"messages": []map[string]string{{"id": "wamid.outreach-1"}}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func outreachClient(t *testing.T, s *Services, tenantID, phone string, marketingConsent bool) string {
	t.Helper()
	ctx := context.Background()
	rr, err := s.Clients.Resolve(ctx, tenantID, phone, "", "Growth Client")
	if err != nil {
		t.Fatal(err)
	}
	c := rr.Client
	c.Consent = map[string]any{"marketing": marketingConsent}
	if err := s.Store.Clients().Upsert(ctx, c); err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func TestOutreachMarketingRequiresConsent(t *testing.T) {
	calls := 0
	s := testServices(t, Deps{WhatsAppURL: waCountingStub(t, &calls).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000001", false)

	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachMarketing,
		Body: "We miss you — 10% off this week.", IdempotencyKey: "m1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != "held" || !strings.Contains(o.Detail, "consent") {
		t.Fatalf("expected held for missing consent, got %+v", o)
	}
	if calls != 0 {
		t.Fatalf("adapter must not be called when consent-gated, calls=%d", calls)
	}
}

func TestOutreachServiceAlwaysAllowed(t *testing.T) {
	calls := 0
	s := testServices(t, Deps{WhatsAppURL: waCountingStub(t, &calls).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000002", false)

	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachService,
		Body: "Your chauffeur arrives in 10 minutes.", IdempotencyKey: "s1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != "sent" || o.ExternalRef != "wamid.outreach-1" {
		t.Fatalf("expected sent with ref, got %+v", o)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	// Attribution spine: outreach_sent lands in the funnel.
	events, err := s.Store.Attribution().List(ctx, tenantID, clientID, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Kind == "outreach_sent" && e.Channel == "whatsapp" && e.Campaign == "service" {
			found = true
		}
	}
	if !found {
		t.Fatalf("outreach_sent attribution missing: %+v", events)
	}
}

func TestOutreachFrequencyCap(t *testing.T) {
	calls := 0
	s := testServices(t, Deps{WhatsAppURL: waCountingStub(t, &calls).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000003", true)

	for i := 0; i < OutreachMarketingWeeklyCap; i++ {
		o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
			ClientID: clientID, Purpose: model.OutreachMarketing,
			Body: "offer", IdempotencyKey: "cap-" + string(rune('a'+i)),
		})
		if err != nil || o.Status != "sent" {
			t.Fatalf("send %d: %+v %v", i, o, err)
		}
	}
	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachMarketing,
		Body: "one too many", IdempotencyKey: "cap-over",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != "held" || !strings.Contains(o.Detail, "frequency cap") {
		t.Fatalf("expected cap hold, got %+v", o)
	}
	if calls != OutreachMarketingWeeklyCap {
		t.Fatalf("adapter calls=%d, want %d", calls, OutreachMarketingWeeklyCap)
	}
	// Service sends still allowed while marketing is capped.
	so, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachService,
		Body: "booking update", IdempotencyKey: "cap-svc",
	})
	if err != nil || so.Status != "sent" {
		t.Fatalf("service send while capped: %+v %v", so, err)
	}
}

func TestOutreachIdempotentReplay(t *testing.T) {
	calls := 0
	s := testServices(t, Deps{WhatsAppURL: waCountingStub(t, &calls).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000004", false)

	in := SendOutreachInput{ClientID: clientID, Body: "hello", IdempotencyKey: "dup-1"}
	first, err := s.Outreach.Send(ctx, tenantID, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Outreach.Send(ctx, tenantID, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("replay returned different record: %s vs %s", first.ID, second.ID)
	}
	if calls != 1 {
		t.Fatalf("replay re-sent: calls=%d", calls)
	}
}

func TestOutreachAdapterHeld(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"mode": "held", "reason": "outside 24h session — approved template required"})
	}))
	t.Cleanup(srv.Close)
	s := testServices(t, Deps{WhatsAppURL: srv.URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000005", false)

	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Body: "freeform outside session", IdempotencyKey: "h1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != "held" || !strings.Contains(o.Detail, "template") {
		t.Fatalf("expected adapter hold, got %+v", o)
	}
}

func TestOutreachNoAdapterHonestDegrade(t *testing.T) {
	s := testServices(t, Deps{}) // no WhatsAppURL
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000006", false)

	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Body: "hello", IdempotencyKey: "nd1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != "failed" || !strings.Contains(o.Detail, "not configured") {
		t.Fatalf("expected honest failed record, got %+v", o)
	}
}

func TestReferralCodeRoundtrip(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000007", false)

	code := model.ClientReferralCode(clientID)
	if !strings.HasPrefix(code, "VIP-") || len(code) != 12 {
		t.Fatalf("bad code %q", code)
	}
	found, err := s.Store.Clients().FindByReferralCode(ctx, tenantID, code)
	if err != nil || found.ID != clientID {
		t.Fatalf("lookup: %+v %v", found, err)
	}
	// Case-insensitive.
	found, err = s.Store.Clients().FindByReferralCode(ctx, tenantID, strings.ToLower(code))
	if err != nil || found.ID != clientID {
		t.Fatalf("lowercase lookup: %+v %v", found, err)
	}
	if _, err := s.Store.Clients().FindByReferralCode(ctx, tenantID, "garbage"); err == nil {
		t.Fatal("malformed code must not resolve")
	}
}

func TestFirstTouchInheritance(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000008", false)

	// First touch: a capture page_view carrying the referral campaign.
	s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "page_view", Channel: "referral",
		Campaign: "ref:VIP-ABCDEF12", ClientID: clientID,
	})
	// A later touch with no campaign must not overwrite first touch.
	s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "conversation_started", Channel: "whatsapp", ClientID: clientID,
	})

	if _, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
		TenantID: tenantID, ClientID: clientID, IdempotencyKey: "ft-1",
		Items: []model.ServiceItem{{
			Vertical: "CHAUFFEUR_POINT_TO_POINT", PickupAddress: "A", DropoffAddress: "B",
			PickupLat: 52.37, PickupLng: 4.90, DropoffLat: 52.31, DropoffLng: 4.76,
			PickupAt: 1733500000, Passengers: 1,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	events, err := s.Store.Attribution().List(ctx, tenantID, clientID, 20)
	if err != nil {
		t.Fatal(err)
	}
	var req *model.AttributionEvent
	for i := range events {
		if events[i].Kind == "request_created" {
			req = &events[i]
		}
	}
	if req == nil {
		t.Fatalf("request_created not tracked: %+v", events)
	}
	if req.Channel != "referral" || req.Campaign != "ref:VIP-ABCDEF12" {
		t.Fatalf("first touch not inherited: %+v", req)
	}
}

func TestReferralConversion(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	referrerID := outreachClient(t, s, tenantID, "+971500000009", false)
	refereeID := outreachClient(t, s, tenantID, "+971500000010", false)
	code := model.ClientReferralCode(referrerID)

	// Auto-quotable catalog so Confirm runs the instant path.
	if _, err := s.Catalog.Add(ctx, &model.CatalogService{
		TenantID: tenantID, Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Sedan|business",
		BaseMinor: 9000, Currency: "EUR", PerKMMinor: 250, AutoQuotable: true, Active: true,
	}); err != nil {
		t.Fatal(err)
	}

	s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "page_view", Channel: "referral",
		Campaign: "ref:" + code, ClientID: refereeID,
	})

	book := func(idem string) {
		t.Helper()
		sr, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
			TenantID: tenantID, ClientID: refereeID, IdempotencyKey: idem,
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
	}
	book("ref-book-1")
	book("ref-book-2")

	events, err := s.Store.Attribution().List(ctx, tenantID, refereeID, 50)
	if err != nil {
		t.Fatal(err)
	}
	conversions := 0
	for _, e := range events {
		if e.Kind == "referral_converted" {
			conversions++
			if e.Channel != "referral" || !strings.HasPrefix(e.Campaign, "ref:VIP-") {
				t.Fatalf("bad conversion attribution: %+v", e)
			}
			if e.ValueMinor <= 0 {
				t.Fatalf("conversion must carry booking value: %+v", e)
			}
		}
	}
	if conversions != 1 {
		t.Fatalf("want exactly one referral conversion across two bookings, got %d", conversions)
	}
}

func TestFunnelAggregation(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000011", false)

	s.Events.Track(ctx, &model.AttributionEvent{TenantID: tenantID, Kind: "page_view", Channel: "seo", Campaign: "launch", ClientID: clientID})
	s.Events.Track(ctx, &model.AttributionEvent{TenantID: tenantID, Kind: "page_view", Channel: "seo", Campaign: "launch", ClientID: clientID})
	s.Events.Track(ctx, &model.AttributionEvent{TenantID: tenantID, Kind: "booking", Channel: "seo", Campaign: "launch", ClientID: clientID, ValueMinor: 42000})

	rows, err := s.Funnel(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	foundView, foundBooking := false, false
	for _, r := range rows {
		if r.Kind == "page_view" && r.Channel == "seo" {
			foundView = true
			if r.Count != 2 {
				t.Fatalf("page_view count=%d, want 2", r.Count)
			}
		}
		if r.Kind == "booking" {
			foundBooking = true
			if r.ValueMinor != 42000 {
				t.Fatalf("booking value=%d, want 42000", r.ValueMinor)
			}
		}
	}
	if !foundView || !foundBooking {
		t.Fatalf("funnel rows missing: %+v", rows)
	}
}
