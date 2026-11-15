//go:build pg

// Postgres integration tests — Phase 41: email_suppressions round-trips
// through RLS with UNIQUE (tenant_id, email) replay semantics and operator
// delete. Gate: CE_PG_TEST_DSN. Requires db/migrations/0029_phase41.sql
// applied.
package store

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPGEmailSuppressions41(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-suppress")

	row := &model.EmailSuppression{
		ID: newTestUUID(), TenantID: tenant, Email: "dead@acme.example",
		Reason: model.SuppressHardBounce, SourceRef: "<msg-1@esp>",
		CreatedAt: time.Now().UTC(),
	}
	if err := s.EmailSuppressions().Add(ctx, row); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Replay conflicts (the domain reloads the original).
	dup := *row
	dup.ID = newTestUUID()
	if err := s.EmailSuppressions().Add(ctx, &dup); err != httpx.ErrConflict {
		t.Fatalf("replay: %v", err)
	}

	exists, err := s.EmailSuppressions().Exists(ctx, tenant, "dead@acme.example")
	if err != nil || !exists {
		t.Fatalf("exists: %v %v", exists, err)
	}
	if exists, _ := s.EmailSuppressions().Exists(ctx, tenant, "alive@acme.example"); exists {
		t.Fatal("exists false positive")
	}

	second := &model.EmailSuppression{
		ID: newTestUUID(), TenantID: tenant, Email: "complainer@acme.example",
		Reason: model.SuppressComplaint, CreatedAt: time.Now().UTC().Add(time.Second),
	}
	if err := s.EmailSuppressions().Add(ctx, second); err != nil {
		t.Fatalf("add second: %v", err)
	}
	rows, err := s.EmailSuppressions().List(ctx, tenant, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("list: %d %v", len(rows), err)
	}
	if rows[0].Email != "complainer@acme.example" { // newest first
		t.Fatalf("list order: %+v", rows)
	}

	// Tenant isolation.
	other := pgTenant(t, s, "pg-suppress-b")
	if exists, _ := s.EmailSuppressions().Exists(ctx, other, "dead@acme.example"); exists {
		t.Fatal("RLS leak: other tenant sees the suppression")
	}
	if rows, _ := s.EmailSuppressions().List(ctx, other, 10); len(rows) != 0 {
		t.Fatalf("RLS leak: %d rows", len(rows))
	}

	// Delete: removes, then 404 on the missing row.
	if err := s.EmailSuppressions().Delete(ctx, tenant, "dead@acme.example"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.EmailSuppressions().Delete(ctx, tenant, "dead@acme.example"); err != httpx.ErrNotFound {
		t.Fatalf("delete missing: %v", err)
	}
	if exists, _ := s.EmailSuppressions().Exists(ctx, tenant, "dead@acme.example"); exists {
		t.Fatal("delete did not remove")
	}
}
