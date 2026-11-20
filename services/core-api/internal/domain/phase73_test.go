package domain

// Phase 73: credit notes & refunds — credit notes shrink an issued
// statement (derived balances, aging, dunning and the statement document
// all recompute from the adjusted total), a fully-credited folio settles
// itself, refunds against settled folios carry reversal money truth, and
// over-credit/over-refund are conflicts, never silent absorption.

import (
	"context"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// refundPostings73 sums the reversal ledger postings keyed to one folio.
func refundPostings73(t *testing.T, s *Services, tenantID, folioID string) (net, fee int64) {
	t.Helper()
	postings, err := s.Payments.Postings(context.Background(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range postings {
		switch {
		case strings.HasPrefix(p.IdempotencyKey, "folio:refundnet:"+folioID):
			net += p.AmountMinor
			if p.DebitCode != "TENANT_REVENUE" || p.CreditCode != "PARTNER_CLEARING" {
				t.Fatalf("refund net posting: %+v", p)
			}
		case strings.HasPrefix(p.IdempotencyKey, "folio:refundfee:"+folioID):
			fee += p.AmountMinor
			if p.DebitCode != "PLATFORM_FEES" || p.CreditCode != "PARTNER_CLEARING" {
				t.Fatalf("refund fee posting: %+v", p)
			}
		}
	}
	return net, fee
}

func TestCreditNoteReducesBalance73(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	_, f := deskPartner67(t, s, tenantID, "Credit Hotel", "+31695557073", "cr-a", 2*24*time.Hour)

	// Pay 40.00 of 90.00, then credit 20.00 — the partner now owes 30.00.
	if _, err := s.Folios.RecordPayment(ctx, tenantID, f.ID, 4000, "bank_transfer", "TRX-1"); err != nil {
		t.Fatal(err)
	}
	f1, err := s.Folios.RecordCredit(ctx, tenantID, f.ID, 2000, "guest cancelled after issue")
	if err != nil {
		t.Fatal(err)
	}
	if f1.State != model.FolioIssued || len(f1.Credits) != 1 {
		t.Fatalf("after credit: %+v", f1)
	}
	cr := f1.Credits[0]
	if cr.Kind != model.FolioCreditNote || cr.AmountMinor != 2000 ||
		cr.Reason != "guest cancelled after issue" || cr.ID == "" || cr.CreatedAt.IsZero() {
		t.Fatalf("credit row: %+v", cr)
	}
	if f1.CreditedMinor() != 2000 || f1.AdjustedTotalMinor() != 7000 || f1.OutstandingMinor() != 3000 {
		t.Fatalf("derived: credited=%d adjusted=%d outstanding=%d",
			f1.CreditedMinor(), f1.AdjustedTotalMinor(), f1.OutstandingMinor())
	}

	// Over-credit — you cannot credit money the partner already paid.
	if _, err := s.Folios.RecordCredit(ctx, tenantID, f.ID, 3001, "too much"); err != httpx.ErrConflict {
		t.Fatalf("over-credit: %v", err)
	}

	// Aging chases the credit-adjusted balance, not the face value.
	report, err := s.Folios.Aging(ctx, tenantID)
	if err != nil || len(report.Rows) != 1 {
		t.Fatalf("aging: %+v %v", report, err)
	}
	if report.Rows[0].TotalMinor != 3000 || report.Rows[0].CollectedMinor != 4000 {
		t.Fatalf("aging after credit: %+v", report.Rows[0])
	}

	// The statement document agrees: credits visible, outstanding adjusted.
	token := s.Trips.StatementTokenFor(tenantID, f.ID)
	v, err := s.Folios.StatementByToken(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if v.CreditedMinor != 2000 || v.OutstandingMinor != 3000 || len(v.Credits) != 1 {
		t.Fatalf("statement view: %+v", v)
	}

	// Settle collects only the adjusted remainder — money truth sums to
	// 70.00, never the frozen 90.00 face value.
	f2, err := s.Folios.Settle(ctx, tenantID, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f2.State != model.FolioSettled || f2.PaidMinor() != 7000 {
		t.Fatalf("settled: %+v", f2)
	}
	net, fee := folioPostings70(t, s, tenantID, f.ID)
	if net+fee != 7000 {
		t.Fatalf("postings net=%d fee=%d — must sum to the adjusted total", net, fee)
	}
}

func TestCreditSettlesFolio73(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	_, f := deskPartner67(t, s, tenantID, "Goodwill Hotel", "+31695557074", "cr-b", time.Hour)

	// Part-paid, then credited to zero — a fully-accounted statement is
	// settled even though less than the face value changed hands.
	if _, err := s.Folios.RecordPayment(ctx, tenantID, f.ID, 5000, "cash", ""); err != nil {
		t.Fatal(err)
	}
	f1, err := s.Folios.RecordCredit(ctx, tenantID, f.ID, 4000, "goodwill")
	if err != nil {
		t.Fatal(err)
	}
	if f1.State != model.FolioSettled || f1.SettledAt.IsZero() || f1.OutstandingMinor() != 0 {
		t.Fatalf("credit-settled: %+v", f1)
	}
	// Terminal: no further credits, payments or reminders.
	if _, err := s.Folios.RecordPayment(ctx, tenantID, f.ID, 100, "cash", ""); err != httpx.ErrConflict {
		t.Fatalf("payment after credit-settle: %v", err)
	}
	if _, err := s.Folios.RemindNow(ctx, tenantID, f.ID); err != httpx.ErrConflict {
		t.Fatalf("remind after credit-settle: %v", err)
	}
	net, fee := folioPostings70(t, s, tenantID, f.ID)
	if net+fee != 5000 {
		t.Fatalf("postings net=%d fee=%d — only the paid money posted", net, fee)
	}
}

func TestCreditGuards73(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	// OPEN folio — still accruing, nothing frozen to correct.
	p := seedPartner56(t, s, tenantID, "Open Hotel", 0)
	p.Phone = "+31695557075"
	if err := s.Store.Partners().Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Folios.SetBillToDesk(ctx, tenantID, p.ID, true); err != nil {
		t.Fatal(err)
	}
	confirmSourced66(t, s, tenantID, p.ID, "+31690000999", "cr-c")
	open := folioFor66(t, s, tenantID, p.ID)
	if len(open) != 1 || open[0].State != model.FolioOpen {
		t.Fatalf("open folio: %+v", open)
	}
	if _, err := s.Folios.RecordCredit(ctx, tenantID, open[0].ID, 1000, "too early"); err != httpx.ErrConflict {
		t.Fatalf("credit on OPEN: %v", err)
	}

	// Amount must be positive.
	_, f := deskPartner67(t, s, tenantID, "Guard Hotel", "+31695557076", "cr-d", time.Hour)
	if _, err := s.Folios.RecordCredit(ctx, tenantID, f.ID, 0, ""); err != httpx.ErrValidation {
		t.Fatalf("zero credit: %v", err)
	}
	if _, err := s.Folios.RecordCredit(ctx, tenantID, f.ID, -100, ""); err != httpx.ErrValidation {
		t.Fatalf("negative credit: %v", err)
	}
}

func TestRefundReturnsMoney73(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	_, f := deskPartner67(t, s, tenantID, "Refund Hotel", "+31695557077", "cr-e", time.Hour)
	if _, err := s.Folios.Settle(ctx, tenantID, f.ID); err != nil {
		t.Fatal(err)
	}

	// Refund 20.00 of the collected 90.00 — money truth flows back.
	f1, err := s.Folios.RecordCredit(ctx, tenantID, f.ID, 2000, "overbilled line")
	if err != nil {
		t.Fatal(err)
	}
	if f1.State != model.FolioSettled || len(f1.Credits) != 1 ||
		f1.Credits[0].Kind != model.FolioCreditRefund || f1.OutstandingMinor() != 0 {
		t.Fatalf("after refund: %+v", f1)
	}
	tenant, err := s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	net, fee := refundPostings73(t, s, tenantID, f.ID)
	if want := PlatformFeeMinor(tenant.PricingRules, 2000); fee != want || net+fee != 2000 {
		t.Fatalf("refund postings net=%d fee=%d want fee=%d", net, fee, want)
	}

	// Over-refund is a conflict — refunds can never exceed what was paid.
	if _, err := s.Folios.RecordCredit(ctx, tenantID, f.ID, 7001, "too much"); err != httpx.ErrConflict {
		t.Fatalf("over-refund: %v", err)
	}
	if _, err := s.Folios.RecordCredit(ctx, tenantID, f.ID, 7000, "the rest"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Folios.RecordCredit(ctx, tenantID, f.ID, 1, "one cent too far"); err != httpx.ErrConflict {
		t.Fatalf("refund beyond paid: %v", err)
	}

	// The document shows the refund trail after settlement.
	v, err := s.Folios.StatementByToken(ctx, s.Trips.StatementTokenFor(tenantID, f.ID))
	if err != nil {
		t.Fatal(err)
	}
	if v.State != model.FolioSettled || v.CreditedMinor != 9000 || len(v.Credits) != 2 {
		t.Fatalf("statement view after refunds: %+v", v)
	}
}
