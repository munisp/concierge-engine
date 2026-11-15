//go:build pg

// Postgres integration tests — Phase 44: consent_events round-trips
// through RLS, append-only ordering, tenant isolation. Gate:
// CE_PG_TEST_DSN. Requires db/migrations/0031_phase44.sql applied.
package store

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGConsentEvents44(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-consent-ev")

	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Evidence Client",
		Phone: "+15550001010", KYCStatus: "none",
		Consent: map[string]any{"marketing": true, "service": true, "data_processing": true}}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatalf("client: %v", err)
	}

	base := time.Now().UTC()
	events := []*model.ConsentEvent{
		{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID, Scope: "marketing",
			Granted: true, Source: model.ConsentSourceRegistrationDefault,
			Detail: "jurisdiction default at registration", CreatedAt: base},
		{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID, Scope: "marketing",
			Granted: false, Source: model.ConsentSourceKeyword,
			Detail: "client texted 'stop'", CreatedAt: base.Add(time.Minute)},
		{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID, Scope: "marketing",
			Granted: true, Source: model.ConsentSourceReopt,
			Detail: "reopt/ link", CreatedAt: base.Add(2 * time.Minute)},
	}
	for _, e := range events {
		if err := s.ConsentEvents().Record(ctx, e); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	// Newest first, all three present, fields round-trip.
	rows, err := s.ConsentEvents().ListForClient(ctx, tenant, cl.ID, 10)
	if err != nil || len(rows) != 3 {
		t.Fatalf("list: %d %v", len(rows), err)
	}
	if rows[0].Source != model.ConsentSourceReopt || !rows[0].Granted {
		t.Fatalf("order/fields: %+v", rows[0])
	}
	if rows[2].Detail != "jurisdiction default at registration" {
		t.Fatalf("detail round-trip: %+v", rows[2])
	}

	// Limit is honored.
	if rows, err := s.ConsentEvents().ListForClient(ctx, tenant, cl.ID, 1); err != nil || len(rows) != 1 {
		t.Fatalf("limit: %d %v", len(rows), err)
	}

	// Tenant isolation: a second tenant sees nothing, and another client
	// of the same tenant sees nothing.
	other := pgTenant(t, s, "pg-consent-ev-b")
	if rows, _ := s.ConsentEvents().ListForClient(ctx, other, cl.ID, 10); len(rows) != 0 {
		t.Fatalf("RLS leak (tenant): %d rows", len(rows))
	}
	cl2 := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Other Client",
		KYCStatus: "none", Consent: map[string]any{}}
	if err := s.Clients().Upsert(ctx, cl2); err != nil {
		t.Fatalf("client2: %v", err)
	}
	if rows, err := s.ConsentEvents().ListForClient(ctx, tenant, cl2.ID, 10); err != nil || len(rows) != 0 {
		t.Fatalf("client scoping: %d %v", len(rows), err)
	}
}
