//go:build pg

package store

// Postgres integration tests — Phase 57: partners.phone column from
// migration 0037 round-trips; empty stays empty (portal-link-only partner).

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGPartnerPhone57(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-partner57")

	p := &model.Partner{
		ID: newTestUUID(), TenantID: tenant, Name: "Mr Travel",
		Verticals: []string{"TOURS"}, Status: "VETTED", PayoutRail: "MANUAL",
		Phone: "+2348012345678", CreatedAt: time.Now().UTC(),
	}
	if err := s.Partners().Create(ctx, p); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.Partners().Get(ctx, tenant, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phone != "+2348012345678" {
		t.Fatalf("phone round-trip: %q", got.Phone)
	}
	got.Phone = "+351912345678"
	if err := s.Partners().Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, err := s.Partners().Get(ctx, tenant, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Phone != "+351912345678" {
		t.Fatalf("phone update: %q", again.Phone)
	}
}
