package domain

// Phase 67: folio aging & dunning — derived buckets against tenant terms,
// the escalating reminder cadence on the folio's own ledger, honest skips,
// one-rung-per-sweep catch-up, and the manual-nudge 24h guard.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// deskPartner67 seeds a bill-to-desk partner with one sourced booking
// accrued, then issues the folio backdated by `age` so aging is
// deterministic. Returns the partner and the issued folio.
func deskPartner67(t *testing.T, s *Services, tenantID, name, phone, idem string, age time.Duration) (*model.Partner, *model.Folio) {
	t.Helper()
	ctx := context.Background()
	p := seedPartner56(t, s, tenantID, name, 0)
	p.Phone = phone
	if err := s.Store.Partners().Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Folios.SetBillToDesk(ctx, tenantID, p.ID, true); err != nil {
		t.Fatal(err)
	}
	confirmSourced66(t, s, tenantID, p.ID, "+31690000999", idem) // Sedan 9000 EUR
	folios := folioFor66(t, s, tenantID, p.ID)
	if len(folios) != 1 {
		t.Fatalf("folio seeded: %+v", folios)
	}
	issued, err := s.Folios.Issue(ctx, tenantID, p.ID, folios[0].Period, folios[0].Currency)
	if err != nil {
		t.Fatal(err)
	}
	issued.IssuedAt = s.Now().Add(-age)
	if err := s.Store.Folios().Update(ctx, issued); err != nil {
		t.Fatal(err)
	}
	return p, issued
}

func TestFolioAging67(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	deskPartner67(t, s, tenantID, "Old Debt Hotel", "", "ag-a", 50*24*time.Hour)  // 36d past terms
	deskPartner67(t, s, tenantID, "Fresh Due Hotel", "", "ag-b", 16*24*time.Hour) // 2d past terms
	deskPartner67(t, s, tenantID, "Current Hotel", "", "ag-c", 2*24*time.Hour)    // inside terms

	report, err := s.Folios.Aging(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if report.TermsDays != 14 || len(report.Rows) != 3 {
		t.Fatalf("report: %+v", report)
	}
	// Most overdue first.
	if report.Rows[0].PartnerName != "Old Debt Hotel" || report.Rows[0].OldestOverdueDays != 36 {
		t.Fatalf("row 0: %+v", report.Rows[0])
	}
	if report.Rows[0].Overdue30PlusMinor != 9000 || report.Rows[0].CurrentMinor != 0 {
		t.Fatalf("old bucket: %+v", report.Rows[0])
	}
	if report.Rows[1].PartnerName != "Fresh Due Hotel" || report.Rows[1].Overdue1To7Minor != 9000 {
		t.Fatalf("row 1: %+v", report.Rows[1])
	}
	if report.Rows[2].PartnerName != "Current Hotel" || report.Rows[2].CurrentMinor != 9000 ||
		report.Rows[2].TotalMinor != 9000 || report.Rows[2].OldestOverdueDays > 0 {
		t.Fatalf("row 2: %+v", report.Rows[2])
	}
	for _, row := range report.Rows {
		if row.Currency != "EUR" || row.OpenFolios != 1 {
			t.Fatalf("row shape: %+v", row)
		}
	}

	// Terms are the tenant's own: 30-day terms re-bucket everything.
	tenant, err := s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	tenant.PricingRules.FolioTermsDays = 30
	if err := s.Store.Tenants().UpdateConfig(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	report, err = s.Folios.Aging(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if report.TermsDays != 30 {
		t.Fatalf("terms: %+v", report)
	}
	if report.Rows[0].PartnerName != "Old Debt Hotel" || report.Rows[0].Overdue8To30Minor != 9000 {
		t.Fatalf("30d terms row 0: %+v", report.Rows[0])
	}
	if report.Rows[1].CurrentMinor != 9000 || report.Rows[2].CurrentMinor != 9000 {
		t.Fatalf("30d terms current rows: %+v", report.Rows)
	}
}

func TestFolioDunning67(t *testing.T) {
	// Count only dunning touches — Phase 64 confirm notifications share
	// the adapter and arrive on detached goroutines.
	var reminders atomic.Int32
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "Payment reminder") {
			reminders.Add(1)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer adapter.Close()

	s := testServices(t, Deps{WhatsAppURL: adapter.URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	// 15 days since issue, 14-day terms → 1 day overdue → rung 1 due.
	hotel, f1 := deskPartner67(t, s, tenantID, "Dun Hotel", "+31695550101", "dn-a", 15*24*time.Hour)

	res, err := s.Folios.RunDunning(ctx, tenantID)
	if err != nil || res.Sent != 1 || res.Skipped != 0 {
		t.Fatalf("sweep 1: %+v %v", res, err)
	}
	if reminders.Load() != 1 {
		t.Fatalf("reminder calls: %d", reminders.Load())
	}
	got, err := s.Folios.Get(ctx, tenantID, f1.ID)
	if err != nil || len(got.Reminders) != 1 {
		t.Fatalf("reminder ledger: %+v %v", got, err)
	}
	r1 := got.Reminders[0]
	if r1.N != 1 || r1.Channel != "whatsapp" || r1.Status != model.FolioReminderSent {
		t.Fatalf("reminder 1: %+v", r1)
	}

	// Rung 2 needs 7 days past due — an immediate re-sweep sends nothing.
	res, err = s.Folios.RunDunning(ctx, tenantID)
	if err != nil || res.Sent != 0 || res.Skipped != 0 || reminders.Load() != 1 {
		t.Fatalf("sweep 2: %+v calls=%d %v", res, reminders.Load(), err)
	}

	// A partner with no contact channel is SKIPPED — recorded verbatim on
	// the folio, never faked as sent.
	hotel2, f2 := deskPartner67(t, s, tenantID, "No Phone Hotel", "", "dn-b", 15*24*time.Hour)
	res, err = s.Folios.RunDunning(ctx, tenantID)
	if err != nil || res.Sent != 0 || res.Skipped != 1 || reminders.Load() != 1 {
		t.Fatalf("sweep 3: %+v calls=%d %v", res, reminders.Load(), err)
	}
	got2, err := s.Folios.Get(ctx, tenantID, f2.ID)
	if err != nil || len(got2.Reminders) != 1 || got2.Reminders[0].Status != "skipped:no-contact" {
		t.Fatalf("skip ledger: %+v %v", got2, err)
	}
	_ = hotel
	_ = hotel2

	// Manual nudge: one touch per 24h across sweep and manual sends.
	if _, err := s.Folios.RemindNow(ctx, tenantID, f1.ID); err != httpx.ErrConflict {
		t.Fatalf("manual within 24h: %v", err)
	}
	got, _ = s.Folios.Get(ctx, tenantID, f1.ID)
	got.Reminders[0].CreatedAt = s.Now().Add(-25 * time.Hour)
	if err := s.Store.Folios().Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	nudged, err := s.Folios.RemindNow(ctx, tenantID, f1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(nudged.Reminders) != 2 || nudged.Reminders[1].N != 2 || nudged.Reminders[1].Status != model.FolioReminderSent {
		t.Fatalf("manual nudge: %+v", nudged.Reminders)
	}
	if reminders.Load() != 2 {
		t.Fatalf("reminder calls after nudge: %d", reminders.Load())
	}

	// Catch-up is one rung per sweep, never a burst: a folio 36 days past
	// terms gets rung 1 on the next sweep, not all four at once.
	_, f3 := deskPartner67(t, s, tenantID, "Ancient Hotel", "+31695550103", "dn-c", 50*24*time.Hour)
	res, err = s.Folios.RunDunning(ctx, tenantID)
	if err != nil || res.Sent != 1 {
		t.Fatalf("catch-up sweep: %+v %v", res, err)
	}
	got3, _ := s.Folios.Get(ctx, tenantID, f3.ID)
	if len(got3.Reminders) != 1 || got3.Reminders[0].N != 1 {
		t.Fatalf("catch-up one rung: %+v", got3.Reminders)
	}

	// Only issued folios are dunned: OPEN is a conflict, SETTLED leaves
	// the working set entirely.
	open := seedPartner56(t, s, tenantID, "Open Hotel", 0)
	if _, err := s.Folios.SetBillToDesk(ctx, tenantID, open.ID, true); err != nil {
		t.Fatal(err)
	}
	confirmSourced66(t, s, tenantID, open.ID, "+31690000998", "dn-d")
	openFolio := folioFor66(t, s, tenantID, open.ID)[0]
	if _, err := s.Folios.RemindNow(ctx, tenantID, openFolio.ID); err != httpx.ErrConflict {
		t.Fatalf("remind open: %v", err)
	}
	if _, err := s.Folios.Settle(ctx, tenantID, f3.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Folios.RemindNow(ctx, tenantID, f3.ID); err != httpx.ErrConflict {
		t.Fatalf("remind settled: %v", err)
	}
	report, err := s.Folios.Aging(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range report.Rows {
		if row.PartnerName == "Ancient Hotel" {
			t.Fatalf("settled folio must leave aging: %+v", row)
		}
	}
}
