package domain

// Phase 74: partner self-service payment links — the pay/ capability
// token, the honest pay page view model (rail live only when a rail can
// actually charge), hosted checkout minting on the tenant's rail, the
// signed-webhook capture converging on the same payment ledger as
// operator-keyed payments, replay safety, and the loud mismatch path
// when captured money exceeds the remaining balance.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPayToken74(t *testing.T) {
	s := testServices(t, Deps{})
	tok := s.Trips.PayTokenFor("t-1", "f-1")
	tenantID, folioID, err := s.Trips.ParsePayToken(tok)
	if err != nil || tenantID != "t-1" || folioID != "f-1" {
		t.Fatalf("roundtrip: %s %s %v", tenantID, folioID, err)
	}
	// Cross-family tokens never open a payment page.
	if _, _, err := s.Trips.ParsePayToken(s.Trips.StatementTokenFor("t-1", "f-1")); err != httpx.ErrForbidden {
		t.Fatalf("statement token accepted: %v", err)
	}
	if _, _, err := s.Trips.ParsePayToken(s.Trips.ConsentTokenFor("t-1", "c-1")); err != httpx.ErrForbidden {
		t.Fatalf("consent token accepted: %v", err)
	}
	if _, _, err := s.Trips.ParsePayToken("garbage"); err == nil {
		t.Fatal("garbage token accepted")
	}
}

func TestPayViewHonestRail74(t *testing.T) {
	s := testServices(t, Deps{}) // no PSP keys — no live rail
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	_, f := deskPartner67(t, s, tenantID, "Pay Hotel", "+31695557078", "pay-a", time.Hour)

	v, err := s.Folios.PayByToken(ctx, s.Trips.PayTokenFor(tenantID, f.ID))
	if err != nil {
		t.Fatal(err)
	}
	if v.RailLive || v.Rail != "" {
		t.Fatalf("no keys must mean no live rail: %+v", v)
	}
	if v.OutstandingMinor != 9000 || v.State != model.FolioIssued || v.DueAt == nil {
		t.Fatalf("view: %+v", v)
	}

	// No live rail → checkout is a conflict, never a fake page.
	if _, err := s.Folios.StartCheckout(ctx, s.Trips.PayTokenFor(tenantID, f.ID)); err != httpx.ErrConflict {
		t.Fatalf("checkout without rail: %v", err)
	}

	// Settled: the page reports PAID, offers nothing.
	if _, err := s.Folios.Settle(ctx, tenantID, f.ID); err != nil {
		t.Fatal(err)
	}
	v, err = s.Folios.PayByToken(ctx, s.Trips.PayTokenFor(tenantID, f.ID))
	if err != nil {
		t.Fatal(err)
	}
	if v.State != model.FolioSettled || v.RailLive {
		t.Fatalf("settled view: %+v", v)
	}
}

func TestFolioCheckoutStripe74(t *testing.T) {
	var gotForm url.Values
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"id": "cs_folio_1", "url": "https://checkout.stripe.com/c/pay/cs_folio_1"})
	}))
	defer fake.Close()

	s := testServices(t, Deps{StripeKey: "sk_test_123", StripeBase: fake.URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	_, f := deskPartner67(t, s, tenantID, "Checkout Hotel", "+31695557079", "pay-b", time.Hour)
	// Part-paid first — checkout charges the balance, not the face value.
	if _, err := s.Folios.RecordPayment(ctx, tenantID, f.ID, 4000, "bank_transfer", "TRX-1"); err != nil {
		t.Fatal(err)
	}

	token := s.Trips.PayTokenFor(tenantID, f.ID)
	v, err := s.Folios.PayByToken(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if !v.RailLive || v.Rail != "stripe" || v.OutstandingMinor != 5000 {
		t.Fatalf("live view: %+v", v)
	}

	hosted, err := s.Folios.StartCheckout(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if hosted != "https://checkout.stripe.com/c/pay/cs_folio_1" {
		t.Fatalf("hosted url: %s", hosted)
	}
	if gotForm.Get("metadata[folio_id]") != f.ID ||
		gotForm.Get("line_items[0][price_data][unit_amount]") != "5000" ||
		gotForm.Get("line_items[0][price_data][product_data][name]") != "Partner statement — "+f.Period {
		t.Fatalf("checkout form: %v", gotForm)
	}

	// Capture via the signed-webhook path — converges on the same
	// ledger. The webhook carries the intent id from the checkout
	// metadata.
	intentID := gotForm.Get("metadata[intent_id]")
	if intentID == "" {
		t.Fatal("intent id missing from checkout metadata")
	}
	if err := s.Folios.HandleFolioCapture(ctx, tenantID, f.ID, intentID, "stripe", "cs_folio_1"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Folios.Get(ctx, tenantID, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != model.FolioSettled || got.PaidMinor() != 9000 {
		t.Fatalf("after capture: %+v", got)
	}
	last := got.Payments[len(got.Payments)-1]
	if last.Method != "stripe" || last.Reference != "cs_folio_1" ||
		last.AmountMinor != 5000 || last.RecordedBy != "system" {
		t.Fatalf("capture payment row: %+v", last)
	}

	// Replay-safe: the PSP retry is acknowledged, the ledger unchanged.
	if err := s.Folios.HandleFolioCapture(ctx, tenantID, f.ID, intentID, "stripe", "cs_folio_1"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Folios.Get(ctx, tenantID, f.ID)
	if len(got.Payments) != 2 {
		t.Fatalf("replay double-posted: %+v", got.Payments)
	}

	// Money truth: postings sum to the face value across both rails of
	// collection (bank transfer + stripe capture).
	net, fee := folioPostings70(t, s, tenantID, f.ID)
	if net+fee != 9000 {
		t.Fatalf("postings net=%d fee=%d", net, fee)
	}
}

func TestFolioCaptureMismatch74(t *testing.T) {
	var gotForm url.Values
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"id": "cs_folio_2", "url": "https://checkout.stripe.com/c/pay/cs_folio_2"})
	}))
	defer fake.Close()

	s := testServices(t, Deps{StripeKey: "sk_test_123", StripeBase: fake.URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	_, f := deskPartner67(t, s, tenantID, "Mismatch Hotel", "+31695557080", "pay-c", time.Hour)
	token := s.Trips.PayTokenFor(tenantID, f.ID)
	if _, err := s.Folios.StartCheckout(ctx, token); err != nil {
		t.Fatal(err)
	}
	intentID := gotForm.Get("metadata[intent_id]")

	// The partner ALSO pays by bank transfer before the card capture
	// lands — the balance moved under the open checkout.
	if _, err := s.Folios.Settle(ctx, tenantID, f.ID); err != nil {
		t.Fatal(err)
	}

	// The capture cannot be silently absorbed: acknowledged, emitted,
	// audit-logged — and the payment ledger is NOT corrupted.
	if err := s.Folios.HandleFolioCapture(ctx, tenantID, f.ID, intentID, "stripe", "cs_folio_2"); err != nil {
		t.Fatalf("mismatch must ack, not 5xx: %v", err)
	}
	got, err := s.Folios.Get(ctx, tenantID, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PaidMinor() != 9000 || len(got.Payments) != 1 {
		t.Fatalf("mismatch corrupted the ledger: %+v", got.Payments)
	}
	intent, err := s.Store.Payments().GetIntent(ctx, tenantID, intentID)
	if err != nil {
		t.Fatal(err)
	}
	if intent.State != "CAPTURED" {
		t.Fatalf("intent not reconciled: %+v", intent)
	}
}
