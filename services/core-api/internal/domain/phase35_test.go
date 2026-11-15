// Phase 35 domain tests: customer acquisition & outreach innovations —
// quiet hours + send-time optimization + scheduled-send sweep (O6/O7),
// drip sequences (O3), segments + broadcasts + A/B + funnel (O1/O2/O4/O15),
// STOP/START keywords (O5), lifecycle rules (O8-O11, O13, O14), journey
// notifications (O12), first-booking credit (A8), referral leaderboard
// (A5), social proof (A10), quote share tokens (A4).
package domain

import (
	"context"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// setOutreachConfig applies a tenant outreach config for a test.
func setOutreachConfig(t *testing.T, s *Services, tenantID string, cfg model.OutreachConfig) {
	t.Helper()
	ctx := context.Background()
	tenant, err := s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	tenant.OutreachConfig = cfg
	if err := s.Store.Tenants().UpdateConfig(ctx, tenant); err != nil {
		t.Fatal(err)
	}
}

func setAcquisitionConfig(t *testing.T, s *Services, tenantID string, cfg model.AcquisitionConfig) {
	t.Helper()
	ctx := context.Background()
	tenant, err := s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	tenant.AcquisitionConfig = cfg
	if err := s.Store.Tenants().UpdateConfig(ctx, tenant); err != nil {
		t.Fatal(err)
	}
}

// ------------------------------------------------------------- O7: quiet hours

func TestQuietDeferWrapsMidnight(t *testing.T) {
	cfg := model.OutreachConfig{QuietStartHour: 21, QuietEndHour: 8}
	// 23:00 is inside a 21→8 window: defers to 08:00 next day.
	inside := time.Date(2026, 3, 10, 23, 0, 0, 0, time.UTC)
	got := quietDefer(cfg, inside)
	want := time.Date(2026, 3, 11, 8, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("23:00 should defer to %v, got %v", want, got)
	}
	// 05:00 is inside the wrap: defers to 08:00 same day.
	early := time.Date(2026, 3, 10, 5, 0, 0, 0, time.UTC)
	if got := quietDefer(cfg, early); !got.Equal(time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("05:00 should defer to same-day 08:00, got %v", got)
	}
	// 12:00 is outside: no deferral.
	if got := quietDefer(cfg, time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)); !got.IsZero() {
		t.Fatalf("12:00 outside quiet hours should not defer, got %v", got)
	}
	// Disabled (both zero) and malformed (equal) configs never defer.
	if got := quietDefer(model.OutreachConfig{}, inside); !got.IsZero() {
		t.Fatal("zero config must not defer")
	}
	if got := quietDefer(model.OutreachConfig{QuietStartHour: 9, QuietEndHour: 9}, inside); !got.IsZero() {
		t.Fatal("equal hours must not defer")
	}
}

func TestQuietHoursDeferMarketingSend(t *testing.T) {
	// Pin the clock inside the quiet window.
	now := time.Date(2026, 3, 10, 22, 30, 0, 0, time.UTC)
	s := testServices(t, Deps{Now: func() time.Time { return now }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	setOutreachConfig(t, s, tenantID, model.OutreachConfig{QuietStartHour: 21, QuietEndHour: 8})
	cid := outreachClient(t, s, tenantID, "+971500000100", true)

	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: cid, Purpose: model.OutreachMarketing, Body: "quiet test",
		IdempotencyKey: "q-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != "scheduled" || o.ScheduledFor == nil {
		t.Fatalf("marketing send in quiet hours should schedule, got %s", o.Status)
	}
	if o.ScheduledFor.Hour() != 8 {
		t.Fatalf("should defer to 08:00, got %v", o.ScheduledFor)
	}
	// Service sends are never deferred — safety beats quiet hours.
	o2, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: cid, Purpose: model.OutreachService, Body: "your trip update",
		IdempotencyKey: "q-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o2.Status == "scheduled" {
		t.Fatal("service send must not defer to quiet hours")
	}
}

// ------------------------------------------------------------- O6: sweep

func TestRunDueDeliversScheduledSends(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	clock := now
	s := testServices(t, Deps{Now: func() time.Time { return clock }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	cid := outreachClient(t, s, tenantID, "+971500000101", true)

	future := now.Add(2 * time.Hour)
	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: cid, Purpose: model.OutreachMarketing, Body: "later",
		IdempotencyKey: "sched-1", ScheduledFor: &future,
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != "scheduled" {
		t.Fatalf("future send should be scheduled, got %s", o.Status)
	}
	// Not due yet.
	if n, err := s.Outreach.RunDue(ctx, tenantID); err != nil || n != 0 {
		t.Fatalf("nothing due yet: n=%d err=%v", n, err)
	}
	// Advance past the schedule: the sweep delivers (no adapter → failed,
	// but terminal — never stuck scheduled).
	clock = now.Add(3 * time.Hour)
	n, err := s.Outreach.RunDue(ctx, tenantID)
	if err != nil || n != 1 {
		t.Fatalf("expected 1 due send, n=%d err=%v", n, err)
	}
	sends, _ := s.Store.Outreach().ListSends(ctx, tenantID, 10)
	if sends[0].Status == "scheduled" {
		t.Fatal("due send must reach a terminal status after the sweep")
	}
}

func TestRunDueRevalidatesConsent(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	clock := now
	s := testServices(t, Deps{Now: func() time.Time { return clock }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	cid := outreachClient(t, s, tenantID, "+971500000102", true)
	future := now.Add(time.Hour)
	if _, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: cid, Purpose: model.OutreachMarketing, Body: "later",
		IdempotencyKey: "sched-2", ScheduledFor: &future,
	}); err != nil {
		t.Fatal(err)
	}
	// Consent withdrawn between schedule and delivery: held, not sent.
	c, _ := s.Store.Clients().Get(ctx, tenantID, cid)
	c.Consent["marketing"] = false
	if err := s.Store.Clients().Upsert(ctx, c); err != nil {
		t.Fatal(err)
	}
	clock = now.Add(2 * time.Hour)
	if _, err := s.Outreach.RunDue(ctx, tenantID); err != nil {
		t.Fatal(err)
	}
	sends, _ := s.Store.Outreach().ListSends(ctx, tenantID, 10)
	if sends[0].Status != "held" {
		t.Fatalf("consent withdrawn pre-delivery must hold, got %s", sends[0].Status)
	}
}

// ------------------------------------------------------------- O3: sequences

func TestSequenceProgression(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	s := testServices(t, Deps{Now: func() time.Time { return clock }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	setOutreachConfig(t, s, tenantID, model.OutreachConfig{
		Sequences: []model.Sequence{{
			Name: "welcome",
			Steps: []model.SequenceStep{
				{AfterHours: 1, Body: "Welcome {{client_name}} — {{tenant_name}} at your service."},
				{AfterHours: 24, Body: "Anything we can arrange this week?"},
			},
		}},
	})
	cid := outreachClient(t, s, tenantID, "+971500000103", true)

	// Step 0 not due yet (client created now, fires +1h).
	res, err := s.Sequences.Run(ctx, tenantID)
	if err != nil || res.Sent+res.Held+res.Failed != 0 {
		t.Fatalf("step 0 should not be due: %+v err=%v", res, err)
	}
	clock = now.Add(2 * time.Hour)
	res, err = s.Sequences.Run(ctx, tenantID)
	if err != nil || res.Sent+res.Failed == 0 {
		t.Fatalf("step 0 should fire: %+v err=%v", res, err)
	}
	// Step 1 requires step 0 AND its own delay — one new step per sweep.
	sends, _ := s.Store.Outreach().SendsForClient(ctx, tenantID, cid, now.Add(-time.Hour))
	if len(sends) != 1 {
		t.Fatalf("exactly one sequence send expected, got %d", len(sends))
	}
	if !strings.Contains(sends[0].Body, "Welcome") || !strings.Contains(sends[0].Body, "Growth Client") {
		t.Fatalf("body must render client/tenant names: %q", sends[0].Body)
	}
	// Replay-safe: a second run at the same clock sends nothing new.
	res, _ = s.Sequences.Run(ctx, tenantID)
	if res.Sent+res.Held+res.Failed != 0 {
		t.Fatalf("idempotent replay must send nothing: %+v", res)
	}
	// Step 1 due at enrollment +1h +24h.
	clock = now.Add(26 * time.Hour)
	res, _ = s.Sequences.Run(ctx, tenantID)
	if res.Sent+res.Failed == 0 {
		t.Fatalf("step 1 should fire: %+v", res)
	}
}

func TestSequenceHeldWithoutConsent(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	s := testServices(t, Deps{Now: func() time.Time { return clock }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	setOutreachConfig(t, s, tenantID, model.OutreachConfig{
		Sequences: []model.Sequence{{
			Name:  "welcome",
			Steps: []model.SequenceStep{{AfterHours: 1, Body: "hello"}},
		}},
	})
	outreachClient(t, s, tenantID, "+971500000104", false) // no marketing consent
	clock = now.Add(2 * time.Hour)                         // step 0 due
	res, err := s.Sequences.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Held != 1 || res.Sent != 0 {
		t.Fatalf("no consent = held, never sent: %+v", res)
	}
}

// ------------------------------------------------------------- O2: segments

func TestSegmentMatching(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	active := outreachClient(t, s, tenantID, "+971500000110", true)
	dormant := outreachClient(t, s, tenantID, "+971500000111", true)
	noConsent := outreachClient(t, s, tenantID, "+971500000112", false)
	book := rewardSetup(t, s, tenantID)
	book(active, "seg-b1")

	// marketing_consent=true filters the non-consented client out.
	got, err := s.SegmentClients(ctx, tenantID, model.SegmentRules{
		MarketingConsent: boolPtr(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, c := range got {
		ids[c.ID] = true
	}
	if !ids[active] || !ids[dormant] || ids[noConsent] {
		t.Fatalf("consent segment wrong: %v", ids)
	}
	// min_bookings: only the active client qualifies.
	got, _ = s.SegmentClients(ctx, tenantID, model.SegmentRules{MinBookings: 1})
	if len(got) != 1 || got[0].ID != active {
		t.Fatalf("min_bookings=1 should match only the booked client, got %d", len(got))
	}
	// dormant: never-booked clients qualify under no_booking_since_days.
	got, _ = s.SegmentClients(ctx, tenantID, model.SegmentRules{NoBookingSinceDays: 30})
	for _, c := range got {
		if c.ID == active {
			t.Fatal("client who booked today is not dormant")
		}
	}
}

func boolPtr(b bool) *bool { return &b }

// ------------------------------------------------------------- O1/O4/O15: campaigns

func TestBroadcastIdempotentAndGated(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	outreachClient(t, s, tenantID, "+971500000120", true)
	outreachClient(t, s, tenantID, "+971500000121", true)
	outreachClient(t, s, tenantID, "+971500000122", false) // held

	res, err := s.Campaigns.Broadcast(ctx, tenantID, BroadcastInput{
		Campaign: "spring", Body: "spring offer", Segment: model.SegmentRules{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Matched != 3 || res.Held != 1 || res.Sent+res.Failed != 2 {
		t.Fatalf("broadcast: %+v", res)
	}
	// Re-run: every client already has a ledger record — nothing new.
	res2, err := s.Campaigns.Broadcast(ctx, tenantID, BroadcastInput{
		Campaign: "spring", Body: "spring offer", Segment: model.SegmentRules{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Matched != 3 {
		t.Fatalf("re-run matches the same audience: %+v", res2)
	}
	sends, _ := s.Store.Outreach().ListSends(ctx, tenantID, 50)
	if len(sends) != 3 {
		t.Fatalf("idempotent re-run must not duplicate sends, got %d", len(sends))
	}
	// O15: the funnel reads the same ledger.
	f, err := s.Campaigns.Funnel(ctx, tenantID, "spring")
	if err != nil {
		t.Fatal(err)
	}
	if f.Held != 1 || f.Sent+f.Failed != 2 {
		t.Fatalf("funnel: %+v", f)
	}
}

func TestBroadcastABSplit(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	for i := 0; i < 10; i++ {
		outreachClient(t, s, tenantID, "+97150000013"+string(rune('0'+i)), true)
	}
	// Template pair: "offer" + "offer#b".
	if err := s.Store.Templates().Upsert(ctx, &model.MessageTemplate{
		ID: "tpl-a", TenantID: tenantID, Name: "offer", Purpose: "marketing", Body: "A body",
	}); err != nil {
		t.Fatal(err)
	}
	var aTpl *model.MessageTemplate
	if list, err := s.Store.Templates().List(ctx, tenantID); err == nil {
		for i := range list {
			if list[i].Name == "offer" {
				aTpl = &list[i]
			}
		}
	}
	if aTpl == nil {
		t.Fatal("template A missing")
	}
	if err := s.Store.Templates().Upsert(ctx, &model.MessageTemplate{
		ID: "tpl-b", TenantID: tenantID, Name: "offer#b", Purpose: "marketing", Body: "B body",
	}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Campaigns.Broadcast(ctx, tenantID, BroadcastInput{
		Campaign: "abtest", TemplateID: aTpl.ID, ABTest: true, Segment: model.SegmentRules{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.VariantA+res.VariantB != 10 || res.VariantA == 0 || res.VariantB == 0 {
		t.Fatalf("10 clients split across both arms: %+v", res)
	}
	// Deterministic: a re-run assigns the same arms (idempotency holds).
	sends, _ := s.Store.Outreach().ListSends(ctx, tenantID, 50)
	if len(sends) != 10 {
		t.Fatalf("expected 10 sends, got %d", len(sends))
	}
	f, _ := s.Campaigns.Funnel(ctx, tenantID, "abtest")
	if len(f.Variants) != 2 {
		t.Fatalf("funnel must split variants: %+v", f.Variants)
	}
}

// ------------------------------------------------------------- O5: keywords

func TestStopKeywordOptsOut(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	cid := outreachClient(t, s, tenantID, "+971500000130", true)

	res, err := s.Cases.Intake(ctx, tenantID, cid, "STOP", "conv-1", "idem-stop-1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Opened {
		t.Fatal("STOP must never open a case")
	}
	c, _ := s.Store.Clients().Get(ctx, tenantID, cid)
	if c.Consent["marketing"] == true {
		t.Fatal("STOP must clear marketing consent")
	}
	// Marketing is now held...
	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: cid, Purpose: model.OutreachMarketing, Body: "offer",
		IdempotencyKey: "post-stop-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != "held" {
		t.Fatalf("post-STOP marketing must hold, got %s", o.Status)
	}
	// ...and START re-enables.
	if _, err := s.Cases.Intake(ctx, tenantID, cid, "start", "conv-1", "idem-start-1"); err != nil {
		t.Fatal(err)
	}
	c, _ = s.Store.Clients().Get(ctx, tenantID, cid)
	if c.Consent["marketing"] != true {
		t.Fatal("START must restore marketing consent")
	}
	// "stop by the office" is NOT an opt-out — exact match only.
	res, err = s.Cases.Intake(ctx, tenantID, cid, "stop by the office at 5", "conv-1", "idem-stop-2")
	if err != nil {
		t.Fatal(err)
	}
	c, _ = s.Store.Clients().Get(ctx, tenantID, cid)
	if c.Consent["marketing"] != true {
		t.Fatal("non-exact 'stop' phrase must not opt out")
	}
	_ = res
}

// ------------------------------------------------------------- O8/O11/O13/O14: lifecycle

func TestQuoteAbandonmentNudge(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	s := testServices(t, Deps{Now: func() time.Time { return clock }, PublicBase: "https://api.example.com", WhatsAppURL: waStub(t).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	// Give quotes a 48h TTL so the +25h nudge lands while the quote is valid.
	tenant, err := s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	tenant.PricingRules.QuoteTTLSeconds = 48 * 3600
	if err := s.Store.Tenants().UpdateConfig(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	cid := outreachClient(t, s, tenantID, "+971500000140", false)
	// Catalog + request + instant quote, left unconfirmed.
	if _, err := s.Catalog.Add(ctx, &model.CatalogService{
		TenantID: tenantID, Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Sedan|business",
		BaseMinor: 9000, Currency: "EUR", PerKMMinor: 250, AutoQuotable: true, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	sr, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
		TenantID: tenantID, ClientID: cid, IdempotencyKey: "qn-req-1",
		Items: []model.ServiceItem{{
			Vertical: "CHAUFFEUR_POINT_TO_POINT", PickupAddress: "A", DropoffAddress: "B",
			PickupLat: 52.37, PickupLng: 4.90, DropoffLat: 52.31, DropoffLng: 4.76,
			PickupAt: 1733500000, Passengers: 1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, instant, err := s.Booking.QuoteRequest(ctx, tenantID, sr.ID); err != nil || !instant {
		t.Fatalf("quote: %v instant=%v", err, instant)
	}
	// Too fresh — no nudge.
	res, err := s.FollowUps.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if res.QuoteNudgeSent != 0 {
		t.Fatalf("fresh quote must not be nudged: %+v", res)
	}
	// Past the 24h default — one nudge, service lane, carrying the share link.
	clock = now.Add(25 * time.Hour)
	res, err = s.FollowUps.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if res.QuoteNudgeSent != 1 {
		t.Fatalf("expected 1 quote nudge: %+v", res)
	}
	sends, _ := s.Store.Outreach().SendsForClient(ctx, tenantID, cid, now.Add(-time.Hour))
	found := false
	for _, o := range sends {
		if strings.HasPrefix(o.IdempotencyKey, "quotenudge:") {
			found = true
			if o.Purpose != model.OutreachService {
				t.Fatal("quote nudge is a service-lane message")
			}
			if !strings.Contains(o.Body, "/quote/") {
				t.Fatalf("nudge must carry the share link: %q", o.Body)
			}
		}
	}
	if !found {
		t.Fatal("quotenudge send missing")
	}
	// Idempotent: no second nudge for the same quote.
	clock = now.Add(49 * time.Hour)
	res, _ = s.FollowUps.Run(ctx, tenantID)
	if res.QuoteNudgeSent != 0 {
		t.Fatalf("quote nudge fires once ever: %+v", res)
	}
}

func TestReviewAskAfterCompletion(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	s := testServices(t, Deps{Now: func() time.Time { return clock }, ConsoleBase: "https://console.example.com", WhatsAppURL: waStub(t).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	cid := outreachClient(t, s, tenantID, "+971500000141", false)
	rewardSetup(t, s, tenantID)(cid, "ra-book-1")
	// Completed assignment ended 4 days ago.
	bookings, _ := s.Store.Bookings().List(ctx, tenantID)
	if len(bookings) != 1 {
		t.Fatal("booking missing")
	}
	a := &model.Assignment{
		ID: NewID(), TenantID: tenantID, BookingID: bookings[0].ID,
		Status: "COMPLETED_TRIP", StartAt: now.Add(-5 * 24 * time.Hour), EndAt: now.Add(-4 * 24 * time.Hour),
	}
	if err := s.Store.Dispatch().CreateAssignment(ctx, a); err != nil {
		t.Fatal(err)
	}
	res, err := s.FollowUps.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if res.ReviewAskSent != 1 {
		t.Fatalf("expected 1 review ask: %+v", res)
	}
	sends, _ := s.Store.Outreach().SendsForClient(ctx, tenantID, cid, now.Add(-time.Hour))
	var ask *model.OutreachSend
	for i := range sends {
		if strings.HasPrefix(sends[i].IdempotencyKey, "review-ask:") {
			ask = &sends[i]
		}
	}
	if ask == nil || !strings.Contains(ask.Body, "#review") {
		t.Fatalf("review ask must deep-link the review form: %+v", ask)
	}
	// A submitted review silences future asks.
	if _, err := s.SubmitReview(ctx, tenantID, cid, a.ID, 5, "superb", true); err != nil {
		t.Fatal(err)
	}
	clock = now.Add(30 * 24 * time.Hour)
	res, _ = s.FollowUps.Run(ctx, tenantID)
	if res.ReviewAskSent != 0 {
		t.Fatalf("reviewed trip is never re-asked: %+v", res)
	}
}

func TestAnniversaryFiresOnDate(t *testing.T) {
	// Client's first booking was exactly one year ago today.
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	clock := now
	s := testServices(t, Deps{Now: func() time.Time { return clock }, WhatsAppURL: waStub(t).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	cid := outreachClient(t, s, tenantID, "+971500000142", true)
	// Backdate: client + a booking whose request anchors to the client.
	past := now.AddDate(-1, 0, 0)
	backdateClient(t, s, tenantID, cid, past)
	backdateBooking(t, s, tenantID, cid, past, "anniv-1")

	res, err := s.FollowUps.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if res.AnniversarySent+res.AnniversaryHeld != 1 {
		t.Fatalf("anniversary should fire on the date: %+v", res)
	}
	// Idempotent within the year (advance the clock so the replay is non-fresh).
	clock = now.Add(2 * time.Hour)
	res, _ = s.FollowUps.Run(ctx, tenantID)
	if res.AnniversarySent != 0 {
		t.Fatalf("anniversary fires once per year: %+v", res)
	}
}

// backdateClient forces a client's CreatedAt (tests only).
func backdateClient(t *testing.T, s *Services, tenantID, clientID string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	c, err := s.Store.Clients().Get(ctx, tenantID, clientID)
	if err != nil {
		t.Fatal(err)
	}
	c.CreatedAt = at
	if err := s.Store.Clients().Upsert(ctx, c); err != nil {
		t.Fatal(err)
	}
}

// backdateBooking writes a booking (with its request + quote) created at a
// past date, anchored to the client.
func backdateBooking(t *testing.T, s *Services, tenantID, clientID string, at time.Time, idem string) {
	t.Helper()
	ctx := context.Background()
	sr := &model.ServiceRequest{
		ID: NewID(), TenantID: tenantID, ClientID: clientID, State: model.StateConfirmed,
		IdempotencyKey: idem, CreatedAt: at, UpdatedAt: at,
	}
	if err := s.Store.Requests().Create(ctx, sr); err != nil {
		t.Fatal(err)
	}
	q := &model.Quote{
		ID: NewID(), TenantID: tenantID, RequestID: sr.ID, Version: 1, Origin: "INSTANT",
		TotalMinor: 10000, Currency: "EUR", ExpiresAt: at.Add(24 * time.Hour), CreatedAt: at,
	}
	if err := s.Store.Quotes().Create(ctx, q); err != nil {
		t.Fatal(err)
	}
	b := &model.Booking{
		ID: NewID(), TenantID: tenantID, RequestID: sr.ID, QuoteID: q.ID,
		State: "CONFIRMED", CreatedAt: at,
	}
	if err := s.Store.Bookings().Create(ctx, b); err != nil {
		t.Fatal(err)
	}
}

// ------------------------------------------------------------- O12: journey

func TestJourneyNotifications(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	cid := outreachClient(t, s, tenantID, "+971500000150", false)
	rewardSetup(t, s, tenantID)(cid, "jn-book-1")
	bookings, _ := s.Store.Bookings().List(ctx, tenantID)
	a := &model.Assignment{
		ID: NewID(), TenantID: tenantID, BookingID: bookings[0].ID, Status: "ASSIGNED",
	}
	if err := s.Store.Dispatch().CreateAssignment(ctx, a); err != nil {
		t.Fatal(err)
	}
	// EN_ROUTE_TO_PICKUP notifies the client on the service lane.
	if err := s.Dispatch.UpdateStatus(ctx, tenantID, a.ID, "EN_ROUTE_TO_PICKUP"); err != nil {
		t.Fatal(err)
	}
	sends, _ := s.Store.Outreach().SendsForClient(ctx, tenantID, cid, time.Now().Add(-time.Hour))
	var journey *model.OutreachSend
	for i := range sends {
		if strings.HasPrefix(sends[i].IdempotencyKey, "journey:") {
			journey = &sends[i]
		}
	}
	if journey == nil {
		t.Fatal("journey notification missing")
	}
	if journey.Purpose != model.OutreachService {
		t.Fatal("journey notifications ride the service lane")
	}
	if !strings.Contains(journey.Body, "on the way") {
		t.Fatalf("journey copy: %q", journey.Body)
	}
	// Idempotent per (assignment, status): a replayed transition is
	// impossible through the state machine, and the key guards the rest.
	sends2, _ := s.Store.Outreach().SendsForClient(ctx, tenantID, cid, time.Now().Add(-time.Hour))
	count := 0
	for _, o := range sends2 {
		if o.IdempotencyKey == "journey:"+a.ID+":EN_ROUTE_TO_PICKUP" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("exactly one journey send per transition, got %d", count)
	}
}

// ------------------------------------------------------------- A8: first-booking credit

func TestFirstBookingCredit(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	setAcquisitionConfig(t, s, tenantID, model.AcquisitionConfig{FirstBookingCreditMinor: 5000})
	cid := outreachClient(t, s, tenantID, "+971500000160", false)
	book := rewardSetup(t, s, tenantID)

	book(cid, "fb-1")
	bal, err := s.Store.Rewards().Balance(ctx, tenantID, cid)
	if err != nil {
		t.Fatal(err)
	}
	if bal != 5000 {
		t.Fatalf("first booking should credit 5000, got %d", bal)
	}
	// Second booking: no repeat credit.
	book(cid, "fb-2")
	bal, _ = s.Store.Rewards().Balance(ctx, tenantID, cid)
	if bal != 5000 {
		t.Fatalf("second booking must not re-credit, got %d", bal)
	}
}

// ------------------------------------------------------------- A5: leaderboard

func TestReferralLeaderboard(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	referrer := outreachClient(t, s, tenantID, "+971500000170", false)
	referee := outreachClient(t, s, tenantID, "+971500000171", false)
	quiet := outreachClient(t, s, tenantID, "+971500000172", false)
	s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "page_view", Channel: "referral",
		Campaign: "ref:" + model.ClientReferralCode(referrer), ClientID: referee,
	})
	setRewardProgram(t, s, tenantID, model.RewardProgram{ReferrerPct: 0.05})
	rewardSetup(t, s, tenantID)(referee, "lb-book-1")

	board, err := s.ReferralLeaderboard(ctx, tenantID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(board) != 1 || board[0].ClientID != referrer {
		t.Fatalf("leaderboard must rank the referrer only: %+v", board)
	}
	if board[0].Conversions != 1 || board[0].RewardsMinor <= 0 {
		t.Fatalf("conversion + reward expected: %+v", board[0])
	}
	for _, r := range board {
		if r.ClientID == quiet {
			t.Fatal("a client with no conversions never ranks")
		}
	}
}

// ------------------------------------------------------------- A4: quote share tokens

func TestQuoteShareTokenFamily(t *testing.T) {
	s := testServices(t, Deps{TripSecret: "test-secret", PublicBase: "https://api.example.com"})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	cid := outreachClient(t, s, tenantID, "+971500000180", false)
	q := rewardSetup(t, s, tenantID)(cid, "qs-book-1")

	share, err := s.Trips.Share(ctx, tenantID, q.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(share.URL, "https://api.example.com/quote/") {
		t.Fatalf("share URL: %s", share.URL)
	}
	// Round-trip.
	tid, qid, err := s.Trips.ParseQuoteToken(share.Token)
	if err != nil || tid != tenantID || qid != q.ID {
		t.Fatalf("token round-trip: %v %v %v", tid, qid, err)
	}
	// A quote token must never authenticate as a trip/track token.
	if _, _, err := s.Trips.ParseToken(share.Token); err == nil {
		t.Fatal("quote token must be rejected by the trip parser")
	}
	// Tampered token rejected.
	if _, _, err := s.Trips.ParseQuoteToken(share.Token[:len(share.Token)-2] + "xx"); err == nil {
		t.Fatal("tampered token must be rejected")
	}
	// The public view resolves with no client PII in the wire view.
	v, err := s.Trips.QuoteView(ctx, share.Token, "https://console.example.com/capture")
	if err != nil {
		t.Fatal(err)
	}
	if v.Quote.ID != q.ID || v.CaptureURL == "" {
		t.Fatalf("view: %+v", v)
	}
}
