//go:build pg

// Postgres integration tests — Phase 39: outreach_sends.subject round-trips
// through the row, and per-channel sends coexist on the ledger the
// frequency cap reads. Gate: CE_PG_TEST_DSN.
package store

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPGOutreachEmailChannel39(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-email-chan")

	c := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Email Target",
		Phone: "+971500000199", Email: "vp.travel@acme.example",
		KYCStatus: "VERIFIED", Consent: map[string]any{"marketing": true}}
	if err := s.Clients().Upsert(ctx, c); err != nil {
		t.Fatalf("client: %v", err)
	}

	mk := func(idem, channel, subject string) *model.OutreachSend {
		return &model.OutreachSend{
			ID: newTestUUID(), TenantID: tenant, ClientID: c.ID,
			Channel: channel, Purpose: model.OutreachMarketing, Body: "offer",
			Subject: subject, Status: "sent", IdempotencyKey: "p39-" + idem,
			CreatedAt: time.Now().UTC(),
		}
	}
	if err := s.Outreach().AddSend(ctx, mk("em1", "email", "F1 transfers")); err != nil {
		t.Fatalf("email send: %v", err)
	}
	if err := s.Outreach().AddSend(ctx, mk("wa1", "whatsapp", "")); err != nil {
		t.Fatalf("wa send: %v", err)
	}

	sends, err := s.Outreach().ListSends(ctx, tenant, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var em *model.OutreachSend
	for i := range sends {
		if sends[i].Channel == "email" {
			em = &sends[i]
		}
	}
	if em == nil || em.Subject != "F1 transfers" {
		t.Fatalf("subject roundtrip: %+v", sends)
	}

	// Idempotent replay on (tenant, idempotency_key) still conflicts.
	if err := s.Outreach().AddSend(ctx, mk("em1", "email", "dup")); err != httpx.ErrConflict {
		t.Fatalf("replay: %v", err)
	}
}
