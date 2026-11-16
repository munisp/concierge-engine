//go:build pg

package store

// Postgres integration tests — Phase 48: settlement batch CRUD through RLS,
// idempotency-key conflict, the new payout columns (quote_expires_at,
// batch_id, settled_at) round-trip, ListPendingForPartner excluding batched
// lines, and tenant isolation. Gate: CE_PG_TEST_DSN. Requires
// db/migrations/0034_phase48.sql.

import (
	"context"
	"errors"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func pgPayout48(t *testing.T, s Store, tenant, partnerID, idem string) *model.Payout {
	t.Helper()
	p := &model.Payout{
		ID: newTestUUID(), TenantID: tenant, PartnerID: partnerID, BookingID: newTestUUID(),
		AmountMinor: 10000, Currency: "EUR", Rail: "MOJALOOP", State: model.PayoutPending,
		IdempotencyKey: idem, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.Payouts().Create(context.Background(), p); err != nil {
		t.Fatalf("payout create: %v", err)
	}
	return p
}

func TestPGSettlementBatches48(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-settle")
	other := pgTenant(t, s, "pg-settle-other")

	partner := &model.Partner{ID: newTestUUID(), TenantID: tenant, Name: "Safari Wings",
		Status: "VETTED", PayoutRail: "MOJALOOP", PayoutRef: "fsiop-safari"}
	if err := s.Partners().Create(ctx, partner); err != nil {
		t.Fatalf("partner: %v", err)
	}

	// Create + Get round-trip.
	b := &model.SettlementBatch{
		ID: newTestUUID(), TenantID: tenant, PartnerID: partner.ID, Currency: "EUR",
		AmountMinor: 30000, State: model.BatchOpen, IdempotencyKey: "pg-asm-1",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.Settlements().Create(ctx, b); err != nil {
		t.Fatalf("batch create: %v", err)
	}
	got, err := s.Settlements().Get(ctx, tenant, b.ID)
	if err != nil {
		t.Fatalf("batch get: %v", err)
	}
	if got.State != model.BatchOpen || got.AmountMinor != 30000 || got.Currency != "EUR" {
		t.Fatalf("batch round-trip: %+v", got)
	}

	// Idempotency conflict + replay path.
	dup := &model.SettlementBatch{
		ID: newTestUUID(), TenantID: tenant, PartnerID: partner.ID, Currency: "EUR",
		AmountMinor: 999, State: model.BatchOpen, IdempotencyKey: "pg-asm-1",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.Settlements().Create(ctx, dup); !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("dup idem: %v", err)
	}
	byIdem, err := s.Settlements().GetByIdem(ctx, tenant, "pg-asm-1")
	if err != nil || byIdem.ID != b.ID {
		t.Fatalf("get by idem: %v %+v", err, byIdem)
	}

	// Update: quote + transfer + expiry round-trip.
	expiry := time.Now().UTC().Add(15 * time.Minute).Truncate(time.Microsecond)
	b.State = model.BatchInFlight
	b.QuoteID = "q-1"
	b.TransferID = "tr-1"
	b.ReceiveMinor = 28500
	b.FeesMinor = 300
	b.QuoteExpiresAt = expiry
	b.UpdatedAt = time.Now().UTC()
	if err := s.Settlements().Update(ctx, b); err != nil {
		t.Fatalf("batch update: %v", err)
	}
	got, err = s.Settlements().GetByTransfer(ctx, tenant, "tr-1")
	if err != nil {
		t.Fatalf("get by transfer: %v", err)
	}
	if got.ID != b.ID || got.QuoteID != "q-1" || got.ReceiveMinor != 28500 || got.FeesMinor != 300 {
		t.Fatalf("batch update round-trip: %+v", got)
	}
	if got.QuoteExpiresAt.IsZero() {
		t.Fatal("quote_expires_at not persisted")
	}

	// List + tenant isolation.
	list, err := s.Settlements().List(ctx, tenant)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	if list, err := s.Settlements().List(ctx, other); err != nil || len(list) != 0 {
		t.Fatalf("cross-tenant list: %v %d", err, len(list))
	}
	if _, err := s.Settlements().Get(ctx, other, b.ID); !errors.Is(err, httpx.ErrNotFound) {
		t.Fatalf("cross-tenant get: %v", err)
	}
}

func TestPGPayoutBatchColumns48(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-pcol")

	partner := &model.Partner{ID: newTestUUID(), TenantID: tenant, Name: "Col Partner",
		Status: "VETTED", PayoutRail: "MOJALOOP", PayoutRef: "fsiop-col"}
	if err := s.Partners().Create(ctx, partner); err != nil {
		t.Fatalf("partner: %v", err)
	}
	b := &model.SettlementBatch{
		ID: newTestUUID(), TenantID: tenant, PartnerID: partner.ID, Currency: "EUR",
		AmountMinor: 20000, State: model.BatchOpen, IdempotencyKey: "pg-asm-c",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.Settlements().Create(ctx, b); err != nil {
		t.Fatalf("batch: %v", err)
	}

	p1 := pgPayout48(t, s, tenant, partner.ID, "pg-c1")
	p2 := pgPayout48(t, s, tenant, partner.ID, "pg-c2")

	// New columns round-trip through Update.
	expiry := time.Now().UTC().Add(10 * time.Minute)
	settled := time.Now().UTC()
	p1.BatchID = b.ID
	p1.QuoteExpiresAt = expiry
	p1.SettledAt = settled
	p1.State = model.PayoutSettled
	p1.UpdatedAt = time.Now().UTC()
	if err := s.Payouts().Update(ctx, p1); err != nil {
		t.Fatalf("payout update: %v", err)
	}
	got, err := s.Payouts().Get(ctx, tenant, p1.ID)
	if err != nil {
		t.Fatalf("payout get: %v", err)
	}
	if got.BatchID != b.ID || got.QuoteExpiresAt.IsZero() || got.SettledAt.IsZero() {
		t.Fatalf("new columns: %+v", got)
	}

	// ListPendingForPartner excludes the batched line.
	pending, err := s.Payouts().ListPendingForPartner(ctx, tenant, partner.ID)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != p2.ID {
		t.Fatalf("pending: %+v", pending)
	}

	// ListByBatch returns the member line.
	lines, err := s.Payouts().ListByBatch(ctx, tenant, b.ID)
	if err != nil || len(lines) != 1 || lines[0].ID != p1.ID {
		t.Fatalf("by batch: %v %+v", err, lines)
	}
}
