//go:build pg

// Postgres integration tests — Phase 35: scheduled outreach sends. The
// scheduled_for column round-trips, the due-index exists, and
// UpdateSendResult only mutates rows still in "scheduled" status (the
// sweep's guard against double-delivery). Gate: CE_PG_TEST_DSN.
package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPGScheduledSendRoundTrip(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-sched-send")

	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Scheduled Client",
		KYCStatus: "none", Consent: map[string]any{}}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatalf("client: %v", err)
	}

	when := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	o := &model.OutreachSend{
		ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		Channel: "whatsapp", Purpose: "marketing", Body: "scheduled hello",
		Status: "scheduled", Detail: "scheduled for later",
		IdempotencyKey: "pg-sched-1", ScheduledFor: &when,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.Outreach().AddSend(ctx, o); err != nil {
		t.Fatalf("add send: %v", err)
	}

	// Round-trip: scheduled_for survives the row.
	sends, err := s.Outreach().ListSends(ctx, tenant, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sends) != 1 || sends[0].ScheduledFor == nil || !sends[0].ScheduledFor.Equal(when) {
		t.Fatalf("scheduled_for roundtrip: %+v", sends)
	}
	if sends[0].Status != "scheduled" {
		t.Fatalf("status roundtrip: %+v", sends[0])
	}

	// UpdateSendResult mutates a scheduled row.
	if err := s.Outreach().UpdateSendResult(ctx, tenant, o.ID, "sent", "delivered by sweep", "wamid.pg-1"); err != nil {
		t.Fatalf("update result: %v", err)
	}
	sends, _ = s.Outreach().ListSends(ctx, tenant, 10)
	if sends[0].Status != "sent" || sends[0].ExternalRef != "wamid.pg-1" {
		t.Fatalf("post-update row: %+v", sends[0])
	}

	// The guard: a non-scheduled row refuses the transition (ErrNoRows).
	if err := s.Outreach().UpdateSendResult(ctx, tenant, o.ID, "failed", "late failure", ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("guard should refuse non-scheduled rows: %v", err)
	}
	sends, _ = s.Outreach().ListSends(ctx, tenant, 10)
	if sends[0].Status != "sent" {
		t.Fatalf("guarded row must stay sent: %+v", sends[0])
	}

	// Idempotent replay of the scheduled insert: same key surfaces
	// ErrConflict and adds no row.
	replay := &model.OutreachSend{
		ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		Channel: "whatsapp", Purpose: "marketing", Body: "scheduled hello",
		Status: "scheduled", IdempotencyKey: "pg-sched-1", ScheduledFor: &when,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.Outreach().AddSend(ctx, replay); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("replay should surface ErrConflict: %v", err)
	}
	sends, _ = s.Outreach().ListSends(ctx, tenant, 10)
	if len(sends) != 1 {
		t.Fatalf("replay must not add rows: %d", len(sends))
	}
}
