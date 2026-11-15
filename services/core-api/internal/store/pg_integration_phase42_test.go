//go:build pg

// Postgres integration tests — Phase 42: SendByExternalRef correlates a
// channel message id (wamid) back to its send through RLS, backed by
// idx_outreach_sends_ref (migration 0030). Gate: CE_PG_TEST_DSN.
package store

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPGSendByExternalRef42(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-wahygiene")

	// A real client is required (outreach_sends references clients).
	cl := &model.Client{
		ID: newTestUUID(), TenantID: tenant, Phone: "+971500000042",
		FullName: "WA Client", CreatedAt: time.Now().UTC(),
	}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatalf("client: %v", err)
	}

	send := &model.OutreachSend{
		ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		Channel: "whatsapp", Purpose: "marketing", Status: "sent",
		Body: "offer", ExternalRef: "wamid.pg42-1",
		IdempotencyKey: "pg42-1", CreatedAt: time.Now().UTC(),
	}
	if err := s.Outreach().AddSend(ctx, send); err != nil {
		t.Fatalf("add send: %v", err)
	}

	got, err := s.Outreach().SendByExternalRef(ctx, tenant, "wamid.pg42-1")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.ID != send.ID || got.ClientID != cl.ID {
		t.Fatalf("roundtrip: %+v", got)
	}

	// Unknown ref → ErrNotFound (a case reply's wamid, for instance).
	if _, err := s.Outreach().SendByExternalRef(ctx, tenant, "wamid.nope"); err != httpx.ErrNotFound {
		t.Fatalf("unknown ref: %v", err)
	}

	// Tenant isolation: the same ref under another tenant finds nothing.
	other := pgTenant(t, s, "pg-wahygiene-b")
	if _, err := s.Outreach().SendByExternalRef(ctx, other, "wamid.pg42-1"); err != httpx.ErrNotFound {
		t.Fatalf("RLS leak: %v", err)
	}
}
