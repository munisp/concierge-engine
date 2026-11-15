package domain

// Phase 39: email outreach channel — channel selection, per-channel caps,
// the b2b-introduction lane, and the unsubscribe token flow.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// emailStub is a counting email-adapter test double returning an RFC
// message id the way the Phase 39 adapter does.
func emailStub(t *testing.T, calls *int, lastPayload *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		if lastPayload != nil {
			_ = json.NewDecoder(r.Body).Decode(lastPayload)
		}
		json.NewEncoder(w).Encode(map[string]any{"mode": "smtp", "message_id": "<msg-1@mail.taylor.example>"})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// emailClient creates a client carrying both identities, consent as asked.
func emailClient(t *testing.T, s *Services, tenantID string, marketingConsent bool) string {
	t.Helper()
	ctx := context.Background()
	rr, err := s.Clients.Resolve(ctx, tenantID, "+971500000099", "vp.travel@acme.example", "Pat Buyer")
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

func TestEmailChannelValidation(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := emailClient(t, s, tenantID, true)

	// Subject required on the email lane.
	if _, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachService, Channel: "email",
		Body: "trip update", IdempotencyKey: "e-nosubj",
	}); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("email without subject: %v", err)
	}
	// Unknown channel refused.
	if _, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachService, Channel: "fax",
		Body: "x", Subject: "x", IdempotencyKey: "e-fax",
	}); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("unknown channel: %v", err)
	}
	// Email-less client refused on the email lane.
	rr, err := s.Clients.Resolve(ctx, tenantID, "+971500000098", "", "No Mail")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: rr.Client.ID, Purpose: model.OutreachService, Channel: "email",
		Body: "trip update", Subject: "Update", IdempotencyKey: "e-nomail",
	}); err == nil || !strings.Contains(err.Error(), "no email") {
		t.Fatalf("email-less client: %v", err)
	}
	// Honest degrade without the adapter configured.
	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachService, Channel: "email",
		Body: "Your chauffeur is assigned.", Subject: "Chauffeur assigned", IdempotencyKey: "e-svc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Channel != "email" || o.Status != "failed" || !strings.Contains(o.Detail, "CE_EMAIL_ADAPTER_URL") {
		t.Fatalf("degraded email send: %+v", o)
	}
	// Service email carries NO marketing footer.
	if strings.Contains(o.Body, "Unsubscribe") {
		t.Fatalf("service body must not carry the marketing footer: %q", o.Body)
	}
}

func TestEmailMarketingFooterAndDelivery(t *testing.T) {
	calls := 0
	var payload map[string]any
	s := testServices(t, Deps{
		EmailURL:   emailStub(t, &calls, &payload).URL,
		TripSecret: "test-secret",
		PublicBase: "https://core.example",
	})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := emailClient(t, s, tenantID, true)

	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachMarketing, Channel: "email",
		Body: "Race-weekend transfers are open.", Subject: "F1 Portimão transfers",
		IdempotencyKey: "e-mkt-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != "sent" || o.Channel != "email" {
		t.Fatalf("marketing email: %+v", o)
	}
	// The adapter received the one-click URL separately (List-Unsubscribe).
	unsub, _ := payload["unsubscribe_url"].(string)
	if !strings.HasPrefix(unsub, "https://core.example/v1/unsub/") {
		t.Fatalf("unsubscribe_url payload: %v", payload)
	}
	// The RECORDED body carries the mandated footer — provenance shows
	// exactly what left the platform.
	if !strings.Contains(o.Body, "Unsubscribe from these messages: "+unsub) {
		t.Fatalf("recorded body missing footer: %q", o.Body)
	}
	if o.ExternalRef != "<msg-1@mail.taylor.example>" {
		t.Fatalf("external ref: %q", o.ExternalRef)
	}
	if calls != 1 {
		t.Fatalf("adapter calls=%d", calls)
	}
}

func TestEmailPerChannelCap(t *testing.T) {
	waCalls, emCalls := 0, 0
	s := testServices(t, Deps{
		WhatsAppURL: waCountingStub(t, &waCalls).URL,
		EmailURL:    emailStub(t, &emCalls, nil).URL,
		TripSecret:  "test-secret",
	})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := emailClient(t, s, tenantID, true)

	// Drain the WhatsApp budget.
	for i := 0; i < OutreachMarketingWeeklyCap; i++ {
		o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
			ClientID: clientID, Purpose: model.OutreachMarketing,
			Body: "offer", IdempotencyKey: "mix-wa-" + string(rune('a'+i)),
		})
		if err != nil || o.Status != "sent" {
			t.Fatalf("wa send %d: %+v %v", i, o, err)
		}
	}
	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachMarketing,
		Body: "one too many", IdempotencyKey: "mix-wa-over",
	})
	if err != nil || o.Status != "held" {
		t.Fatalf("wa cap: %+v %v", o, err)
	}
	// Email is a separate budget — the WA cap must not touch it.
	for i := 0; i < OutreachMarketingWeeklyCap; i++ {
		o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
			ClientID: clientID, Purpose: model.OutreachMarketing, Channel: "email",
			Body: "offer", Subject: "Offer", IdempotencyKey: "mix-em-" + string(rune('a'+i)),
		})
		if err != nil || o.Status != "sent" {
			t.Fatalf("email send %d: %+v %v", i, o, err)
		}
	}
	o, err = s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachMarketing, Channel: "email",
		Body: "one too many", Subject: "Offer", IdempotencyKey: "mix-em-over",
	})
	if err != nil || o.Status != "held" || !strings.Contains(o.Detail, "frequency cap") {
		t.Fatalf("email cap: %+v %v", o, err)
	}
	if waCalls != 3 || emCalls != 3 {
		t.Fatalf("calls wa=%d email=%d, want 3/3", waCalls, emCalls)
	}
}

func TestB2BIntroductionLane(t *testing.T) {
	emCalls, waCalls := 0, 0
	s := testServices(t, Deps{
		WhatsAppURL: waCountingStub(t, &waCalls).URL,
		EmailURL:    emailStub(t, &emCalls, nil).URL,
		TripSecret:  "test-secret",
	})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := emailClient(t, s, tenantID, false) // no marketing consent

	// The client is reachable only via a bought list: open b2b lead.
	if _, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: model.LeadKindPerson, Name: "Pat Buyer",
		Email: "vp.travel@acme.example", Phone: "+971500000099",
		Source: "import", ListSource: "cognism-q1", ContactBasis: model.ContactBasisB2B,
	}); err != nil {
		t.Fatal(err)
	}

	v, err := s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, "email")
	if err != nil || !v.Allowed || !v.IntroLane || v.IntroMax != EmailIntroMaxTouches {
		t.Fatalf("intro verdict: %+v %v", v, err)
	}
	// WhatsApp stays closed — the exception is email-only by design.
	vWA, err := s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, "whatsapp")
	if err != nil || vWA.Allowed {
		t.Fatalf("wa verdict must stay gated: %+v %v", vWA, err)
	}

	for i := 1; i <= EmailIntroMaxTouches; i++ {
		o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
			ClientID: clientID, Purpose: model.OutreachMarketing, Channel: "email",
			Body: "Introduction", Subject: "Taylor × Acme travel desk",
			IdempotencyKey: "intro-" + string(rune('0'+i)),
		})
		if err != nil || o.Status != "sent" {
			t.Fatalf("intro touch %d: %+v %v", i, o, err)
		}
		if !strings.Contains(o.Detail, "b2b introduction touch") {
			t.Fatalf("touch %d detail: %q", i, o.Detail)
		}
		if !strings.Contains(o.Body, "Unsubscribe from these messages:") {
			t.Fatalf("touch %d missing footer: %q", i, o.Body)
		}
	}
	// Touch 3: the lane is closed until consent is earned.
	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachMarketing, Channel: "email",
		Body: "Third", Subject: "Third", IdempotencyKey: "intro-3",
	})
	if err != nil || o.Status != "held" || !strings.Contains(o.Detail, "marketing consent not granted") {
		t.Fatalf("lane must close after %d touches: %+v %v", EmailIntroMaxTouches, o, err)
	}
	if emCalls != EmailIntroMaxTouches {
		t.Fatalf("email adapter calls=%d, want %d", emCalls, EmailIntroMaxTouches)
	}

	// Earned consent reopens the normal lane (Phase 38 repeat-touch grant).
	if _, err := s.Clients.SetConsent(ctx, tenantID, clientID, "marketing", true); err != nil {
		t.Fatal(err)
	}
	o, err = s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachMarketing, Channel: "email",
		Body: "Welcome aboard", Subject: "Welcome", IdempotencyKey: "intro-4",
	})
	if err != nil || o.Status != "sent" {
		t.Fatalf("post-consent email: %+v %v", o, err)
	}

	// A self-reported lead without consent must NOT open the lane.
	otherID := outreachClient(t, s, tenantID, "+971500000097", false)
	if _, err := s.Leads.Capture(ctx, tenantID, CaptureLeadInput{
		Kind: model.LeadKindPerson, Name: "Organic", Phone: "+971500000097",
		Source: "site",
	}); err != nil {
		t.Fatal(err)
	}
	v2, err := s.Outreach.Verdict(ctx, tenantID, otherID, model.OutreachMarketing, "email")
	if err != nil || v2.Allowed || v2.IntroLane {
		t.Fatalf("self-reported lead must not open the intro lane: %+v %v", v2, err)
	}
}

func TestUnsubscribeTokenFlow(t *testing.T) {
	s := testServices(t, Deps{TripSecret: "test-secret", PublicBase: "https://core.example"})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := emailClient(t, s, tenantID, true)

	tok := s.Trips.UnsubTokenFor(tenantID, clientID)
	res, err := s.Outreach.UnsubscribeByToken(ctx, tok)
	if err != nil || res.Already {
		t.Fatalf("unsubscribe: %+v %v", res, err)
	}
	cl, err := s.Store.Clients().Get(ctx, tenantID, clientID)
	if err != nil {
		t.Fatal(err)
	}
	if cl.Consent["marketing"] == true {
		t.Fatal("consent must be off after unsubscribe")
	}
	// Replay is idempotent.
	res, err = s.Outreach.UnsubscribeByToken(ctx, tok)
	if err != nil || !res.Already {
		t.Fatalf("replay: %+v %v", res, err)
	}
	// Marketing is gated now, service still allowed.
	v, _ := s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, "email")
	if v.Allowed {
		t.Fatal("marketing must be held after unsubscribe")
	}
	v, _ = s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachService, "email")
	if !v.Allowed {
		t.Fatal("service lane must survive unsubscribe")
	}
	// Tampered and cross-family tokens rejected.
	if _, err := s.Outreach.UnsubscribeByToken(ctx, tok[:len(tok)-2]+"xx"); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("tampered token: %v", err)
	}
	trackTok := s.Trips.UnsubTokenFor(tenantID, clientID) // same family sanity
	if _, _, err := s.Trips.ParseUnsubToken(trackTok); err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	quoteTok := s.Trips.QuoteTokenFor(tenantID, "q1")
	if _, _, err := s.Trips.ParseUnsubToken(quoteTok); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("quote token must not unsubscribe: %v", err)
	}
}

func TestBroadcastEmailChannel(t *testing.T) {
	emCalls := 0
	s := testServices(t, Deps{EmailURL: emailStub(t, &emCalls, nil).URL, TripSecret: "s"})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	emailClient(t, s, tenantID, true)
	emailClient2 := func() string {
		rr, err := s.Clients.Resolve(ctx, tenantID, "+971500000096", "ea@acme.example", "EA Buyer")
		if err != nil {
			t.Fatal(err)
		}
		c := rr.Client
		c.Consent = map[string]any{"marketing": true}
		if err := s.Store.Clients().Upsert(ctx, c); err != nil {
			t.Fatal(err)
		}
		return c.ID
	}()
	_ = emailClient2

	res, err := s.Campaigns.Broadcast(ctx, tenantID, BroadcastInput{
		Campaign: "f1-portimao", Body: "Transfers open.", Subject: "F1 transfers",
		Channel: "email", Segment: model.SegmentRules{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 2 || res.Matched != 2 {
		t.Fatalf("email broadcast: %+v", res)
	}
	sends, err := s.Outreach.Sends(ctx, tenantID, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range sends {
		if o.Channel != "email" {
			t.Fatalf("broadcast send channel: %+v", o)
		}
	}
	// Same campaign on WhatsApp is a NEW send (channel-suffixed key), not a replay.
	resWA, err := s.Campaigns.Broadcast(ctx, tenantID, BroadcastInput{
		Campaign: "f1-portimao", Body: "Transfers open.", Segment: model.SegmentRules{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resWA.Matched != 2 || resWA.Sent+resWA.Failed != 2 {
		t.Fatalf("wa broadcast after email: %+v", resWA)
	}
	sends, _ = s.Outreach.Sends(ctx, tenantID, 50)
	if len(sends) != 4 {
		t.Fatalf("want 4 sends (2 email + 2 wa), got %d", len(sends))
	}
	// Re-running the email broadcast replays — still 4.
	if _, err := s.Campaigns.Broadcast(ctx, tenantID, BroadcastInput{
		Campaign: "f1-portimao", Body: "Transfers open.", Subject: "F1 transfers",
		Channel: "email", Segment: model.SegmentRules{},
	}); err != nil {
		t.Fatal(err)
	}
	sends, _ = s.Outreach.Sends(ctx, tenantID, 50)
	if len(sends) != 4 {
		t.Fatalf("replay must not duplicate: %d sends", len(sends))
	}
}
