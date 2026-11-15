//go:build pg

// Postgres integration tests — Phase 36: leads. The partial-unique
// idempotency index (WHERE idempotency_key <> ”) conflicts on keyed
// replays while unkeyed creates always insert; the 30-column row
// round-trips including jsonb briefs and nullable links. Gate:
// CE_PG_TEST_DSN.
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

func TestPGLeadRoundTrip(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-leads")

	when := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	l := &model.Lead{
		ID: newTestUUID(), TenantID: tenant, Kind: model.LeadKindEvent,
		Name: "Gala Planner", Phone: "+971501110090", Email: "gala@example.com",
		EventType: "gala", EventDate: &when, Headcount: 120, Venue: "Atlantis",
		Source: "site", Channel: "seo", Campaign: "launch",
		Status: model.LeadStatusNew, Score: 80, ScoreReasons: []string{"+20 event brief"},
		ConsentMarketing: true,
		CreatedAt:        time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.Leads().Create(ctx, l); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Full round-trip: jsonb briefs, event date and nullable links survive.
	got, err := s.Leads().Get(ctx, tenant, l.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Kind != model.LeadKindEvent || got.Headcount != 120 || got.Venue != "Atlantis" {
		t.Fatalf("roundtrip: %+v", got)
	}
	if got.EventDate == nil || !got.EventDate.Equal(when) {
		t.Fatalf("event_date roundtrip: %+v", got.EventDate)
	}
	if len(got.ScoreReasons) != 1 || got.ScoreReasons[0] != "+20 event brief" {
		t.Fatalf("score_reasons roundtrip: %+v", got.ScoreReasons)
	}
	if got.SLAAlertedAt != nil || got.ConvertedClientID != "" {
		t.Fatalf("nullables: %+v", got)
	}
}

func TestPGLeadIdempotency(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-leads-idem")

	mk := func(id, key, phone string) *model.Lead {
		return &model.Lead{
			ID: id, TenantID: tenant, Kind: model.LeadKindPerson,
			Name: "Idem", Phone: phone, Source: "api", Status: model.LeadStatusNew,
			IdempotencyKey: key, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
	}
	if err := s.Leads().Create(ctx, mk(newTestUUID(), "pg-lead-key-1", "+971501110091")); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Keyed replay: partial-unique index conflicts.
	err := s.Leads().Create(ctx, mk(newTestUUID(), "pg-lead-key-1", "+971501110091"))
	if !errors.Is(err, httpx.ErrConflict) {
		t.Fatalf("keyed replay: %v", err)
	}
	// Unkeyed creates always insert — even the same phone twice (a repeat
	// inquiry is signal; dedup is the domain layer's decision).
	if err := s.Leads().Create(ctx, mk(newTestUUID(), "", "+971501110091")); err != nil {
		t.Fatalf("unkeyed: %v", err)
	}
	if err := s.Leads().Create(ctx, mk(newTestUUID(), "", "+971501110091")); err != nil {
		t.Fatalf("unkeyed 2: %v", err)
	}
	leads, err := s.Leads().List(ctx, tenant, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(leads) != 3 {
		t.Fatalf("want 3 leads, got %d", len(leads))
	}
	// Newest first.
	if !leads[0].CreatedAt.After(leads[2].CreatedAt) && !leads[0].CreatedAt.Equal(leads[2].CreatedAt) {
		t.Fatalf("ordering: %+v", leads)
	}
}

func TestPGLeadUpdateAndSLAIndex(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-leads-upd")

	l := &model.Lead{
		ID: newTestUUID(), TenantID: tenant, Kind: model.LeadKindCorporate,
		Name: "Corp", Phone: "+971501110092", Company: "Initech",
		Source: "api", Status: model.LeadStatusNew,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.Leads().Create(ctx, l); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Transition + SLA stamp + conversion link all mutate through Update.
	now := time.Now().UTC().Truncate(time.Second)
	l.Status = model.LeadStatusConverted
	l.SLAAlertedAt = &now
	l.ConvertedAccountID = newTestUUID()
	l.UpdatedAt = now
	if err := s.Leads().Update(ctx, l); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := s.Leads().Get(ctx, tenant, l.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != model.LeadStatusConverted || got.ConvertedAccountID != l.ConvertedAccountID {
		t.Fatalf("update roundtrip: %+v", got)
	}
	if got.SLAAlertedAt == nil || !got.SLAAlertedAt.Equal(now) {
		t.Fatalf("sla_alerted_at roundtrip: %+v", got.SLAAlertedAt)
	}
	// Update of a missing id: not found.
	l.ID = newTestUUID()
	if err := s.Leads().Update(ctx, l); !errors.Is(err, httpx.ErrNotFound) {
		t.Fatalf("missing update: %v", err)
	}
	// Get of a missing id: not found.
	if _, err := s.Leads().Get(ctx, tenant, l.ID); !errors.Is(err, httpx.ErrNotFound) {
		t.Fatalf("missing get: %v", err)
	}

	// The SLA partial index exists (sweep scans only NEW + un-alerted).
	var idx string
	err = s.(*postgres).db.QueryRowContext(ctx,
		`SELECT indexname FROM pg_indexes WHERE indexname = 'idx_leads_sla'`).Scan(&idx)
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatal("idx_leads_sla missing")
	}
	if err != nil {
		t.Fatalf("index check: %v", err)
	}
}
