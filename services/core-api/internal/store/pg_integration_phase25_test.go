//go:build pg

// Postgres integration tests — Phase 25: LatestGeoStamp (client tracking
// feed), tenant lifecycle_config roundtrip, client List (lifecycle sweep
// source). Same gate as pg_integration_test.go.
package store

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGLatestGeoStamp(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-geolatest")
	other := pgTenant(t, s, "pg-geolatest-other")

	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Geo Client",
		Phone: "+971500000400", KYCStatus: "none", Consent: map[string]any{}}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatalf("client: %v", err)
	}
	bk := pgQuoteBornBooking(t, s, tenant, cl.ID, "pg-geo-b1")
	a := &model.Assignment{ID: newTestUUID(), TenantID: tenant, BookingID: bk,
		Status: "IN_TRIP", CreatedAt: time.Now().UTC()}
	if err := s.Dispatch().CreateAssignment(ctx, a); err != nil {
		t.Fatalf("assignment: %v", err)
	}

	// No stamps yet: clean not-found, never an invented position.
	if _, err := s.Dispatch().LatestGeoStamp(ctx, tenant, a.ID); err == nil {
		t.Fatal("LatestGeoStamp on stamp-less trip must be not-found")
	}

	first := &model.GeoStamp{ID: newTestUUID(), TenantID: tenant, AssignmentID: a.ID,
		Kind: "PING", Lat: 52.30, Lng: 4.80, AccuracyM: 20}
	if err := s.Dispatch().AddGeoStamp(ctx, first); err != nil {
		t.Fatalf("stamp1: %v", err)
	}
	time.Sleep(20 * time.Millisecond) // `at` column defaults to now(); keep order unambiguous
	second := &model.GeoStamp{ID: newTestUUID(), TenantID: tenant, AssignmentID: a.ID,
		Kind: "PING", Lat: 52.37, Lng: 4.90, AccuracyM: 8}
	if err := s.Dispatch().AddGeoStamp(ctx, second); err != nil {
		t.Fatalf("stamp2: %v", err)
	}

	got, err := s.Dispatch().LatestGeoStamp(ctx, tenant, a.ID)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if got.ID != second.ID || got.Lat != 52.37 || got.AccuracyM != 8 {
		t.Fatalf("latest is not the newest stamp: %+v", got)
	}
	if got.At.IsZero() {
		t.Fatal("stamp timestamp lost")
	}

	// RLS: another tenant asking for this assignment gets not-found.
	if _, err := s.Dispatch().LatestGeoStamp(ctx, other, a.ID); err == nil {
		t.Fatal("RLS leak on LatestGeoStamp")
	}
}

func TestPGLifecycleConfig(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-lifecycle")

	tn, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	tn.LifecycleConfig = model.LifecycleConfig{Disabled: true, PostTripHours: 12, WinBackDays: 45}
	if err := s.Tenants().UpdateConfig(ctx, tn); err != nil {
		t.Fatalf("update: %v", err)
	}
	back, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("reget: %v", err)
	}
	if !back.LifecycleConfig.Disabled || back.LifecycleConfig.PostTripHours != 12 ||
		back.LifecycleConfig.WinBackDays != 45 {
		t.Fatalf("lifecycle_config roundtrip: %+v", back.LifecycleConfig)
	}

	// List (the sweeper's path) carries it too.
	tenants, err := s.Tenants().List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, row := range tenants {
		if row.ID == tenant {
			found = true
			if row.LifecycleConfig.WinBackDays != 45 {
				t.Fatalf("List lost lifecycle_config: %+v", row.LifecycleConfig)
			}
		}
	}
	if !found {
		t.Fatal("tenant missing from List")
	}

	// A config-less tenant reads as zero-value, and a partial update of an
	// unrelated config (dispatch) must not wipe lifecycle (selective patch
	// discipline, Phase 24, enforced at the pg layer).
	tn2, _ := s.Tenants().Get(ctx, tenant)
	tn2.DispatchConfig = model.DispatchConfig{TurnaroundMinutes: 15}
	if err := s.Tenants().UpdateConfig(ctx, tn2); err != nil {
		t.Fatalf("update2: %v", err)
	}
	back2, _ := s.Tenants().Get(ctx, tenant)
	if back2.LifecycleConfig.WinBackDays != 45 {
		t.Fatalf("lifecycle_config wiped by dispatch update: %+v", back2.LifecycleConfig)
	}
}

func TestPGClientsList(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-clientlist")
	other := pgTenant(t, s, "pg-clientlist-other")

	mk := func(tenantID, name, phone string) *model.Client {
		c := &model.Client{ID: newTestUUID(), TenantID: tenantID, FullName: name,
			Phone: phone, KYCStatus: "none", Consent: map[string]any{},
			CreatedAt: time.Now().UTC()}
		if err := s.Clients().Upsert(ctx, c); err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
		return c
	}
	mk(tenant, "List Client A", "+971500000410")
	time.Sleep(20 * time.Millisecond) // created_at DESC ordering must be unambiguous
	cB := mk(tenant, "List Client B", "+971500000411")
	mk(other, "List Client Other", "+971500000412")

	list, err := s.Clients().List(ctx, tenant, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("tenant scope: got %d clients", len(list))
	}
	if list[0].ID != cB.ID {
		t.Fatalf("newest-first ordering broken: %s then %s", list[0].FullName, list[1].FullName)
	}

	// Limit is honored.
	one, err := s.Clients().List(ctx, tenant, 1)
	if err != nil || len(one) != 1 {
		t.Fatalf("limit: %d %v", len(one), err)
	}

	// RLS: the other tenant sees only its own.
	ol, err := s.Clients().List(ctx, other, 10)
	if err != nil || len(ol) != 1 {
		t.Fatalf("RLS leak on clients List: %d %v", len(ol), err)
	}
}
