// Phase 52 domain tests: engagement-driven send-time optimization —
// the client's own opens/clicks/replies (attribution spine) beat the
// Phase 35 booking-pattern proxy, with a minimum sample, a quiet-hours
// override, full provenance in the send detail, and a composer preview
// on the verdict.
package domain

import (
	"context"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// trackEngagement52 seeds n engagement events of the given kind at the
// given hour for a client.
func trackEngagement52(t *testing.T, s *Services, tenantID, clientID, kind string, hour, n int, base time.Time) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		at := time.Date(base.Year(), base.Month(), base.Day()-i-1, hour, 15, 0, 0, time.UTC)
		if err := s.Store.Attribution().Track(ctx, &model.AttributionEvent{
			TenantID: tenantID, Kind: kind, ClientID: clientID,
			Channel: model.OutreachChannelEmail, At: at,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// seedBooking52 creates a request + booking pair whose CreatedAt sits at
// the given hour — the Phase 35 booking-pattern signal.
func seedBooking52(t *testing.T, s *Services, tenantID, clientID string, hour int, base time.Time) {
	t.Helper()
	ctx := context.Background()
	at := time.Date(base.Year(), base.Month(), base.Day()-2, hour, 30, 0, 0, time.UTC)
	rid := "req-sto-" + clientID
	if err := s.Store.Requests().Create(ctx, &model.ServiceRequest{
		ID: rid, TenantID: tenantID, ClientID: clientID,
		State: model.StateConfirmed, CreatedAt: at, UpdatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Bookings().Create(ctx, &model.Booking{
		ID: "bk-sto-" + clientID, TenantID: tenantID, RequestID: rid,
		QuoteID: "q-sto-" + clientID, State: "COMPLETED", CreatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEngagementSTOBeatsBookingPattern52(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	s := testServices(t, Deps{Now: func() time.Time { return now }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	setOutreachConfig(t, s, tenantID, model.OutreachConfig{OptimizeSendTime: true})
	cid := outreachClient(t, s, tenantID, "+971500000201", true)
	// Booking pattern says 09:00; engagement says 19:00. Engagement wins.
	seedBooking52(t, s, tenantID, cid, 9, now)
	trackEngagement52(t, s, tenantID, cid, "email_opened", 19, StoMinEngagements, now)

	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: cid, Purpose: model.OutreachMarketing, Body: "offers",
		IdempotencyKey: "sto-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != "scheduled" || o.ScheduledFor == nil {
		t.Fatalf("optimized send should schedule, got %s", o.Status)
	}
	if o.ScheduledFor.Hour() != 19 || !o.ScheduledFor.After(now) {
		t.Fatalf("should target the 19:00 engagement peak, got %v", o.ScheduledFor)
	}
	if !strings.Contains(o.Detail, "smart send time") ||
		!strings.Contains(o.Detail, "3 opens/clicks/replies") {
		t.Fatalf("detail must record the engagement evidence, got %q", o.Detail)
	}
}

func TestEngagementSTOMinSampleFallsBack52(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	s := testServices(t, Deps{Now: func() time.Time { return now }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	setOutreachConfig(t, s, tenantID, model.OutreachConfig{OptimizeSendTime: true})
	cid := outreachClient(t, s, tenantID, "+971500000202", true)
	// Only 2 engagements — below the trust threshold; booking pattern (09:00
	// is past → next day 09:00) takes over and says so.
	seedBooking52(t, s, tenantID, cid, 9, now)
	trackEngagement52(t, s, tenantID, cid, "email_clicked", 19, StoMinEngagements-1, now)

	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: cid, Purpose: model.OutreachMarketing, Body: "offers",
		IdempotencyKey: "sto-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != "scheduled" || o.ScheduledFor.Hour() != 9 {
		t.Fatalf("below-sample should fall back to booking hour 9, got %v", o.ScheduledFor)
	}
	if !strings.Contains(o.Detail, "booking pattern") {
		t.Fatalf("detail must admit the weaker signal, got %q", o.Detail)
	}
}

func TestEngagementSTONoEvidenceSendsNow52(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	s := testServices(t, Deps{Now: func() time.Time { return now }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	setOutreachConfig(t, s, tenantID, model.OutreachConfig{OptimizeSendTime: true})
	cid := outreachClient(t, s, tenantID, "+971500000203", true)

	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: cid, Purpose: model.OutreachMarketing, Body: "offers",
		IdempotencyKey: "sto-3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status == "scheduled" {
		t.Fatalf("no evidence = no deferral, got scheduled for %v", o.ScheduledFor)
	}
}

func TestQuietHoursOverrideEngagementSTO52(t *testing.T) {
	// 19:30, quiet 18→23, engagement peak 21:00: the STO hour lands inside
	// the quiet window, so the window end wins and the detail says why.
	now := time.Date(2026, 3, 10, 19, 30, 0, 0, time.UTC)
	s := testServices(t, Deps{Now: func() time.Time { return now }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	setOutreachConfig(t, s, tenantID, model.OutreachConfig{
		OptimizeSendTime: true, QuietStartHour: 18, QuietEndHour: 23})
	cid := outreachClient(t, s, tenantID, "+971500000204", true)
	trackEngagement52(t, s, tenantID, cid, "client_replied", 21, StoMinEngagements, now)

	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: cid, Purpose: model.OutreachMarketing, Body: "offers",
		IdempotencyKey: "sto-4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.ScheduledFor == nil || o.ScheduledFor.Hour() != 23 {
		t.Fatalf("quiet window end 23:00 must beat the 21:00 STO hour, got %v", o.ScheduledFor)
	}
	if !strings.Contains(o.Detail, "quiet hours") {
		t.Fatalf("detail must name quiet hours as the reason, got %q", o.Detail)
	}
}

func TestVerdictPreviewsOptimizedSendAt52(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	s := testServices(t, Deps{Now: func() time.Time { return now }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	setOutreachConfig(t, s, tenantID, model.OutreachConfig{OptimizeSendTime: true})
	cid := outreachClient(t, s, tenantID, "+971500000205", true)
	trackEngagement52(t, s, tenantID, cid, "email_opened", 19, StoMinEngagements, now)

	v, err := s.Outreach.Verdict(ctx, tenantID, cid, model.OutreachMarketing, model.OutreachChannelEmail)
	if err != nil {
		t.Fatal(err)
	}
	if v.OptimizedSendAt == nil || v.OptimizedSendAt.Hour() != 19 {
		t.Fatalf("marketing verdict should preview the 19:00 slot, got %v", v.OptimizedSendAt)
	}
	vs, err := s.Outreach.Verdict(ctx, tenantID, cid, model.OutreachService, model.OutreachChannelEmail)
	if err != nil {
		t.Fatal(err)
	}
	if vs.OptimizedSendAt != nil {
		t.Fatal("service verdicts never carry an optimization hint")
	}
}

func TestEngagementHourUnit52(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	s := testServices(t, Deps{Now: func() time.Time { return now }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	cid := outreachClient(t, s, tenantID, "+971500000206", true)

	// Non-engagement kinds and zero timestamps are not evidence.
	if err := s.Store.Attribution().Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "outreach_sent", ClientID: cid,
		Channel: model.OutreachChannelEmail, At: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if w, n := s.Outreach.engagementHour(ctx, tenantID, cid, now); !w.IsZero() || n != 0 {
		t.Fatalf("outreach_sent is not engagement evidence: %v n=%d", w, n)
	}

	// Peak hour already past today → next-day occurrence.
	trackEngagement52(t, s, tenantID, cid, "email_clicked", 8, StoMinEngagements, now)
	w, n := s.Outreach.engagementHour(ctx, tenantID, cid, now)
	if w.IsZero() || n != StoMinEngagements {
		t.Fatalf("expected the 3-sample peak, got %v n=%d", w, n)
	}
	if w.Hour() != 8 || w.Day() != now.Day()+1 {
		t.Fatalf("past peak must roll to next-day 08:00, got %v", w)
	}
}
