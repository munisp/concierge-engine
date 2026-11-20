package domain

// Phase 71: statement delivery — the stmt/ capability token, the
// derived partner-facing statement view, channel-picked delivery
// (WhatsApp first, email fallback) with the verbatim deliveries
// ledger, and the ISSUED-only guard.

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

func TestStatementToken71(t *testing.T) {
	s := testServices(t, Deps{})
	tok := s.Trips.StatementTokenFor("t1", "f1")
	tenantID, folioID, err := s.Trips.ParseStatementToken(tok)
	if err != nil || tenantID != "t1" || folioID != "f1" {
		t.Fatalf("roundtrip: %q %q %v", tenantID, folioID, err)
	}
	// Cross-family rejection: a consent or tracking link can never open
	// a financial statement.
	if _, _, err := s.Trips.ParseStatementToken(s.Trips.ConsentTokenFor("t1", "c1")); err != httpx.ErrForbidden {
		t.Fatalf("consent token must be rejected: %v", err)
	}
	if _, _, err := s.Trips.ParseStatementToken(s.Trips.TokenFor("t1", "a1")); err != httpx.ErrForbidden {
		t.Fatalf("track token must be rejected: %v", err)
	}
	if _, _, err := s.Trips.ParseStatementToken("garbage"); err == nil {
		t.Fatal("garbage token must be rejected")
	}
}

// adapterSink71 records request bodies that contain marker and replies
// with the empty success envelope (postToAdapter maps it to "sent").
func adapterSink71(marker string, bodies *[]string, mu *sync.Mutex) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if marker == "" || strings.Contains(string(b), marker) {
			mu.Lock()
			*bodies = append(*bodies, string(b))
			mu.Unlock()
		}
		_, _ = w.Write([]byte(`{}`))
	}))
}

func TestSendStatementWhatsApp71(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	adapter := adapterSink71("Statement ", &bodies, &mu)
	defer adapter.Close()

	s := testServices(t, Deps{WhatsAppURL: adapter.URL, PublicBase: "https://api.example.com"})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	p, f := deskPartner67(t, s, tenantID, "Deliverable Hotel", "+31695557080", "st-a", 2*24*time.Hour)

	got, err := s.Folios.SendStatement(ctx, tenantID, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Deliveries) != 1 {
		t.Fatalf("deliveries ledger: %+v", got.Deliveries)
	}
	d := got.Deliveries[0]
	if d.Kind != "statement" || d.Channel != "whatsapp" || d.Status != "sent" ||
		d.ID == "" || d.CreatedAt.IsZero() {
		t.Fatalf("delivery row: %+v", d)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("adapter calls: %v", bodies)
	}
	body := bodies[0]
	wantURL := s.Folios.StatementURL(tenantID, f.ID)
	if !strings.Contains(body, p.Phone) || !strings.Contains(body, "90.00 EUR") ||
		!strings.Contains(body, "/v1/statement/") || !strings.Contains(body, wantURL[strings.LastIndex(wantURL, "/"):]) {
		t.Fatalf("statement body: %s", body)
	}
}

func TestSendStatementEmailFallback71(t *testing.T) {
	// No phone on file: the statement goes to the billing email.
	var mu sync.Mutex
	var bodies []string
	adapter := adapterSink71("", &bodies, &mu)
	defer adapter.Close()

	s := testServices(t, Deps{EmailURL: adapter.URL, PublicBase: "https://api.example.com"})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	p, f := deskPartner67(t, s, tenantID, "Email Desk Hotel", "", "st-b", 2*24*time.Hour)
	p.Email = "accounts@emaildesk.example.com"
	if err := s.Store.Partners().Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	// Half paid: the email quotes the outstanding balance.
	if _, err := s.Folios.RecordPayment(ctx, tenantID, f.ID, 4000, "bank_transfer", "T-1"); err != nil {
		t.Fatal(err)
	}

	got, err := s.Folios.SendStatement(ctx, tenantID, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	d := got.Deliveries[0]
	if d.Channel != "email" || d.Status != "sent" {
		t.Fatalf("delivery row: %+v", d)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("adapter calls: %v", bodies)
	}
	body := bodies[0]
	if !strings.Contains(body, "accounts@emaildesk.example.com") ||
		!strings.Contains(body, "subject") || !strings.Contains(body, "50.00 EUR") ||
		strings.Contains(body, "whatsapp") {
		t.Fatalf("email payload: %s", body)
	}
}

func TestSendStatementSkips71(t *testing.T) {
	s := testServices(t, Deps{}) // no adapters configured
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	// No contact at all — recorded verbatim, never faked.
	_, f := deskPartner67(t, s, tenantID, "Ghost Hotel", "", "st-c", 2*24*time.Hour)
	got, err := s.Folios.SendStatement(ctx, tenantID, f.ID)
	if err != nil {
		t.Fatal(err) // a skip is a recorded outcome, not an error
	}
	if got.Deliveries[0].Status != "skipped:no-contact" || got.Deliveries[0].Channel != "none" {
		t.Fatalf("no-contact delivery: %+v", got.Deliveries[0])
	}

	// Phone but no adapter — honest skip, channel says what was attempted.
	_, f2 := deskPartner67(t, s, tenantID, "No Adapter Hotel", "+31695557081", "st-d", 2*24*time.Hour)
	got2, err := s.Folios.SendStatement(ctx, tenantID, f2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Deliveries[0].Status != "skipped:no-adapter" || got2.Deliveries[0].Channel != "whatsapp" {
		t.Fatalf("no-adapter delivery: %+v", got2.Deliveries[0])
	}
}

func TestSendStatementStateGuard71(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	// OPEN folio: still accruing — delivering it would freeze a moving
	// total in the partner's inbox.
	p := seedPartner56(t, s, tenantID, "Accruing Desk", 0)
	if _, err := s.Folios.SetBillToDesk(ctx, tenantID, p.ID, true); err != nil {
		t.Fatal(err)
	}
	confirmSourced66(t, s, tenantID, p.ID, "+31690000997", "st-e")
	open := folioFor66(t, s, tenantID, p.ID)[0]
	if _, err := s.Folios.SendStatement(ctx, tenantID, open.ID); err != httpx.ErrConflict {
		t.Fatalf("OPEN send: %v", err)
	}

	// SETTLED folio: terminal — no collection document needed.
	_, issued := deskPartner67(t, s, tenantID, "Settled Desk", "", "st-f", 2*24*time.Hour)
	if _, err := s.Folios.Settle(ctx, tenantID, issued.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Folios.SendStatement(ctx, tenantID, issued.ID); err != httpx.ErrConflict {
		t.Fatalf("SETTLED send: %v", err)
	}
}

func TestStatementByToken71(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	p, f := deskPartner67(t, s, tenantID, "Viewing Hotel", "", "st-g", 16*24*time.Hour) // 2d overdue
	if _, err := s.Folios.RecordPayment(ctx, tenantID, f.ID, 4000, "cash", ""); err != nil {
		t.Fatal(err)
	}

	v, err := s.Folios.StatementByToken(ctx, s.Trips.StatementTokenFor(tenantID, f.ID))
	if err != nil {
		t.Fatal(err)
	}
	if v.TenantName == "" || v.PartnerName != p.Name || v.Period != f.Period ||
		v.TotalMinor != 9000 || v.PaidMinor != 4000 || v.OutstandingMinor != 5000 {
		t.Fatalf("view: %+v", v)
	}
	if v.DueAt == nil || v.IssuedAt == nil {
		t.Fatalf("issued view must carry issued/due: %+v", v)
	}
	if want := f.IssuedAt.Add(time.Duration(v.TermsDays) * 24 * time.Hour); !v.DueAt.Equal(want) {
		t.Fatalf("due = issued + terms: got %v want %v", v.DueAt, want)
	}
	if v.OverdueDays != 2 {
		t.Fatalf("overdue derived: %d", v.OverdueDays)
	}
	if len(v.Lines) != 1 || len(v.Payments) != 1 {
		t.Fatalf("ledgers on the document: %+v", v)
	}

	// A wrong-family token never resolves a statement.
	if _, err := s.Folios.StatementByToken(ctx, s.Trips.PartnerTokenFor(tenantID, p.ID)); err != httpx.ErrForbidden {
		t.Fatalf("partner token must be rejected: %v", err)
	}

	// After settlement the saved link still resolves — the partner can
	// re-read the document marked PAID.
	if _, err := s.Folios.Settle(ctx, tenantID, f.ID); err != nil {
		t.Fatal(err)
	}
	v2, err := s.Folios.StatementByToken(ctx, s.Trips.StatementTokenFor(tenantID, f.ID))
	if err != nil {
		t.Fatal(err)
	}
	if v2.State != model.FolioSettled || v2.SettledAt == nil || v2.OutstandingMinor != 0 || v2.DueAt != nil {
		t.Fatalf("settled view: %+v", v2)
	}
}
