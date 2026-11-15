package domain

// Phase 40: campaign economics (spend ledger → cost-per-conversion → ROAS)
// and the email deliverability guardrail.

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

// emailClientAt creates the n-th distinct email+phone client, consent as
// asked — the guard tests need enough clients to fill the volume gate
// without tripping the per-client frequency cap.
func emailClientAt(t *testing.T, s *Services, tenantID string, n int, marketingConsent bool) string {
	t.Helper()
	ctx := context.Background()
	rr, err := s.Clients.Resolve(ctx, tenantID,
		fmt.Sprintf("+9715000%06d", n), fmt.Sprintf("buyer%d@acme.example", n), "Buyer")
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

// blastEmails sends n marketing emails, rotating to a fresh client every
// time the per-client weekly cap runs out.
func blastEmails(t *testing.T, s *Services, tenantID string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		cid := emailClientAt(t, s, tenantID, i/OutreachMarketingWeeklyCap, true)
		o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
			ClientID: cid, Purpose: model.OutreachMarketing, Channel: "email",
			Body: "offer", Subject: "Offer", IdempotencyKey: fmt.Sprintf("blast-%d", i),
		})
		if err != nil || o.Status != "sent" {
			t.Fatalf("blast send %d: %+v %v", i, o, err)
		}
	}
}

func trackBounces(t *testing.T, s *Services, tenantID string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		s.Events.Track(context.Background(), &model.AttributionEvent{
			TenantID: tenantID, Kind: "email_bounced", Channel: "email",
			Campaign: fmt.Sprintf("<bounce-%d@mail.taylor.example>", i),
		})
	}
}

func TestRecordSpendValidation40(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	if _, err := s.Campaigns.RecordSpend(ctx, tenantID, RecordSpendInput{
		Campaign: "", AmountMinor: 1000, Currency: "NGN",
	}); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("empty campaign: %v", err)
	}
	if _, err := s.Campaigns.RecordSpend(ctx, tenantID, RecordSpendInput{
		Campaign: "c1", AmountMinor: 0, Currency: "NGN",
	}); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("zero amount: %v", err)
	}
	if _, err := s.Campaigns.RecordSpend(ctx, tenantID, RecordSpendInput{
		Campaign: "c1", AmountMinor: 1000, Currency: "U1",
	}); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("bad currency: %v", err)
	}
	if _, err := s.Campaigns.RecordSpend(ctx, tenantID, RecordSpendInput{
		Campaign: "c1", AmountMinor: 1000, Currency: "NGN", SpentOn: "16/11/2026",
	}); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("bad spent_on: %v", err)
	}
	// Happy path: currency normalized, spent_on defaults to today.
	sp, err := s.Campaigns.RecordSpend(ctx, tenantID, RecordSpendInput{
		Campaign: "lagos-launch", Channel: "paid_search", AmountMinor: 100000,
		Currency: "ngn", Note: "Google Ads week 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sp.Currency != "NGN" || sp.SpentOn != s.Now().Format("2006-01-02") {
		t.Fatalf("normalization: %+v", sp)
	}
}

func TestRecordSpendIdempotent40(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	first, err := s.Campaigns.RecordSpend(ctx, tenantID, RecordSpendInput{
		Campaign: "lagos-launch", AmountMinor: 50000, Currency: "NGN",
		IdempotencyKey: "spend-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Replay: same key, even with a different amount, returns the original —
	// one cost, recorded once.
	again, err := s.Campaigns.RecordSpend(ctx, tenantID, RecordSpendInput{
		Campaign: "lagos-launch", AmountMinor: 999999, Currency: "NGN",
		IdempotencyKey: "spend-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || again.AmountMinor != 50000 {
		t.Fatalf("replay must return the original: %+v vs %+v", again, first)
	}
	// Unkeyed entries always insert (two real invoices are two costs).
	if _, err := s.Campaigns.RecordSpend(ctx, tenantID, RecordSpendInput{
		Campaign: "lagos-launch", AmountMinor: 1000, Currency: "NGN",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Campaigns.RecordSpend(ctx, tenantID, RecordSpendInput{
		Campaign: "lagos-launch", AmountMinor: 1000, Currency: "NGN",
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Campaigns.SpendList(ctx, tenantID, "lagos-launch", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 spend rows, got %d", len(rows))
	}
	// Cross-tenant isolation.
	other := seedTenant(t, s)
	rows, _ = s.Campaigns.SpendList(ctx, other, "lagos-launch", 50)
	if len(rows) != 0 {
		t.Fatalf("tenant isolation: %d rows leaked", len(rows))
	}
}

func TestCampaignEconomics40(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	if _, err := s.Campaigns.RecordSpend(ctx, tenantID, RecordSpendInput{
		Campaign: "lagos-launch", AmountMinor: 100000, Currency: "NGN",
	}); err != nil {
		t.Fatal(err)
	}
	// Two attributed bookings under the campaign: ₦2 500 + ₦1 500.
	s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "booking", Channel: "paid_search",
		Campaign: "lagos-launch", ValueMinor: 250000})
	s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "booking", Channel: "paid_search",
		Campaign: "lagos-launch", ValueMinor: 150000})
	// Noise that must NOT join the campaign's economics.
	s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "booking", Channel: "seo",
		Campaign: "other-campaign", ValueMinor: 999999})
	s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "page_view", Channel: "paid_search",
		Campaign: "lagos-launch"})

	f, err := s.Campaigns.Funnel(ctx, tenantID, "lagos-launch")
	if err != nil {
		t.Fatal(err)
	}
	if f.Converted != 2 || f.AttributedValueMinor != 400000 {
		t.Fatalf("attribution: %+v", f)
	}
	if f.SpendMinor != 100000 || f.Currency != "NGN" {
		t.Fatalf("spend: %+v", f)
	}
	if f.CostPerConversionMinor != 50000 {
		t.Fatalf("cost per conversion = %d, want 50000", f.CostPerConversionMinor)
	}
	if f.ROAS != 4.0 {
		t.Fatalf("ROAS = %v, want 4.0", f.ROAS)
	}

	// Mixed currency: the totals sum but the ratios are withheld — summing
	// naira and dollars into one number would be a lie, not a metric.
	if _, err := s.Campaigns.RecordSpend(ctx, tenantID, RecordSpendInput{
		Campaign: "lagos-launch", AmountMinor: 500, Currency: "USD",
	}); err != nil {
		t.Fatal(err)
	}
	f, err = s.Campaigns.Funnel(ctx, tenantID, "lagos-launch")
	if err != nil {
		t.Fatal(err)
	}
	if !f.MixedCurrency || f.Currency != "" || f.SpendMinor != 100500 {
		t.Fatalf("mixed currency: %+v", f)
	}
	if f.CostPerConversionMinor != 0 || f.ROAS != 0 {
		t.Fatalf("ratios must be withheld on mixed currency: %+v", f)
	}
}

func TestCampaignsBoard40(t *testing.T) {
	waCalls := 0
	s := testServices(t, Deps{WhatsAppURL: waCountingStub(t, &waCalls).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	outreachClient(t, s, tenantID, "+971500000077", true)

	// A campaign with spend but no sends yet (paid list, not yet mailed).
	if _, err := s.Campaigns.RecordSpend(ctx, tenantID, RecordSpendInput{
		Campaign: "paid-only", AmountMinor: 5000, Currency: "NGN",
	}); err != nil {
		t.Fatal(err)
	}
	// A campaign with sends but no spend (organic broadcast).
	res, err := s.Campaigns.Broadcast(ctx, tenantID, BroadcastInput{
		Campaign: "wa-blast", Body: "Transfers open.", Segment: model.SegmentRules{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 1 {
		t.Fatalf("broadcast: %+v", res)
	}
	s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "booking", Channel: "whatsapp",
		Campaign: "wa-blast", ValueMinor: 80000})

	board, err := s.Campaigns.Campaigns(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(board) != 2 || board[0].Campaign != "paid-only" || board[1].Campaign != "wa-blast" {
		t.Fatalf("board: %+v", board)
	}
	paid, blast := board[0], board[1]
	if paid.SpendMinor != 5000 || paid.Sent != 0 || paid.Currency != "NGN" {
		t.Fatalf("paid-only row: %+v", paid)
	}
	if blast.Sent != 1 || blast.Converted != 1 || blast.AttributedValueMinor != 80000 || blast.SpendMinor != 0 {
		t.Fatalf("wa-blast row: %+v", blast)
	}
}

func TestDeliverabilityGuard40(t *testing.T) {
	emCalls, waCalls := 0, 0
	s := testServices(t, Deps{
		WhatsAppURL: waCountingStub(t, &waCalls).URL,
		EmailURL:    emailStub(t, &emCalls, nil).URL,
		TripSecret:  "s",
	})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	// 21 delivered marketing emails, then the ESP reports 3 bounces:
	// 3/21 ≈ 14% — over the 5% ceiling at meaningful volume.
	blastEmails(t, s, tenantID, 21)
	trackBounces(t, s, tenantID, 3)

	cid := emailClientAt(t, s, tenantID, 99, true)
	v, err := s.Outreach.Verdict(ctx, tenantID, cid, model.OutreachMarketing, "email")
	if err != nil {
		t.Fatal(err)
	}
	if v.Allowed || !strings.Contains(v.Reason, "deliverability guard") {
		t.Fatalf("guard must hold: %+v", v)
	}
	if v.EmailSent7d != 21 {
		t.Fatalf("email_sent_7d = %d, want 21", v.EmailSent7d)
	}
	if v.EmailBounceRate < 0.14 || v.EmailBounceRate > 0.15 {
		t.Fatalf("bounce rate = %v, want ≈0.143", v.EmailBounceRate)
	}
	// A send under the guard is held and auditable — the adapter is never
	// called.
	before := emCalls
	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: cid, Purpose: model.OutreachMarketing, Channel: "email",
		Body: "offer", Subject: "Offer", IdempotencyKey: "guarded-1",
	})
	if err != nil || o.Status != "held" || !strings.Contains(o.Detail, "deliverability guard") {
		t.Fatalf("guarded send: %+v %v", o, err)
	}
	if emCalls != before {
		t.Fatal("held send must not reach the adapter")
	}

	// Service email is exempt — a safety message still goes.
	v, _ = s.Outreach.Verdict(ctx, tenantID, cid, model.OutreachService, "email")
	if !v.Allowed {
		t.Fatalf("service lane must survive the guard: %+v", v)
	}
	// WhatsApp is untouched by email reputation.
	waID := outreachClient(t, s, tenantID, "+971500099999", true)
	v, _ = s.Outreach.Verdict(ctx, tenantID, waID, model.OutreachMarketing, model.OutreachChannelWhatsApp)
	if !v.Allowed {
		t.Fatalf("whatsapp must be unaffected: %+v", v)
	}
}

func TestDeliverabilityGuardBelowVolume40(t *testing.T) {
	emCalls := 0
	s := testServices(t, Deps{EmailURL: emailStub(t, &emCalls, nil).URL, TripSecret: "s"})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	// 19 sends with 5 bounces (26%!) — but below the volume gate there is
	// no signal worth pausing a tenant for.
	blastEmails(t, s, tenantID, 19)
	trackBounces(t, s, tenantID, 5)

	cid := emailClientAt(t, s, tenantID, 99, true)
	v, err := s.Outreach.Verdict(ctx, tenantID, cid, model.OutreachMarketing, "email")
	if err != nil || !v.Allowed {
		t.Fatalf("below volume gate the guard stays inert: %+v %v", v, err)
	}
	if v.EmailSent7d != 19 || v.EmailBounceRate <= 0.26 {
		t.Fatalf("health fields still report honestly: %+v", v)
	}
}

func TestDeliverabilityGuardRecovery40(t *testing.T) {
	emCalls := 0
	now := time.Now().UTC() // anchored to real time — memory stamps events with it
	s := testServices(t, Deps{
		Now:        func() time.Time { return now },
		EmailURL:   emailStub(t, &emCalls, nil).URL,
		TripSecret: "s",
	})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	blastEmails(t, s, tenantID, 21)
	trackBounces(t, s, tenantID, 3)
	cid := emailClientAt(t, s, tenantID, 99, true)
	v, _ := s.Outreach.Verdict(ctx, tenantID, cid, model.OutreachMarketing, "email")
	if v.Allowed {
		t.Fatalf("guard should be tripped: %+v", v)
	}

	// Eight days later the bad window has aged out of the 7-day view — the
	// guard is self-healing, no operator intervention required.
	now = now.Add(8 * 24 * time.Hour)
	v, err := s.Outreach.Verdict(ctx, tenantID, cid, model.OutreachMarketing, "email")
	if err != nil || !v.Allowed {
		t.Fatalf("guard must release after the window ages out: %+v %v", v, err)
	}
	if v.EmailSent7d != 0 {
		t.Fatalf("window must be empty: %+v", v)
	}
	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: cid, Purpose: model.OutreachMarketing, Channel: "email",
		Body: "we are back", Subject: "Back", IdempotencyKey: "recovery-1",
	})
	if err != nil || o.Status != "sent" {
		t.Fatalf("post-recovery send: %+v %v", o, err)
	}
}
