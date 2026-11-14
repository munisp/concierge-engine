//go:build pg

// Postgres integration tests — Phase 22: conversation threading anchor
// (FindActive + RLS), assignment service windows (roundtrip incl. NULL),
// tenant dispatch_config. Same gate as pg_integration_test.go.
package store

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGConversationThreading(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-thread")
	other := pgTenant(t, s, "pg-thread-other")

	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Thread Test",
		Phone: "+971500000300", KYCStatus: "none", Consent: map[string]any{}}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatalf("client: %v", err)
	}

	c1 := &model.Conversation{ID: newTestUUID(), TenantID: tenant, ClientID: cl.ID,
		Channel: "WHATSAPP", State: "NEW", ExternalRef: "wamid.pg1", CreatedAt: time.Now().UTC()}
	if err := s.Conversations().CreateConversation(ctx, c1); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.Conversations().FindActive(ctx, tenant, cl.ID, "WHATSAPP")
	if err != nil || got.ID != c1.ID {
		t.Fatalf("FindActive: %+v, %v", got, err)
	}
	if _, err := s.Conversations().FindActive(ctx, tenant, cl.ID, "WEB"); err == nil {
		t.Fatal("different channel must not match")
	}
	if _, err := s.Conversations().FindActive(ctx, other, cl.ID, "WHATSAPP"); err == nil {
		t.Fatal("RLS leak on FindActive")
	}

	if err := s.Conversations().SetState(ctx, tenant, c1.ID, "CLOSED"); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := s.Conversations().FindActive(ctx, tenant, cl.ID, "WHATSAPP"); err == nil {
		t.Fatal("CLOSED thread must not be the active anchor")
	}
}

func TestPGAssignmentWindows(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-windows")

	cl := &model.Client{ID: newTestUUID(), TenantID: tenant, FullName: "Window Client",
		Phone: "+971500000301", KYCStatus: "none", Consent: map[string]any{}}
	if err := s.Clients().Upsert(ctx, cl); err != nil {
		t.Fatalf("client: %v", err)
	}
	fleet := &model.FleetResource{ID: newTestUUID(), TenantID: tenant, Kind: "SEDAN",
		Label: "PG Window Car", QualityScore: 1, Active: true}
	if err := s.Dispatch().AddFleetResource(ctx, fleet); err != nil {
		t.Fatalf("fleet: %v", err)
	}

	start := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	bk := pgQuoteBornBooking(t, s, tenant, cl.ID, "pg-win-b1")
	a := &model.Assignment{ID: newTestUUID(), TenantID: tenant, BookingID: bk,
		FleetResourceID: fleet.ID, Status: "ASSIGNED",
		StartAt: start, EndAt: start.Add(2 * time.Hour),
		CreatedAt: time.Now().UTC()}
	if err := s.Dispatch().CreateAssignment(ctx, a); err != nil {
		t.Fatalf("assignment: %v", err)
	}
	list, err := s.Dispatch().ListAssignments(ctx, tenant)
	if err != nil || len(list) == 0 {
		t.Fatalf("list: %v", err)
	}
	var got *model.Assignment
	for i := range list {
		if list[i].ID == a.ID {
			got = &list[i]
		}
	}
	if got == nil {
		t.Fatal("assignment not found in list")
	}
	if !got.StartAt.Equal(start) || !got.EndAt.Equal(start.Add(2*time.Hour)) {
		t.Fatalf("window roundtrip: got %v..%v want %v..%v", got.StartAt, got.EndAt, start, start.Add(2*time.Hour))
	}

	// Partner-only assignment with an unknown schedule: window stays zero (NULL).
	bk2 := pgQuoteBornBooking(t, s, tenant, cl.ID, "pg-win-b2")
	a2 := &model.Assignment{ID: newTestUUID(), TenantID: tenant, BookingID: bk2,
		PartnerID: newTestUUID(), Status: "ASSIGNED", CreatedAt: time.Now().UTC()}
	if err := s.Dispatch().CreateAssignment(ctx, a2); err != nil {
		t.Fatalf("assignment2: %v", err)
	}
	list2, err := s.Dispatch().ListAssignments(ctx, tenant)
	if err != nil {
		t.Fatalf("list2: %v", err)
	}
	for _, row := range list2 {
		if row.ID == a2.ID {
			if !row.StartAt.IsZero() || !row.EndAt.IsZero() {
				t.Fatalf("zero window must roundtrip as zero, got %v..%v", row.StartAt, row.EndAt)
			}
			return
		}
	}
	t.Fatal("partner assignment not found")
}

func TestPGDispatchConfig(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()
	tenant := pgTenant(t, s, "pg-dispatchcfg")

	tn, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	tn.DispatchConfig = model.DispatchConfig{TurnaroundMinutes: 45, DefaultDurationMinutes: 90}
	if err := s.Tenants().UpdateConfig(ctx, tn); err != nil {
		t.Fatalf("update: %v", err)
	}
	back, err := s.Tenants().Get(ctx, tenant)
	if err != nil {
		t.Fatalf("reget: %v", err)
	}
	if back.DispatchConfig.TurnaroundMinutes != 45 || back.DispatchConfig.DefaultDurationMinutes != 90 {
		t.Fatalf("dispatch_config roundtrip: %+v", back.DispatchConfig)
	}
}
