package domain

// Phase 41: email list hygiene — hard-bounce suppression holds every email
// lane to a dead address; engagement sunsetting holds never-engaged clients
// out of the marketing lane before they become complaints.

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

func TestEmailSuppression41(t *testing.T) {
	calls := 0
	s := testServices(t, Deps{
		EmailURL:   emailStub(t, &calls, nil).URL,
		TripSecret: "s",
		PublicBase: "https://core.example",
	})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := emailClient(t, s, tenantID, true) // vp.travel@acme.example

	// Baseline: marketing email allowed.
	v, err := s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, "email")
	if err != nil || !v.Allowed {
		t.Fatalf("baseline verdict: %+v %v", v, err)
	}

	// Suppress (mixed-case input normalizes).
	row, created, err := s.Outreach.Suppress(ctx, tenantID, SuppressInput{
		Email: "VP.Travel@acme.example ", Reason: model.SuppressHardBounce, SourceRef: "<msg-9@mail.taylor.example>",
	})
	if err != nil || !created {
		t.Fatalf("suppress: %+v created=%v %v", row, created, err)
	}
	if row.Email != "vp.travel@acme.example" {
		t.Fatalf("email not normalized: %q", row.Email)
	}

	// Replay returns the original row, created=false.
	replay, created2, err := s.Outreach.Suppress(ctx, tenantID, SuppressInput{
		Email: "vp.travel@acme.example", Reason: model.SuppressHardBounce,
	})
	if err != nil || created2 || replay.ID != row.ID {
		t.Fatalf("replay: %+v created=%v %v", replay, created2, err)
	}

	// Both email lanes hold; WhatsApp is untouched.
	v, _ = s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, "email")
	if v.Allowed || !v.EmailSuppressed || !strings.Contains(v.Reason, "suppressed") {
		t.Fatalf("marketing email should be held suppressed: %+v", v)
	}
	v, _ = s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachService, "email")
	if v.Allowed || !v.EmailSuppressed {
		t.Fatalf("service email should be held suppressed: %+v", v)
	}
	v, _ = s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, model.OutreachChannelWhatsApp)
	if !v.Allowed {
		t.Fatalf("whatsapp unaffected: %+v", v)
	}

	// A send attempt is held and the adapter is never called.
	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachMarketing, Channel: "email",
		Body: "offer", Subject: "Offer", IdempotencyKey: "supp-1",
	})
	if err != nil || o.Status != "held" || calls != 0 {
		t.Fatalf("suppressed send: %+v calls=%d %v", o, calls, err)
	}

	// List shows the row; operator recovery reopens the lane.
	rows, err := s.Outreach.Suppressions(ctx, tenantID, 100)
	if err != nil || len(rows) != 1 {
		t.Fatalf("list: %+v %v", rows, err)
	}
	if err := s.Outreach.Unsuppress(ctx, tenantID, "VP.Travel@acme.example"); err != nil {
		t.Fatalf("unsuppress: %v", err)
	}
	v, _ = s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, "email")
	if !v.Allowed {
		t.Fatalf("unsuppressed verdict should allow: %+v", v)
	}
	if err := s.Outreach.Unsuppress(ctx, tenantID, "vp.travel@acme.example"); !errors.Is(err, httpx.ErrNotFound) {
		t.Fatalf("second unsuppress should 404: %v", err)
	}

	// Validation.
	if _, _, err := s.Outreach.Suppress(ctx, tenantID, SuppressInput{Email: "not-an-email", Reason: model.SuppressManual}); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("bad email: %v", err)
	}
	if _, _, err := s.Outreach.Suppress(ctx, tenantID, SuppressInput{Email: "a@b.c", Reason: "soft_bounce"}); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("bad reason: %v", err)
	}
}

func TestEmailSunset41(t *testing.T) {
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

	// Service email stays exempt throughout.
	if v, _ := s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachService, "email"); !v.Allowed {
		t.Fatalf("service email must never sunset: %+v", v)
	}

	// 8 all-time marketing sends across three weekly windows (cap = 3/7d).
	for i := 0; i < EmailSunsetMinSends; i++ {
		if i > 0 && i%OutreachMarketingWeeklyCap == 0 {
			now = now.Add(8 * 24 * time.Hour)
		}
		o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
			ClientID: clientID, Purpose: model.OutreachMarketing, Channel: "email",
			Body: "offer", Subject: "Offer", IdempotencyKey: fmt.Sprintf("sun-%d", i),
		})
		if err != nil || o.Status != "sent" {
			t.Fatalf("send %d: %+v %v", i, o, err)
		}
	}

	// The 9th is held by the sunset rule.
	v, err := s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, "email")
	if err != nil || v.Allowed || !strings.Contains(v.Reason, "sunset") {
		t.Fatalf("9th send should sunset: %+v %v", v, err)
	}
	// WhatsApp verdict is untouched (zero WA sends).
	if v, _ := s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, model.OutreachChannelWhatsApp); !v.Allowed {
		t.Fatalf("whatsapp unaffected by email sunset: %+v", v)
	}

	// A single recorded open resets the sunset permanently.
	s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "email_opened", Channel: "email", ClientID: clientID,
	})
	v, _ = s.Outreach.Verdict(ctx, tenantID, clientID, model.OutreachMarketing, "email")
	if !v.Allowed {
		t.Fatalf("one open resets the sunset: %+v", v)
	}
}
