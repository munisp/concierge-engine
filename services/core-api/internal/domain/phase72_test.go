package domain

// Phase 72: dunning over the email lane — a phoneless partner with a
// billing email gets the same escalating cadence over email (channel
// recorded verbatim), and the honest skips name the lane attempted.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestDunningEmailLane72(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer adapter.Close()

	s := testServices(t, Deps{EmailURL: adapter.URL, PublicBase: "https://api.example.com"})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	// Phoneless partner, billing email on file; 15d since issue,
	// 14-day terms → rung 1 due.
	p, f := deskPartner67(t, s, tenantID, "Inbox Hotel", "", "dn-a", 15*24*time.Hour)
	p.Email = "accounts@inboxhotel.example.com"
	if err := s.Store.Partners().Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	// Half paid: the reminder quotes the outstanding balance.
	if _, err := s.Folios.RecordPayment(ctx, tenantID, f.ID, 5000, "cash", ""); err != nil {
		t.Fatal(err)
	}

	res, err := s.Folios.RunDunning(ctx, tenantID)
	if err != nil || res.Sent != 1 || res.Skipped != 0 {
		t.Fatalf("sweep: %+v %v", res, err)
	}
	got, err := s.Store.Folios().Get(ctx, tenantID, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Reminders) != 1 {
		t.Fatalf("reminder ledger: %+v", got.Reminders)
	}
	r := got.Reminders[0]
	if r.Channel != "email" || r.Status != model.FolioReminderSent {
		t.Fatalf("reminder row: %+v", r)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("adapter calls: %v", bodies)
	}
	body := bodies[0]
	if !strings.Contains(body, "accounts@inboxhotel.example.com") ||
		!strings.Contains(body, "subject") || !strings.Contains(body, "40.00 EUR") ||
		!strings.Contains(body, "/v1/statement/") {
		t.Fatalf("email reminder payload: %s", body)
	}
}

func TestDunningEmailSkips72(t *testing.T) {
	s := testServices(t, Deps{}) // no adapters at all
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	seedCatalog63(t, s, tenantID)

	// Email on file but no adapter: honest skip naming the email lane.
	p, f := deskPartner67(t, s, tenantID, "No Adapter Desk", "", "dn-b", 15*24*time.Hour)
	p.Email = "accounts@noadapter.example.com"
	if err := s.Store.Partners().Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	res, err := s.Folios.RunDunning(ctx, tenantID)
	if err != nil || res.Sent != 0 || res.Skipped != 1 {
		t.Fatalf("sweep: %+v %v", res, err)
	}
	got, _ := s.Store.Folios().Get(ctx, tenantID, f.ID)
	if got.Reminders[0].Channel != "email" || got.Reminders[0].Status != "skipped:no-adapter" {
		t.Fatalf("no-adapter row: %+v", got.Reminders[0])
	}

	// Neither phone nor email: the no-contact skip names channel none.
	_, f2 := deskPartner67(t, s, tenantID, "Ghost Desk", "", "dn-c", 15*24*time.Hour)
	res, err = s.Folios.RunDunning(ctx, tenantID)
	if err != nil || res.Sent != 0 || res.Skipped != 1 {
		t.Fatalf("sweep 2: %+v %v", res, err)
	}
	got2, _ := s.Store.Folios().Get(ctx, tenantID, f2.ID)
	if got2.Reminders[0].Channel != "none" || got2.Reminders[0].Status != "skipped:no-contact" {
		t.Fatalf("no-contact row: %+v", got2.Reminders[0])
	}
}
