// Phase 12 domain tests: receipt -> case correlation and SLA alerting.
// A failed delivery on an approved reply means the client never received
// it while the SLA clock runs — the case must alert, escalate, and leave
// the waiting-on-client state. A read is evidence the client saw it.
package domain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// sendReplyLinked spins up a fake WhatsApp adapter that returns
// wamid.reply-1, sends an approved reply, and returns the case (now linked
// to the ref via SendReply) in WaitingClient state.
func sendReplyLinked(t *testing.T, s *Services, tenantID string) string {
	t.Helper()
	ctx := context.Background()
	rr, err := s.Clients.Resolve(ctx, tenantID, "+971501234567", "", "Ada Vip")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := s.Conversations.Open(ctx, tenantID, rr.Client.ID, "WHATSAPP", "wamid.1")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Cases.Open(ctx, tenantID, rr.Client.ID, "Late chauffeur",
		"driver 40 minutes late, refund please", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cases.Link(ctx, tenantID, c.ID, "", "", conv.ID); err != nil {
		t.Fatal(err)
	}
	res, err := s.Cases.SendReply(ctx, tenantID, c.ID, "Replacement en route.", "op-1", "rules")
	if err != nil || !res.Sent || res.ExternalRef != "wamid.reply-1" {
		t.Fatalf("send: %+v, %v", res, err)
	}
	return c.ID
}

func waStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"mode": "freeform",
			"body": map[string]any{"messages": []map[string]string{{"id": "wamid.reply-1"}}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestReceiptFailedAlertsLinkedCase(t *testing.T) {
	s := testServices(t, Deps{WhatsAppURL: waStub(t).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	caseID := sendReplyLinked(t, s, tenantID)

	// Operator believes the client was told; case waits on the client.
	if _, err := s.Cases.Assign(ctx, tenantID, caseID, "op-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cases.Transition(ctx, tenantID, caseID, model.CaseInProgress); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cases.Transition(ctx, tenantID, caseID, model.CaseWaitingClient); err != nil {
		t.Fatal(err)
	}

	// The channel reports the reply never arrived.
	if _, err := s.Receipts.Record(ctx, tenantID, "whatsapp", "wamid.reply-1", "failed", time.Time{}); err != nil {
		t.Fatal(err)
	}

	c, err := s.Cases.Get(ctx, tenantID, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != model.CaseInProgress {
		t.Fatalf("failed delivery must pull the case out of waiting-on-client, got %s", c.State)
	}
	if !c.Escalated {
		t.Fatal("failed delivery under SLA must escalate")
	}
	notes, err := s.Cases.Notes(ctx, tenantID, caseID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range notes {
		if strings.Contains(n.Body, "DELIVERY FAILED") && strings.Contains(n.Body, "wamid.reply-1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("case notes must carry the failure evidence: %+v", notes)
	}
}

func TestReceiptReadAnnotatesCase(t *testing.T) {
	s := testServices(t, Deps{WhatsAppURL: waStub(t).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	caseID := sendReplyLinked(t, s, tenantID)

	if _, err := s.Receipts.Record(ctx, tenantID, "whatsapp", "wamid.reply-1", "read", time.Time{}); err != nil {
		t.Fatal(err)
	}
	notes, err := s.Cases.Notes(ctx, tenantID, caseID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range notes {
		if strings.Contains(n.Body, "client read the reply") {
			found = true
		}
	}
	if !found {
		t.Fatalf("read receipt must annotate the case: %+v", notes)
	}
	// Read is evidence, not an escalation.
	c, _ := s.Cases.Get(ctx, tenantID, caseID)
	if c.Escalated {
		t.Fatal("a read receipt must not escalate the case")
	}
}

func TestReceiptUnlinkedRefNeverAlerts(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	// Template sends (booking confirmations etc.) have no case link —
	// their receipts are stored evidence but must not touch any case.
	if _, err := s.Receipts.Record(ctx, tenantID, "whatsapp", "wamid.template-1", "failed", time.Time{}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Receipts.Failures(ctx, tenantID, 10)
	if err != nil || len(rows) != 1 || rows[0].CaseID != "" {
		t.Fatalf("unlinked failure: %+v, %v", rows, err)
	}
}

func TestReceiptLinkFirstWinsAndCaseTimeline(t *testing.T) {
	s := testServices(t, Deps{WhatsAppURL: waStub(t).URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	caseID := sendReplyLinked(t, s, tenantID)

	// A replayed/duplicate link attempt must never re-point the evidence.
	if err := s.Receipts.Link(ctx, tenantID, "wamid.reply-1", "case-OTHER"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Receipts.Record(ctx, tenantID, "whatsapp", "wamid.reply-1", "delivered", time.Time{}); err != nil {
		t.Fatal(err)
	}

	tl, err := s.Receipts.ForCase(ctx, tenantID, caseID)
	if err != nil || tl.CaseID != caseID {
		t.Fatalf("timeline: %+v, %v", tl, err)
	}
	rcs, ok := tl.Messages["wamid.reply-1"]
	if !ok || len(rcs) != 1 || rcs[0].Status != "delivered" {
		t.Fatalf("timeline must carry the linked ref's receipts: %+v", tl.Messages)
	}
	// And the failure board must still attribute the ref to the real case.
	if _, err := s.Receipts.Record(ctx, tenantID, "whatsapp", "wamid.reply-1", "failed", time.Time{}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Receipts.Failures(ctx, tenantID, 10)
	if err != nil || len(rows) != 1 || rows[0].CaseID != caseID {
		t.Fatalf("failures board: %+v, %v", rows, err)
	}
}

func TestReceiptFailuresNewestFirst(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	old := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

	s.Receipts.Record(ctx, tenantID, "whatsapp", "wamid.old", "failed", old)
	s.Receipts.Record(ctx, tenantID, "whatsapp", "wamid.new", "failed", old.Add(time.Hour))

	rows, err := s.Receipts.Failures(ctx, tenantID, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("failures: %+v, %v", rows, err)
	}
	if rows[0].Receipt.ExternalRef != "wamid.new" {
		t.Fatalf("board must be newest-first: %+v", rows)
	}
}
