//go:build pg

// Postgres integration tests — Phase 38: lead provenance columns
// (list_source, contact_basis) round-trip through the 32-column row, the
// basis CHECK constraint bites on unknown values, and the basis index
// exists. Gate: CE_PG_TEST_DSN.
package store

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGLeadProvenance38(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-leads-prov")

	l := &model.Lead{
		ID: newTestUUID(), TenantID: tenant, Kind: model.LeadKindCorporate,
		Name: "Buyer", Phone: "+447700900030", Company: "Apex Racing",
		Source: "import", ListSource: "cognism-q1-2027",
		ContactBasis: model.ContactBasisB2B,
		Status:       model.LeadStatusNew,
		CreatedAt:    time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.Leads().Create(ctx, l); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.Leads().Get(ctx, tenant, l.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ListSource != "cognism-q1-2027" || got.ContactBasis != model.ContactBasisB2B {
		t.Fatalf("provenance roundtrip: %+v", got)
	}

	// Update path carries provenance too (consent earned on a repeat touch).
	l.ConsentMarketing = true
	l.UpdatedAt = time.Now().UTC().Truncate(time.Second)
	if err := s.Leads().Update(ctx, l); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = s.Leads().Get(ctx, tenant, l.ID)
	if err != nil {
		t.Fatalf("get 2: %v", err)
	}
	if !got.ConsentMarketing || got.ContactBasis != model.ContactBasisB2B {
		t.Fatalf("update roundtrip: %+v", got)
	}

	// The CHECK refuses a basis the domain would never write.
	bad := &model.Lead{
		ID: newTestUUID(), TenantID: tenant, Kind: model.LeadKindPerson,
		Name: "Bad", Phone: "+447700900031", Source: "import",
		ContactBasis: "telepathy", Status: model.LeadStatusNew,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.Leads().Create(ctx, bad); err == nil {
		t.Fatal("unknown basis should violate the CHECK")
	}

	// The basis index exists (segment join / list filtering).
	var idx string
	err = s.(*postgres).db.QueryRowContext(ctx,
		`SELECT indexname FROM pg_indexes WHERE indexname = 'idx_leads_basis'`).Scan(&idx)
	if err != nil {
		t.Fatalf("idx_leads_basis missing: %v", err)
	}
}
