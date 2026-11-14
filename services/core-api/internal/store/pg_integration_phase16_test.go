//go:build pg

// Postgres integration tests — Phase 16: erased_at roundtrip (NULL vs set),
// erasure persistence with bookings retained for the same pseudonymous id.
// Same gate as pg_integration_test.go (CE_PG_TEST_DSN).
package store

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGClientErasureRoundtrip(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-erasure")

	c := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "GDPR Subject",
		Email: "subject@example.eu", Phone: "+491599900002", KYCStatus: "VERIFIED",
		Consent: map[string]any{"marketing": false}, Locale: "de-DE", Timezone: "Europe/Berlin"}
	if err := s.Clients().Upsert(ctx, c); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.Clients().Get(ctx, tenant, c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Erased() {
		t.Fatal("fresh client must not be erased (erased_at must roundtrip NULL)")
	}

	// Erase: tombstone write.
	c.FullName = "Erased client"
	c.Email = "erased-" + c.ID + "@erased.invalid"
	c.Phone, c.Locale, c.Timezone = "", "", ""
	c.Consent = map[string]any{"erased": true}
	c.ErasedAt = time.Now().UTC().Truncate(time.Second)
	if err := s.Clients().Upsert(ctx, c); err != nil {
		t.Fatalf("erase upsert: %v", err)
	}
	got, err = s.Clients().Get(ctx, tenant, c.ID)
	if err != nil {
		t.Fatalf("get erased: %v", err)
	}
	if !got.Erased() || got.Phone != "" || got.FullName != "Erased client" {
		t.Fatalf("erasure did not persist: %+v", got)
	}

	// Financial history stays attached to the pseudonymous id (carve-out):
	// a booking created pre-erasure is still listed ForClient afterwards.
	req := &model.ServiceRequest{ID: newTestUUID(), TenantID: tenant, ClientID: c.ID,
		State: "NEW", Version: 1, IdempotencyKey: "pg-erasure-r1",
		Items:    []model.ServiceItem{{ID: newTestUUID(), TenantID: tenant, Vertical: "CHAUFFEUR_POINT_TO_POINT"}},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := s.Requests().Create(ctx, req); err != nil {
		t.Fatalf("request: %v", err)
	}
	bk := &model.Booking{ID: newTestUUID(), TenantID: tenant, RequestID: req.ID,
		State: "CONFIRMED", CreatedAt: time.Now().UTC()}
	if err := s.Bookings().Create(ctx, bk); err != nil {
		t.Fatalf("booking: %v", err)
	}
	list, err := s.Bookings().ForClient(ctx, tenant, c.ID, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("booking must survive erasure (accounting carve-out): %+v %v", list, err)
	}
}
