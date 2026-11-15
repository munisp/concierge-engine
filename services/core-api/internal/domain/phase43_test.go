package domain

// Phase 43: post-sunset re-engagement — the win-back touch (one per 90 days
// through a sunset hold while consent is on) and the reopt/ re-subscribe
// token family riding service-lane email footers.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// TestReengagementEmailCooldown43: the exemption grants one touch, the
// cooldown then holds, and a 91-day-old touch stops counting.
func TestReengagementEmailCooldown43(t *testing.T) {
	now := time.Now() // anchored to real time: the memory store stamps real time
	calls := 0
	s := testServices(t, Deps{
		Now:        func() time.Time { return now },
		EmailURL:   emailStub(t, &calls, nil).URL,
		TripSecret: "s",
		PublicBase: "https://core.example",
	})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := emailClient(t, s, tenantID, true)

	// 8 all-time marketing sends across three weekly windows (cap = 3/7d).
	for i := 0; i < EmailSunsetMinSends; i++ {
		if i > 0 && i%OutreachMarketingWeeklyCap == 0 {
			now = now.Add(8 * 24 * time.Hour)
		}
		o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
			ClientID: clientID, Purpose: model.OutreachMarketing, Channel: "email",
			Body: "offer", Subject: "Offer", IdempotencyKey: fmt.Sprintf("re43-%d", i),
		})
		if err != nil || o.Status != "sent" {
			t.Fatalf("send %d: %+v %v", i, o, err)
		}
	}

	// The sunset exemption grants the win-back touch.
	v, err := s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, "email")
	if err != nil || !v.Allowed || !v.ReengagementTouch {
		t.Fatalf("exemption verdict: %+v %v", v, err)
	}
	touch, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachMarketing, Channel: "email",
		Body: "offer", Subject: "Offer", IdempotencyKey: "re43-touch",
	})
	if err != nil || touch.Status != "sent" || !strings.HasPrefix(touch.Detail, "re-engagement touch") {
		t.Fatalf("touch send: %+v %v", touch, err)
	}

	// Cooldown in force: the next send is held even with the cap clear.
	now = now.Add(8 * 24 * time.Hour)
	v, err = s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, "email")
	if err != nil || v.Allowed || !strings.Contains(v.Reason, "sunset") ||
		!strings.Contains(v.Reason, "re-engagement") {
		t.Fatalf("cooldown should hold: %+v %v", v, err)
	}

	// A failed touch does not burn the window: age the sent touch out, then
	// confirm a fresh touch is eligible again (sunset still applies — the
	// client never engaged).
	now = now.Add(ReengagementCooldownDays * 24 * time.Hour)
	v, err = s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, "email")
	if err != nil || !v.Allowed || !v.ReengagementTouch {
		t.Fatalf("touch should be eligible again after %dd: %+v %v", ReengagementCooldownDays, v, err)
	}
}

// TestReengagementUnreachableNotExempt43: a dead WA number is never
// win-back material — the unreachable hold has no exemption.
func TestReengagementUnreachableNotExempt43(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000044", true)

	// Sunset-level send history AND a dead number (seeded directly — the
	// send cadence is covered in the cooldown test).
	for i := 0; i < WaSunsetMinSends; i++ {
		if err := s.Store.Outreach().AddSend(ctx, &model.OutreachSend{
			ID: fmt.Sprintf("u43-%d", i), TenantID: tenantID, ClientID: clientID,
			Channel: "whatsapp", Purpose: "marketing", Status: "sent",
			Body: "offer", IdempotencyKey: fmt.Sprintf("u43-%d", i),
		}); err != nil {
			t.Fatalf("seed send %d: %v", i, err)
		}
	}
	for i := 0; i < WaUnreachableMinFailures; i++ {
		s.Events.Track(ctx, &model.AttributionEvent{
			TenantID: tenantID, Kind: "wa_failed", Channel: "whatsapp", ClientID: clientID,
		})
	}

	v, err := s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, "whatsapp")
	if err != nil || v.Allowed || !v.WaUnreachable || v.ReengagementTouch {
		t.Fatalf("unreachable must hold without exemption: %+v %v", v, err)
	}
}

// TestReopt43: the reopt/ token family — mint, parse, consent flip,
// attribution, idempotent replay, and cross-family rejection.
func TestReopt43(t *testing.T) {
	s := testServices(t, Deps{TripSecret: "s", PublicBase: "https://core.example"})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := emailClient(t, s, tenantID, false) // marketing consent off

	// Roundtrip.
	tok := s.Trips.ReoptTokenFor(tenantID, clientID)
	gotTenant, gotClient, err := s.Trips.ParseReoptToken(tok)
	if err != nil || gotTenant != tenantID || gotClient != clientID {
		t.Fatalf("roundtrip: %q %q %v", gotTenant, gotClient, err)
	}

	// The click flips consent and records the opt-in on the spine.
	res, err := s.Outreach.ReoptInByToken(ctx, tok)
	if err != nil || res.Already || res.TenantName == "" {
		t.Fatalf("reopt: %+v %v", res, err)
	}
	cl, err := s.Store.Clients().Get(ctx, tenantID, clientID)
	if err != nil || cl.Consent["marketing"] != true {
		t.Fatalf("consent should be on: %+v %v", cl, err)
	}
	events, err := s.Store.Attribution().List(ctx, tenantID, clientID, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if ev.Kind == "reengagement_optin" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reengagement_optin attribution missing: %+v", events)
	}

	// Replay reports already-subscribed, no duplicate flip.
	res, err = s.Outreach.ReoptInByToken(ctx, tok)
	if err != nil || !res.Already {
		t.Fatalf("replay: %+v %v", res, err)
	}

	// Cross-family and garbage tokens are refused.
	if _, err := s.Outreach.ReoptInByToken(ctx, s.Trips.UnsubTokenFor(tenantID, clientID)); !errors.Is(err, httpx.ErrForbidden) {
		t.Fatalf("unsub token must not reopt: %v", err)
	}
	if _, err := s.Outreach.ReoptInByToken(ctx, "not-a-token"); err == nil {
		t.Fatal("garbage token must error")
	}

	// Consent on = marketing verdict opens (fresh client, no caps/sunset).
	v, err := s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, "email")
	if err != nil || !v.Allowed || v.ReengagementTouch {
		t.Fatalf("post-reopt verdict: %+v %v", v, err)
	}
}

// TestReoptFooter43: service-lane emails to opted-out clients carry the
// re-subscribe link; marketing emails carry unsubscribe (never reopt);
// service email to a consented client carries neither.
func TestReoptFooter43(t *testing.T) {
	calls := 0
	s := testServices(t, Deps{
		EmailURL:   emailStub(t, &calls, nil).URL,
		TripSecret: "s",
		PublicBase: "https://core.example",
	})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	optedOut := emailClient(t, s, tenantID, false)
	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: optedOut, Purpose: model.OutreachService, Channel: "email",
		Subject: "Booking confirmed", Body: "Your airport transfer is confirmed for 09:00.",
		IdempotencyKey: "svc-reopt",
	})
	if err != nil || o.Status != "sent" {
		t.Fatalf("service send: %+v %v", o, err)
	}
	if !strings.Contains(o.Body, "/v1/reopt/") || strings.Contains(o.Body, "/v1/unsub/") {
		t.Fatalf("service email to opted-out client must carry reopt only: %q", o.Body)
	}
	// The embedded token really parses back to this client.
	idx := strings.Index(o.Body, "/v1/reopt/")
	tok := strings.TrimRight(o.Body[idx+len("/v1/reopt/"):], "\n")
	gt, gc, err := s.Trips.ParseReoptToken(tok)
	if err != nil || gt != tenantID || gc != optedOut {
		t.Fatalf("footer token: %q %q %v", gt, gc, err)
	}

	consented := emailClientAt(t, s, tenantID, 7, true)
	m, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: consented, Purpose: model.OutreachMarketing, Channel: "email",
		Subject: "Offer", Body: "Weekend offer.", IdempotencyKey: "mkt-reopt",
	})
	if err != nil || m.Status != "sent" {
		t.Fatalf("marketing send: %+v %v", m, err)
	}
	if !strings.Contains(m.Body, "/v1/unsub/") || strings.Contains(m.Body, "/v1/reopt/") {
		t.Fatalf("marketing email must carry unsub only: %q", m.Body)
	}

	svc2, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: consented, Purpose: model.OutreachService, Channel: "email",
		Subject: "Booking confirmed", Body: "Your driver is outside.",
		IdempotencyKey: "svc-consented",
	})
	if err != nil || svc2.Status != "sent" {
		t.Fatalf("service send 2: %+v %v", svc2, err)
	}
	if strings.Contains(svc2.Body, "/v1/reopt/") || strings.Contains(svc2.Body, "/v1/unsub/") {
		t.Fatalf("consented service email carries no footer: %q", svc2.Body)
	}
}
