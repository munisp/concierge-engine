package domain

// Phase 47 domain tests: KYC / identity verification lane — case workflow,
// booking gate, expiry sweep, erasure companion.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// kycQuoteFixture seeds a client plus an instantly-quotable request and
// returns the quote.
func kycQuoteFixture(t *testing.T, s *Services, tenantID, clientID string) *model.Quote {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Catalog.Add(ctx, &model.CatalogService{
		TenantID: tenantID, Vertical: "CHAUFFEUR_POINT_TO_POINT", Name: "Sedan|business",
		BaseMinor: 9000, Currency: "EUR", PerKMMinor: 250, AutoQuotable: true, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	sr, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
		TenantID: tenantID, ClientID: clientID, IdempotencyKey: "kyc-req-" + NewID()[:8],
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
	return q
}

func TestKycCaseLifecycle47(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := emailClient(t, s, tenantID, true)

	// Open: happy path.
	kc, err := s.Kyc.Open(ctx, tenantID, clientID, model.KycLevelStandard, "kyc-1")
	if err != nil {
		t.Fatal(err)
	}
	if kc.Status != model.KycStatusPending || kc.Provider != KycProviderManual {
		t.Fatalf("new case = %s/%s, want PENDING/manual", kc.Status, kc.Provider)
	}

	// Idempotent replay: same key returns the original case.
	replay, err := s.Kyc.Open(ctx, tenantID, clientID, model.KycLevelStandard, "kyc-1")
	if err != nil {
		t.Fatalf("idem replay: %v", err)
	}
	if replay.ID != kc.ID {
		t.Fatalf("replay returned %s, want original %s", replay.ID, kc.ID)
	}

	// One open case per (client, level): a second open under a new key conflicts.
	if _, err := s.Kyc.Open(ctx, tenantID, clientID, model.KycLevelStandard, "kyc-2"); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("second open case = %v, want conflict", err)
	}

	// Submit validation: unknown document type, empty ref.
	if _, err := s.Kyc.Submit(ctx, tenantID, kc.ID, "library_card", "ref-1"); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("bad document type = %v, want validation", err)
	}
	if _, err := s.Kyc.Submit(ctx, tenantID, kc.ID, "passport", ""); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("empty ref = %v, want validation", err)
	}

	// Decide before submit: 409.
	if _, err := s.Kyc.Decide(ctx, tenantID, kc.ID, true, "", "ops"); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("decide before submit = %v, want conflict", err)
	}

	// Submit → IN_REVIEW.
	kc, err = s.Kyc.Submit(ctx, tenantID, kc.ID, "passport", "vault://taylor/p-123")
	if err != nil {
		t.Fatal(err)
	}
	if kc.Status != model.KycStatusInReview || kc.SubmittedAt.IsZero() {
		t.Fatalf("submitted case = %s", kc.Status)
	}

	// Reject requires a reason.
	if _, err := s.Kyc.Decide(ctx, tenantID, kc.ID, false, "", "ops"); !errors.Is(err, httpx.ErrValidation) {
		t.Fatalf("reasonless reject = %v, want validation", err)
	}

	// Approve → client status advances to "standard".
	kc, err = s.Kyc.Decide(ctx, tenantID, kc.ID, true, "", "ops-lead")
	if err != nil {
		t.Fatal(err)
	}
	if kc.Status != model.KycStatusApproved || kc.DecidedAt.IsZero() || kc.DecidedBy != "ops-lead" {
		t.Fatalf("approved case = %+v", kc)
	}
	c, err := s.Store.Clients().Get(ctx, tenantID, clientID)
	if err != nil {
		t.Fatal(err)
	}
	if c.KYCStatus != "standard" {
		t.Fatalf("client kyc_status = %q, want standard", c.KYCStatus)
	}

	// Terminal replay with the same decision is a no-op; a conflicting
	// re-decision is 409.
	if _, err := s.Kyc.Decide(ctx, tenantID, kc.ID, true, "", "ops-lead"); err != nil {
		t.Fatalf("absorbing replay: %v", err)
	}
	if _, err := s.Kyc.Decide(ctx, tenantID, kc.ID, false, "changed mind", "ops-lead"); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("conflicting re-decision = %v, want conflict", err)
	}

	// Enhanced case: approval advances the client to "enhanced".
	ec, err := s.Kyc.Open(ctx, tenantID, clientID, model.KycLevelEnhanced, "kyc-3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Kyc.Submit(ctx, tenantID, ec.ID, "passport", "vault://taylor/p-123"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Kyc.Decide(ctx, tenantID, ec.ID, true, "", "ops-lead"); err != nil {
		t.Fatal(err)
	}
	c, _ = s.Store.Clients().Get(ctx, tenantID, clientID)
	if c.KYCStatus != "enhanced" {
		t.Fatalf("client kyc_status = %q, want enhanced", c.KYCStatus)
	}

	// A STANDARD approval must not regress an ENHANCED client — and Open
	// itself refuses a level the client already holds.
	if _, err := s.Kyc.Open(ctx, tenantID, clientID, model.KycLevelStandard, "kyc-4"); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("open below held level = %v, want conflict", err)
	}

	// Queue lists newest first and filters by status.
	queue, err := s.Kyc.List(ctx, tenantID, model.KycStatusApproved, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 2 {
		t.Fatalf("approved queue = %d, want 2", len(queue))
	}
}

func TestKycBookingGate47(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := emailClient(t, s, tenantID, true)

	// Tenant gates every booking at STANDARD (threshold below any total).
	tn, err := s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	tn.KycConfig = model.KycConfig{StandardAboveMinor: 1, EnhancedAboveMinor: -1}
	if err := s.Store.Tenants().UpdateConfig(ctx, tn); err != nil {
		t.Fatal(err)
	}

	q := kycQuoteFixture(t, s, tenantID, clientID)

	// Unverified client: confirm blocked with kyc_required, and the gate
	// auto-opened a STANDARD case so the operator queue shows the work.
	if _, err := s.Booking.Confirm(ctx, tenantID, q.ID); err == nil || !strings.Contains(err.Error(), "kyc_required") {
		t.Fatalf("gated confirm = %v, want kyc_required conflict", err)
	}
	cases, err := s.Kyc.ListForClient(ctx, tenantID, clientID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 || cases[0].Level != model.KycLevelStandard || cases[0].Status != model.KycStatusPending {
		t.Fatalf("auto-opened cases = %+v", cases)
	}

	// Rejecting the case does not unblock.
	if _, err := s.Kyc.Submit(ctx, tenantID, cases[0].ID, "passport", "vault://taylor/p-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Kyc.Decide(ctx, tenantID, cases[0].ID, false, "illegible document", "ops"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Booking.Confirm(ctx, tenantID, q.ID); err == nil || !strings.Contains(err.Error(), "kyc_required") {
		t.Fatalf("confirm after rejection = %v, want kyc_required conflict", err)
	}

	// Fresh case → approve → confirm succeeds.
	kc2, err := s.Kyc.Open(ctx, tenantID, clientID, model.KycLevelStandard, "kyc-gate-2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Kyc.Submit(ctx, tenantID, kc2.ID, "passport", "vault://taylor/p-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Kyc.Decide(ctx, tenantID, kc2.ID, true, "", "ops"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Booking.Confirm(ctx, tenantID, q.ID); err != nil {
		t.Fatalf("confirm after approval: %v", err)
	}
}

func TestKycGateLevelsAndDisabled47(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := emailClient(t, s, tenantID, true)

	// Enhanced required above 5000 minor: a 9000+km quote crosses it.
	tn, _ := s.Store.Tenants().Get(ctx, tenantID)
	tn.KycConfig = model.KycConfig{StandardAboveMinor: 1, EnhancedAboveMinor: 5000}
	if err := s.Store.Tenants().UpdateConfig(ctx, tn); err != nil {
		t.Fatal(err)
	}
	q := kycQuoteFixture(t, s, tenantID, clientID)

	// STANDARD-verified client still fails an ENHANCED requirement.
	kc, _ := s.Kyc.Open(ctx, tenantID, clientID, model.KycLevelStandard, "lvl-1")
	_, _ = s.Kyc.Submit(ctx, tenantID, kc.ID, "passport", "vault://taylor/p-3")
	if _, err := s.Kyc.Decide(ctx, tenantID, kc.ID, true, "", "ops"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Booking.Confirm(ctx, tenantID, q.ID); err == nil || !strings.Contains(err.Error(), model.KycLevelEnhanced) {
		t.Fatalf("standard client on enhanced quote = %v, want ENHANCED requirement", err)
	}

	// Disabled config: no gate at all.
	tn, _ = s.Store.Tenants().Get(ctx, tenantID)
	tn.KycConfig = model.KycConfig{Disabled: true}
	if err := s.Store.Tenants().UpdateConfig(ctx, tn); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Booking.Confirm(ctx, tenantID, q.ID); err != nil {
		t.Fatalf("disabled gate must not block: %v", err)
	}

	// RequiredKycLevel threshold semantics: defaults, negative = never.
	if got := RequiredKycLevel(model.KycConfig{}, KycDefaultStandardAboveMinor-1); got != "" {
		t.Fatalf("below default standard threshold = %q", got)
	}
	if got := RequiredKycLevel(model.KycConfig{}, KycDefaultStandardAboveMinor); got != model.KycLevelStandard {
		t.Fatalf("at default standard threshold = %q", got)
	}
	if got := RequiredKycLevel(model.KycConfig{}, KycDefaultEnhancedAboveMinor); got != model.KycLevelEnhanced {
		t.Fatalf("at default enhanced threshold = %q", got)
	}
	if got := RequiredKycLevel(model.KycConfig{StandardAboveMinor: -1, EnhancedAboveMinor: -1}, 1<<60); got != "" {
		t.Fatalf("never-require = %q", got)
	}
}

func TestKycExpiryAndErasure47(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := emailClient(t, s, tenantID, true)

	// Open a case and backdate it past the default 30-day expiry window.
	kc, err := s.Kyc.Open(ctx, tenantID, clientID, model.KycLevelStandard, "exp-1")
	if err != nil {
		t.Fatal(err)
	}
	kc.CreatedAt = s.Now().AddDate(0, 0, -31)
	if err := s.Store.KycCases().Update(ctx, kc); err != nil {
		t.Fatal(err)
	}
	n, err := s.Kyc.ExpireStale(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expired = %d, want 1", n)
	}
	got, _ := s.Kyc.Get(ctx, tenantID, kc.ID)
	if got.Status != model.KycStatusExpired {
		t.Fatalf("status = %s, want EXPIRED", got.Status)
	}
	// Idempotent: second sweep expires nothing.
	if n, _ := s.Kyc.ExpireStale(ctx, tenantID); n != 0 {
		t.Fatalf("second sweep expired = %d, want 0", n)
	}
	// Expired is terminal: decide conflicts.
	if _, err := s.Kyc.Decide(ctx, tenantID, kc.ID, true, "", "ops"); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("decide expired = %v, want conflict", err)
	}

	// Erasure: an APPROVED case's document refs are scrubbed immediately,
	// the case row survives as the AML record.
	kc2, _ := s.Kyc.Open(ctx, tenantID, clientID, model.KycLevelStandard, "exp-2")
	if _, err := s.Kyc.Submit(ctx, tenantID, kc2.ID, "national_id", "vault://taylor/n-9"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Kyc.Decide(ctx, tenantID, kc2.ID, true, "", "ops"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Clients.EraseWithEvidence(ctx, tenantID, clientID); err != nil {
		t.Fatal(err)
	}
	got2, err := s.Kyc.Get(ctx, tenantID, kc2.ID)
	if err != nil {
		t.Fatalf("case row must survive erasure: %v", err)
	}
	if got2.Status != model.KycStatusApproved || got2.DocumentType != "" || got2.DocumentRef != "" {
		t.Fatalf("post-erasure case = %+v, want APPROVED with scrubbed refs", got2)
	}

	// Erased clients cannot open new cases.
	if _, err := s.Kyc.Open(ctx, tenantID, clientID, model.KycLevelEnhanced, "exp-3"); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("open for erased client = %v, want conflict", err)
	}
}
