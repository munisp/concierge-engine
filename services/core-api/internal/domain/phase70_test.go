package domain

// Phase 70: partial payments against issued folios — the verbatim
// payment ledger, derived paid/outstanding, proportional money-truth
// splits keyed per payment, overpayment rejection, auto-settlement at
// zero, Settle recording only the remainder, and aging/dunning chasing
// the balance instead of the face value.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// folioPostings70 sums the ledger postings keyed to one folio.
func folioPostings70(t *testing.T, s *Services, tenantID, folioID string) (net, fee int64) {
	t.Helper()
	postings, err := s.Payments.Postings(context.Background(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range postings {
		switch {
		case strings.HasPrefix(p.IdempotencyKey, "folio:net:"+folioID):
			net += p.AmountMinor
			if p.DebitCode != "PARTNER_CLEARING" || p.CreditCode != "TENANT_REVENUE" {
				t.Fatalf("net posting: %+v", p)
			}
		case strings.HasPrefix(p.IdempotencyKey, "folio:fee:"+folioID):
			fee += p.AmountMinor
			if p.CreditCode != "PLATFORM_FEES" {
				t.Fatalf("fee posting: %+v", p)
			}
		}
	}
	return net, fee
}

func TestFolioPartialPayments70(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	_, f := deskPartner67(t, s, tenantID, "Installments Hotel", "+31695557070", "pp-a", 2*24*time.Hour)

	// A payment must be a positive amount.
	if _, err := s.Folios.RecordPayment(ctx, tenantID, f.ID, 0, "cash", ""); err != httpx.ErrValidation {
		t.Fatalf("zero payment: %v", err)
	}
	if _, err := s.Folios.RecordPayment(ctx, tenantID, f.ID, -500, "cash", ""); err != httpx.ErrValidation {
		t.Fatalf("negative payment: %v", err)
	}

	// Partial: 40.00 of 90.00 — recorded verbatim, still ISSUED.
	f1, err := s.Folios.RecordPayment(ctx, tenantID, f.ID, 4000, " Bank_Transfer ", "TRX-1")
	if err != nil {
		t.Fatal(err)
	}
	if f1.State != model.FolioIssued || len(f1.Payments) != 1 ||
		f1.PaidMinor() != 4000 || f1.OutstandingMinor() != 5000 {
		t.Fatalf("after partial: %+v", f1)
	}
	pay1 := f1.Payments[0]
	if pay1.Method != "bank_transfer" || pay1.Reference != "TRX-1" ||
		pay1.ReceivedAt.IsZero() || pay1.ID == "" {
		t.Fatalf("payment row: %+v", pay1)
	}

	// Overpayment is a conflict — recorded money never exceeds the statement.
	if _, err := s.Folios.RecordPayment(ctx, tenantID, f.ID, 5001, "cash", ""); err != httpx.ErrConflict {
		t.Fatalf("overpayment: %v", err)
	}

	// Aging chases the outstanding balance, not the face value.
	report, err := s.Folios.Aging(ctx, tenantID)
	if err != nil || len(report.Rows) != 1 {
		t.Fatalf("aging: %+v %v", report, err)
	}
	row := report.Rows[0]
	if row.TotalMinor != 5000 || row.CurrentMinor != 5000 || row.CollectedMinor != 4000 {
		t.Fatalf("aging after partial: %+v", row)
	}

	// Second payment to zero: the folio settles itself.
	f2, err := s.Folios.RecordPayment(ctx, tenantID, f.ID, 5000, "cash", "")
	if err != nil {
		t.Fatal(err)
	}
	if f2.State != model.FolioSettled || f2.SettledAt.IsZero() ||
		f2.PaidMinor() != 9000 || f2.OutstandingMinor() != 0 || len(f2.Payments) != 2 {
		t.Fatalf("auto-settled: %+v", f2)
	}

	// Terminal: no more payments, no reminders, no settle replay.
	if _, err := s.Folios.RecordPayment(ctx, tenantID, f.ID, 100, "cash", ""); err != httpx.ErrConflict {
		t.Fatalf("payment after settle: %v", err)
	}
	if _, err := s.Folios.RemindNow(ctx, tenantID, f2.ID); err != httpx.ErrConflict {
		t.Fatalf("remind after settle: %v", err)
	}

	// Money truth: every payment posted its own proportional split; the
	// postings sum exactly to the statement total.
	net, fee := folioPostings70(t, s, tenantID, f.ID)
	if net == 0 || fee == 0 || net+fee != 9000 {
		t.Fatalf("postings net=%d fee=%d", net, fee)
	}
	tenant, err := s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	wantFee := PlatformFeeMinor(tenant.PricingRules, 4000) + PlatformFeeMinor(tenant.PricingRules, 5000)
	if fee != wantFee {
		t.Fatalf("fee %d != per-payment splits %d", fee, wantFee)
	}

	// A settled folio leaves the aging working set.
	report, _ = s.Folios.Aging(ctx, tenantID)
	if len(report.Rows) != 0 {
		t.Fatalf("settled must leave aging: %+v", report)
	}
}

func TestFolioSettleAfterPartial70(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	_, f := deskPartner67(t, s, tenantID, "Remainder Hotel", "", "pp-b", 2*24*time.Hour)

	if _, err := s.Folios.RecordPayment(ctx, tenantID, f.ID, 3000, "bank_transfer", "TRX-9"); err != nil {
		t.Fatal(err)
	}
	// Settle records only the outstanding remainder as the final payment.
	settled, err := s.Folios.Settle(ctx, tenantID, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.State != model.FolioSettled || len(settled.Payments) != 2 ||
		settled.PaidMinor() != 9000 || settled.OutstandingMinor() != 0 {
		t.Fatalf("settled after partial: %+v", settled)
	}
	last := settled.Payments[1]
	if last.Method != "settle" || last.AmountMinor != 6000 {
		t.Fatalf("remainder payment: %+v", last)
	}
	// Idempotent: a settle replay changes nothing.
	again, err := s.Folios.Settle(ctx, tenantID, f.ID)
	if err != nil || len(again.Payments) != 2 {
		t.Fatalf("settle replay: %+v %v", again, err)
	}
	// Ledger: remainder split keyed folio:{id} + the partial's keyed split.
	net, fee := folioPostings70(t, s, tenantID, f.ID)
	if net+fee != 9000 {
		t.Fatalf("postings net=%d fee=%d", net, fee)
	}
}

func TestFolioPaymentOnOpen70(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	// An OPEN folio is still accruing — its total is not frozen, so it
	// cannot take payments.
	p := seedPartner56(t, s, tenantID, "Accruing Hotel", 0)
	if _, err := s.Folios.SetBillToDesk(ctx, tenantID, p.ID, true); err != nil {
		t.Fatal(err)
	}
	confirmSourced66(t, s, tenantID, p.ID, "+31690000998", "pp-c")
	folios := folioFor66(t, s, tenantID, p.ID)
	if len(folios) != 1 || folios[0].State != model.FolioOpen {
		t.Fatalf("open folio: %+v", folios)
	}
	if _, err := s.Folios.RecordPayment(ctx, tenantID, folios[0].ID, 1000, "cash", ""); err != httpx.ErrConflict {
		t.Fatalf("payment on OPEN: %v", err)
	}
}

func TestFolioDunningOutstanding70(t *testing.T) {
	// The reminder message quotes the outstanding balance, not the face
	// value — a half-paid statement is dunned for the half still owed.
	var mu sync.Mutex
	var bodies []string
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "Payment reminder") {
			mu.Lock()
			bodies = append(bodies, string(b))
			mu.Unlock()
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer adapter.Close()

	s := testServices(t, Deps{WhatsAppURL: adapter.URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	// 15 days since issue, 14-day terms → 1 day overdue → rung 1 due.
	_, f := deskPartner67(t, s, tenantID, "Half Paid Hotel", "+31695557071", "pp-d", 15*24*time.Hour)
	if _, err := s.Folios.RecordPayment(ctx, tenantID, f.ID, 5000, "cash", ""); err != nil {
		t.Fatal(err)
	}
	res, err := s.Folios.RunDunning(ctx, tenantID)
	if err != nil || res.Sent != 1 {
		t.Fatalf("sweep: %+v %v", res, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 || !strings.Contains(bodies[0], "40.00 EUR") ||
		strings.Contains(bodies[0], "90.00 EUR") {
		t.Fatalf("reminder body must quote the balance: %v", bodies)
	}
}
