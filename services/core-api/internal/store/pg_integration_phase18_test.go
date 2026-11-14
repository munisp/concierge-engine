//go:build pg

// Postgres integration tests — Phase 18: client rewards lifecycle (accrue,
// idempotency, balance, oldest-first redeem, refund re-accrual, RLS), plus
// coverage extension for dispatch (fleet/crew/assignment), payment intents
// with reward_applied_minor, handoff queue, and quote expires_at NULL
// roundtrip (nilIfZero hardening). Same gate as pg_integration_test.go.
package store

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// pgQuoteBornBooking creates the request -> quote -> booking chain that
// bookings.quote_id NOT NULL requires (bookings are quote-born).
func pgQuoteBornBooking(t *testing.T, s Store, tenant, clientID, idem string) string {
	t.Helper()
	req := &model.ServiceRequest{ID: newTestUUID(), TenantID: tenant, ClientID: clientID,
		State: "NEW", Version: 1, IdempotencyKey: idem,
		Items:    []model.ServiceItem{{ID: newTestUUID(), TenantID: tenant, Vertical: "CHAUFFEUR_POINT_TO_POINT"}},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := s.Requests().Create(ctx18, req); err != nil {
		t.Fatalf("request: %v", err)
	}
	q := &model.Quote{ID: newTestUUID(), TenantID: tenant, RequestID: req.ID,
		Version: 1, Origin: "INSTANT", TotalMinor: 10000, Currency: "EUR",
		Lines: []model.QuoteLine{{Description: "base", AmountMinor: 10000}},
		ExpiresAt: time.Now().Add(time.Hour).UTC(), CreatedAt: time.Now().UTC()}
	if err := s.Quotes().Create(ctx18, q); err != nil {
		t.Fatalf("quote: %v", err)
	}
	bk := &model.Booking{ID: newTestUUID(), TenantID: tenant, RequestID: req.ID,
		QuoteID: q.ID, State: "CONFIRMED", CreatedAt: time.Now().UTC()}
	if err := s.Bookings().Create(ctx18, bk); err != nil {
		t.Fatalf("booking: %v", err)
	}
	return bk.ID
}

var ctx18 = context.Background()

func TestPGRewardsLifecycle(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-rewards")
	tenantB := pgTenant(t, s, "pg-rewards-b")

	referrer := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Referrer",
		KYCStatus: "none", Consent: map[string]any{}}
	if err := s.Clients().Upsert(ctx, referrer); err != nil {
		t.Fatalf("client: %v", err)
	}
	bookingA := pgQuoteBornBooking(t, s, tenant, referrer.ID, "pg-rw-r1")
	bookingB := pgQuoteBornBooking(t, s, tenant, referrer.ID, "pg-rw-r2")

	rw := s.Rewards()
	r1 := &model.ClientReward{ID: newTestUUID(), TenantID: tenant, ClientID: referrer.ID,
		SourceBookingID: bookingA, Kind: "referral_reward", AmountMinor: 500,
		Currency: "EUR", State: model.RewardAccrued, CreatedAt: time.Now().UTC()}
	if err := rw.Accrue(ctx, r1); err != nil {
		t.Fatalf("accrue r1: %v", err)
	}
	// Idempotent per (tenant, client, source booking) — the UNIQUE key.
	dup := *r1
	dup.ID = newTestUUID()
	if err := rw.Accrue(ctx, &dup); err != httpx.ErrConflict {
		t.Fatalf("duplicate accrual: want ErrConflict, got %v", err)
	}
	time.Sleep(10 * time.Millisecond) // keep created_at ordering unambiguous
	r2 := &model.ClientReward{ID: newTestUUID(), TenantID: tenant, ClientID: referrer.ID,
		SourceBookingID: bookingB, Kind: "referral_reward", AmountMinor: 700,
		Currency: "EUR", State: model.RewardAccrued, CreatedAt: time.Now().UTC()}
	if err := rw.Accrue(ctx, r2); err != nil {
		t.Fatalf("accrue r2: %v", err)
	}

	bal, err := rw.Balance(ctx, tenant, referrer.ID)
	if err != nil || bal != 1200 {
		t.Fatalf("balance: want 1200, got %d (%v)", bal, err)
	}
	// RLS: tenant B sees nothing of tenant A's rewards.
	if bal, err = rw.Balance(ctx, tenantB, referrer.ID); err != nil || bal != 0 {
		t.Fatalf("RLS leak: tenant B balance %d (%v)", bal, err)
	}

	// Oldest-first redemption covers the oldest reward whole.
	covered, err := rw.Redeem(ctx, tenant, referrer.ID, "intent-pg-1", 100)
	if err != nil || covered != 500 {
		t.Fatalf("redeem: want covered=500, got %d (%v)", covered, err)
	}
	if bal, _ = rw.Balance(ctx, tenant, referrer.ID); bal != 700 {
		t.Fatalf("balance after redeem: want 700, got %d", bal)
	}
	covered, err = rw.Redeem(ctx, tenant, referrer.ID, "intent-pg-2", 5000)
	if err != nil || covered != 700 {
		t.Fatalf("redeem rest: want covered=700, got %d (%v)", covered, err)
	}
	if bal, _ = rw.Balance(ctx, tenant, referrer.ID); bal != 0 {
		t.Fatalf("balance after second redeem: want 0, got %d", bal)
	}

	// Refund returns the second intent's rewards only.
	if err := rw.RefundRedemption(ctx, tenant, "intent-pg-2"); err != nil {
		t.Fatalf("refund redemption: %v", err)
	}
	if bal, _ = rw.Balance(ctx, tenant, referrer.ID); bal != 700 {
		t.Fatalf("balance after refund: want 700, got %d", bal)
	}
}

func TestPGDispatchRoundtrip(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-dispatch")

	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Dispatch Client",
		KYCStatus: "none", Consent: map[string]any{}}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatalf("client: %v", err)
	}
	bookingID := pgQuoteBornBooking(t, s, tenant, cl.ID, "pg-disp-r1")

	fleet := &model.FleetResource{ID: newTestUUID(), TenantID: tenant,
		Kind: "vehicle", Label: "S-Class · AM-123-X", QualityScore: 0.98, Active: true}
	if err := s.Dispatch().AddFleetResource(ctx, fleet); err != nil {
		t.Fatalf("fleet: %v", err)
	}
	crew := &model.CrewMember{ID: newTestUUID(), TenantID: tenant, FullName: "Ada Chauffeur",
		Certificates: []string{"defensive-driving", "vip-protocol"}, QualityScore: 0.95, Active: true}
	if err := s.Dispatch().AddCrewMember(ctx, crew); err != nil {
		t.Fatalf("crew: %v", err)
	}

	asg := &model.Assignment{ID: newTestUUID(), TenantID: tenant, BookingID: bookingID,
		FleetResourceID: fleet.ID, CrewMemberID: crew.ID, Status: "ASSIGNED",
		SLADeadline: time.Now().Add(2 * time.Hour).UTC(), CreatedAt: time.Now().UTC()}
	if err := s.Dispatch().CreateAssignment(ctx, asg); err != nil {
		t.Fatalf("assignment: %v", err)
	}
	if err := s.Dispatch().UpdateAssignmentStatus(ctx, tenant, asg.ID, "EN_ROUTE"); err != nil {
		t.Fatalf("assignment status: %v", err)
	}
	list, err := s.Dispatch().ListAssignments(ctx, tenant)
	if err != nil || len(list) != 1 {
		t.Fatalf("assignments: %+v %v", list, err)
	}
	if list[0].Status != "EN_ROUTE" || list[0].FleetResourceID != fleet.ID || list[0].CrewMemberID != crew.ID {
		t.Fatalf("assignment roundtrip: %+v", list[0])
	}
	fl, err := s.Dispatch().ListFleet(ctx, tenant)
	if err != nil || len(fl) != 1 || fl[0].Label != fleet.Label {
		t.Fatalf("fleet list: %+v %v", fl, err)
	}
	cr, err := s.Dispatch().ListCrew(ctx, tenant)
	if err != nil || len(cr) != 1 || len(cr[0].Certificates) != 2 {
		t.Fatalf("crew list: %+v %v", cr, err)
	}
}

func TestPGPaymentIntentRewardApplied(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-reward-pay")

	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Pay Client",
		KYCStatus: "none", Consent: map[string]any{}}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatalf("client: %v", err)
	}
	bookingID := pgQuoteBornBooking(t, s, tenant, cl.ID, "pg-rpay-r1")

	intent := &model.PaymentIntent{ID: newTestUUID(), TenantID: tenant, BookingID: bookingID,
		Mode: "FULL", AmountMinor: 10000, RewardAppliedMinor: 500, Currency: "EUR",
		PSP: "stripe", State: "LINK_SENT", IdempotencyKey: "pg-rpay-1",
		CreatedAt: time.Now().UTC()}
	if err := s.Payments().CreateIntent(ctx, intent); err != nil {
		t.Fatalf("intent: %v", err)
	}
	got, err := s.Payments().GetIntent(ctx, tenant, intent.ID)
	if err != nil {
		t.Fatalf("intent get: %v", err)
	}
	if got.RewardAppliedMinor != 500 || got.AmountMinor != 10000 {
		t.Fatalf("reward_applied_minor roundtrip: %+v", got)
	}

	// Compensation posting roundtrip (idempotent on IdempotencyKey).
	lp := &model.LedgerPosting{ID: newTestUUID(), TenantID: tenant, BookingID: bookingID,
		DebitCode: "PLATFORM_FEES", CreditCode: "CLIENT_CLEARING", AmountMinor: 500,
		Currency: "EUR", IdempotencyKey: "reward:redeem:" + intent.ID, CreatedAt: time.Now().UTC()}
	if err := s.Payments().AddPosting(ctx, lp); err != nil {
		t.Fatalf("posting: %v", err)
	}
	dup := *lp
	dup.ID = newTestUUID()
	if err := s.Payments().AddPosting(ctx, &dup); err != nil {
		t.Fatalf("posting replay must be a no-op: %v", err)
	}
	postings, err := s.Payments().Postings(ctx, tenant)
	if err != nil || len(postings) != 1 {
		t.Fatalf("postings: %+v %v", postings, err)
	}
	if postings[0].DebitCode != "PLATFORM_FEES" || postings[0].AmountMinor != 500 {
		t.Fatalf("posting roundtrip: %+v", postings[0])
	}
}

func TestPGHandoffsRoundtrip(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-handoff")

	conv := &model.Conversation{ID: newTestUUID(), TenantID: tenant,
		Channel: "whatsapp", State: "OPEN", ExternalRef: "conv-pg-1"}
	if err := s.Conversations().CreateConversation(ctx, conv); err != nil {
		t.Fatalf("conversation: %v", err)
	}
	h := &model.HandoffItem{ID: newTestUUID(), TenantID: tenant, ConversationID: conv.ID,
		Trigger: "VALUE", Reason: "booking value above auto-approve threshold",
		Brief:         map[string]any{"summary": "EUR 12,400 jet charter request"},
		PendingAction: map[string]any{"type": "quote", "quote_id": "q-1"},
		Status:        "OPEN", CreatedAt: time.Now().UTC()}
	if err := s.Handoffs().Open(ctx, h); err != nil {
		t.Fatalf("handoff open: %v", err)
	}
	got, err := s.Handoffs().Get(ctx, tenant, h.ID)
	if err != nil {
		t.Fatalf("handoff get: %v", err)
	}
	if got.Trigger != "VALUE" || got.Brief["summary"] == "" || got.PendingAction["type"] != "quote" {
		t.Fatalf("handoff roundtrip: %+v", got)
	}
	open, err := s.Handoffs().ListOpen(ctx, tenant)
	if err != nil || len(open) != 1 {
		t.Fatalf("list open: %+v %v", open, err)
	}
	if err := s.Handoffs().Resolve(ctx, tenant, h.ID, "APPROVED", "ops-lead"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	open, err = s.Handoffs().ListOpen(ctx, tenant)
	if err != nil || len(open) != 0 {
		t.Fatalf("resolved handoff must leave the open queue: %+v %v", open, err)
	}
}

func TestPGQuoteExpiresAtNull(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-quote-null")

	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Quote Client",
		KYCStatus: "none", Consent: map[string]any{}}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatalf("client: %v", err)
	}
	req := &model.ServiceRequest{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		State: "NEW", Version: 1, IdempotencyKey: "pg-qnull-r1",
		Items:    []model.ServiceItem{{ID: newTestUUID(), TenantID: tenant, Vertical: "CHAUFFEUR_POINT_TO_POINT"}},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := s.Requests().Create(ctx, req); err != nil {
		t.Fatalf("request: %v", err)
	}
	// Zero ExpiresAt must persist as NULL (nilIfZero), not 0001-01-01 — a
	// zero time would defeat the expiring-quotes partial index.
	q := &model.Quote{ID: newTestUUID(), TenantID: tenant, RequestID: req.ID,
		Version: 1, Origin: "INSTANT", TotalMinor: 9000, Currency: "EUR",
		Lines: []model.QuoteLine{{Description: "base", AmountMinor: 9000}},
		CreatedAt: time.Now().UTC()}
	if err := s.Quotes().Create(ctx, q); err != nil {
		t.Fatalf("quote create: %v", err)
	}
	got, err := s.Quotes().Get(ctx, tenant, q.ID)
	if err != nil {
		t.Fatalf("quote get: %v", err)
	}
	if !got.ExpiresAt.IsZero() {
		t.Fatalf("zero expires_at must roundtrip as zero, got %v", got.ExpiresAt)
	}
}
