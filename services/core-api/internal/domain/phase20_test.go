package domain

// Phase 27: tenant onboarding journey, brownfield import, and the
// acquisition innovation pack (gift vouchers, switch offers, referral
// boost windows, dormant-lead nudges, reviews, partner co-marketing).

import (
	"context"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// stepStatus finds a step's derived status in an onboarding report.
func stepStatus(rep *OnboardingReport, id string) string {
	for _, s := range rep.Steps {
		if s.ID == id {
			return s.Status
		}
	}
	return ""
}

func TestOnboardingJourney(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s) // flagship archetype → default track "existing"

	rep, err := s.Onboarding(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Track != "existing" {
		t.Fatalf("flagship should default to existing track, got %q", rep.Track)
	}
	if got := stepStatus(rep, "catalog"); got != "pending" {
		t.Fatalf("catalog should be pending, got %q", got)
	}
	// Website is live by construction — the step is "know it exists".
	if got := stepStatus(rep, "website"); got != "done" {
		t.Fatalf("website should be done by construction, got %q", got)
	}
	if rep.NextStep == "" {
		t.Fatal("expected a next step")
	}

	// Add a catalog service — the step flips without any status write.
	if _, err := s.Catalog.Add(ctx, &model.CatalogService{
		TenantID: tenantID, Vertical: "ground", Name: "Airport Transfer",
		BaseMinor: 9500, Currency: "EUR", AutoQuotable: true,
	}); err != nil {
		t.Fatal(err)
	}
	rep, _ = s.Onboarding(ctx, tenantID)
	if got := stepStatus(rep, "catalog"); got != "done" {
		t.Fatalf("catalog should derive done after Add, got %q", got)
	}

	// Track switch + skip/reinstate.
	if _, err := s.UpdateOnboarding(ctx, tenantID, "new", []string{"team"}, nil); err != nil {
		t.Fatal(err)
	}
	rep, _ = s.Onboarding(ctx, tenantID)
	if rep.Track != "new" {
		t.Fatalf("track not switched: %q", rep.Track)
	}
	if got := stepStatus(rep, "team"); got != "skipped" {
		t.Fatalf("team should be skipped, got %q", got)
	}
	if _, err := s.UpdateOnboarding(ctx, tenantID, "", nil, []string{"team"}); err != nil {
		t.Fatal(err)
	}
	rep, _ = s.Onboarding(ctx, tenantID)
	if got := stepStatus(rep, "team"); got != "pending" {
		t.Fatalf("team should be reinstated to pending, got %q", got)
	}
	if _, err := s.UpdateOnboarding(ctx, tenantID, "bogus", nil, nil); err != httpx.ErrValidation {
		t.Fatalf("bogus track: want ErrValidation, got %v", err)
	}
	if _, err := s.UpdateOnboarding(ctx, tenantID, "", []string{"bogus"}, nil); err != httpx.ErrValidation {
		t.Fatalf("bogus skip: want ErrValidation, got %v", err)
	}
}

func TestImportCatalogAndClients(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	res, err := s.ImportCatalog(ctx, tenantID, []ImportCatalogItem{
		{Vertical: "ground", Name: "Airport Transfer", BaseMinor: 9500},
		{Vertical: "ground", Name: "Hourly Chauffeur", BaseMinor: 6500, PerHourMinor: 5500},
		{Vertical: "ground", Name: "airport transfer", BaseMinor: 1}, // case-insensitive dup
		{Vertical: "", Name: "broken row"},                           // invalid
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 2 || res.Skipped != 1 || len(res.Errors) != 1 {
		t.Fatalf("import: want 2 created / 1 skipped / 1 error, got %+v", res)
	}
	// Re-run: fully idempotent — everything is now a duplicate.
	res, err = s.ImportCatalog(ctx, tenantID, []ImportCatalogItem{
		{Vertical: "ground", Name: "Airport Transfer", BaseMinor: 9500},
	})
	if err != nil || res.Created != 0 || res.Skipped != 1 {
		t.Fatalf("re-import should skip: %+v err=%v", res, err)
	}
	// Currency defaults to the tenant's pricing currency.
	services, _ := s.Catalog.List(ctx, tenantID)
	for _, svc0 := range services {
		if svc0.Currency != "EUR" {
			t.Fatalf("currency default: got %q", svc0.Currency)
		}
	}

	// Clients: phone dedupe, marketing consent OFF, attribution "import".
	cres, err := s.ImportClients(ctx, tenantID, []ImportClientItem{
		{FullName: "Ada Lovelace", Phone: "+447700900001", Email: "ada@example.com"},
		{FullName: "Alan Turing", Phone: "+447700900002"},
		{FullName: "Dup", Phone: "+447700900001"},
		{FullName: "NoPhone"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cres.Created != 2 || cres.Skipped != 1 || len(cres.Errors) != 1 {
		t.Fatalf("client import: want 2/1/1, got %+v", cres)
	}
	ada, err := s.Store.Clients().FindByPhone(ctx, tenantID, "+447700900001")
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := ada.Consent["marketing"].(bool); m {
		t.Fatal("imported client must NOT carry marketing consent")
	}
	if imp, _ := ada.Consent["imported"].(bool); !imp {
		t.Fatal("imported marker missing")
	}
	ch, _ := s.FirstTouch(ctx, tenantID, ada.ID)
	if ch != "import" {
		t.Fatalf("first touch should be import (never an acquisition), got %q", ch)
	}
}

func TestGiftVoucherAcquisition(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	c, err := s.CreatePromo(ctx, tenantID, CreatePromoInput{
		Code: "GIFT-ADA", CreditMinor: 5000, Currency: "EUR", Gift: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxRedemptions != 1 {
		t.Fatalf("gift voucher must force single redemption, got %d", c.MaxRedemptions)
	}
	// Recipient redeems → gift:<code> attribution stamped.
	recipient := outreachClient(t, s, tenantID, "+31633330001", false)
	if _, err := s.RedeemPromo(ctx, tenantID, "gift-ada", recipient); err != nil {
		t.Fatal(err)
	}
	_, campaign := s.FirstTouch(ctx, tenantID, recipient)
	if campaign != "gift:GIFT-ADA" {
		t.Fatalf("gift attribution: got %q", campaign)
	}
	if got := classifyAcquisition("web", campaign); got != "gift" {
		t.Fatalf("classify gift: got %q", got)
	}
	// The voucher is spent — a second redeemer is refused.
	other := outreachClient(t, s, tenantID, "+31633330002", false)
	if _, err := s.RedeemPromo(ctx, tenantID, "GIFT-ADA", other); err != httpx.ErrConflict {
		t.Fatalf("second gift redemption: want ErrConflict, got %v", err)
	}
	// And the gifted first booking assesses the acquisition fee (gift ∈ defaults).
	intentID := captureAcq(t, s, tenantID, recipient, "gift-book-1", 100000)
	fees, _ := acqFees(t, s, tenantID)
	if fees != 10000 {
		t.Fatalf("gifted first booking should assess 10%% fee, got %d", fees)
	}
	_ = intentID
}

func TestSwitchOfferAlert(t *testing.T) {
	calls := 0
	s := testServices(t, Deps{WhatsAppURL: waCountingStub(t, &calls).URL, OperatorPhone: "+31600000000"})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	if _, err := s.CreatePromo(ctx, tenantID, CreatePromoInput{
		Code: "SWITCH25", CreditMinor: 2500, Currency: "EUR", SwitchOffer: true,
	}); err != nil {
		t.Fatal(err)
	}
	clientID := outreachClient(t, s, tenantID, "+31644440001", false)
	if _, err := s.RedeemPromo(ctx, tenantID, "SWITCH25", clientID); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("switch redemption should alert the operator roster, calls=%d", calls)
	}
	// A plain promo alerts nobody.
	if _, err := s.CreatePromo(ctx, tenantID, CreatePromoInput{
		Code: "PLAIN10", CreditMinor: 1000, Currency: "EUR",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemPromo(ctx, tenantID, "PLAIN10", clientID); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("plain promo must not alert, calls=%d", calls)
	}
}

func TestReferralBoostWindow(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+31655550001", false)

	prog := model.RewardProgram{ReferrerPct: 0.05}
	// No window: 5% of 100000 = 5000.
	if amt := s.accrueReward(ctx, tenantID, clientID, NewID(), "referral_reward", 100000, "EUR", prog.ReferrerPct, prog); amt != 5000 {
		t.Fatalf("base accrual: want 5000, got %d", amt)
	}
	// Live window at 2x: doubles.
	prog.BoostUntil = s.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	prog.BoostMultiplier = 2
	if amt := s.accrueReward(ctx, tenantID, clientID, NewID(), "referral_reward", 100000, "EUR", prog.ReferrerPct, prog); amt != 10000 {
		t.Fatalf("boosted accrual: want 10000, got %d", amt)
	}
	// Expired window: back to base.
	prog.BoostUntil = s.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if amt := s.accrueReward(ctx, tenantID, clientID, NewID(), "referral_reward", 100000, "EUR", prog.ReferrerPct, prog); amt != 5000 {
		t.Fatalf("expired window should not boost, got %d", amt)
	}
	// Malformed window: inert, never crashes accrual.
	prog.BoostUntil = "not-a-date"
	if amt := s.accrueReward(ctx, tenantID, clientID, NewID(), "referral_reward", 100000, "EUR", prog.ReferrerPct, prog); amt != 5000 {
		t.Fatalf("malformed window should be inert, got %d", amt)
	}
}

func TestLeadNudge(t *testing.T) {
	calls := 0
	s := testServices(t, Deps{WhatsAppURL: waCountingStub(t, &calls).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	// Dormant lead: created 10 days ago, marketing consent, zero bookings.
	lead := outreachClient(t, s, tenantID, "+31666660001", true)
	c, _ := s.Store.Clients().Get(ctx, tenantID, lead)
	c.CreatedAt = s.Now().Add(-10 * 24 * time.Hour)
	if err := s.Store.Clients().Upsert(ctx, c); err != nil {
		t.Fatal(err)
	}
	// Fresh lead: 2 days old — not yet.
	fresh := outreachClient(t, s, tenantID, "+31666660002", true)
	// Booked client: old but has a booking — win_back's territory.
	booked := outreachClient(t, s, tenantID, "+31666660003", true)
	bookAt(t, s, tenantID, booked, "lead-book-1", s.Now().Add(48*time.Hour).Unix(), 2)

	res, err := s.FollowUps.Run(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if res.LeadNudgeSent != 1 {
		t.Fatalf("want exactly 1 lead nudge, got %+v", res)
	}
	// Idempotent within the ISO week.
	res, _ = s.FollowUps.Run(ctx, tenantID)
	if res.LeadNudgeSent != 0 {
		t.Fatalf("replay must not re-nudge, got %+v", res)
	}
	_ = fresh
}

func TestReviewsAndSummary(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+31677770001", false)

	// Consented review counts; non-consented does not.
	if _, err := s.SubmitReview(ctx, tenantID, clientID, "asg-1", 5, "Flawless airport run.", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitReview(ctx, tenantID, clientID, "asg-2", 3, "", false); err != nil {
		t.Fatal(err)
	}
	// Replay: same assignment returns the original, no duplicate.
	r1, _ := s.SubmitReview(ctx, tenantID, clientID, "asg-1", 1, "changed my mind", false)
	if r1.Rating != 5 {
		t.Fatalf("replay should return original rating 5, got %d", r1.Rating)
	}
	sum, err := s.ReviewSummary(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Count != 1 || sum.Average != 5.0 {
		t.Fatalf("summary should count consented only: %+v", sum)
	}
	if len(sum.Recent) != 1 || sum.Recent[0] != "Flawless airport run." {
		t.Fatalf("recent texts: %+v", sum.Recent)
	}
	// Validation.
	if _, err := s.SubmitReview(ctx, tenantID, clientID, "asg-3", 6, "", false); err != httpx.ErrValidation {
		t.Fatalf("rating 6: want ErrValidation, got %v", err)
	}
}

func TestPartnerAcquisition(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	p := &model.Partner{ID: NewID(), TenantID: tenantID, Name: "Hotel Grande", Status: "VETTED"}
	if err := s.Store.Partners().Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	pa, err := s.PartnerAcquisition(ctx, tenantID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pa.Link, "campaign=partner%3A"+p.ID) && !strings.Contains(pa.Link, "campaign=partner:"+p.ID) {
		t.Fatalf("partner link should carry campaign, got %q", pa.Link)
	}
	// A conversion through the link attributes to the partner.
	clientID := outreachClient(t, s, tenantID, "+31688880001", false)
	s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "page_view", Channel: "partner",
		Campaign: "partner:" + p.ID, ClientID: clientID,
	})
	s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "booking", Channel: "partner",
		Campaign: "partner:" + p.ID, ClientID: clientID, ValueMinor: 42000,
	})
	pa, _ = s.PartnerAcquisition(ctx, tenantID, p.ID)
	if pa.Bookings != 1 || pa.ValueMinor != 42000 || pa.Leads != 1 {
		t.Fatalf("partner metrics: %+v", pa)
	}
	// Unknown partner → not found.
	if _, err := s.PartnerAcquisition(ctx, tenantID, NewID()); err != httpx.ErrNotFound {
		t.Fatalf("unknown partner: want ErrNotFound, got %v", err)
	}
}
