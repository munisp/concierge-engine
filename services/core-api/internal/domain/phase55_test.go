// Phase 55 domain tests: vertical segment axes (proven demand via
// booked-request items, declared interest via open-lead verticals) and
// pack validation.
package domain

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// bookVertical55 seeds a request carrying an item in the given vertical
// plus its booking — the proven-demand chain the segment rule reads.
func bookVertical55(t *testing.T, s *Services, tenantID, clientID, vertical string) {
	t.Helper()
	ctx := context.Background()
	now := s.Now()
	rid := "req-v55-" + clientID + "-" + vertical
	if err := s.Store.Requests().Create(ctx, &model.ServiceRequest{
		ID: rid, TenantID: tenantID, ClientID: clientID,
		State: model.StateConfirmed, CreatedAt: now, UpdatedAt: now,
		Items: []model.ServiceItem{{ID: "it-" + rid, Vertical: vertical}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Bookings().Create(ctx, &model.Booking{
		ID: "bk-v55-" + clientID + "-" + vertical, TenantID: tenantID,
		RequestID: rid, QuoteID: "q-" + rid, State: "COMPLETED", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSegmentVerticalAffinity55(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	s := testServices(t, Deps{Now: func() time.Time { return now }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	jet := outreachClient(t, s, tenantID, "+971500000501", true)
	yacht := outreachClient(t, s, tenantID, "+971500000502", true)
	bookVertical55(t, s, tenantID, jet, "PRIVATE_JETS")
	bookVertical55(t, s, tenantID, yacht, "YACHTS")

	matched, err := s.SegmentClients(ctx, tenantID, model.SegmentRules{Vertical: "PRIVATE_JETS"})
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 || matched[0].ID != jet {
		t.Fatalf("proven-demand vertical segment must match only the jet booker: %+v", matched)
	}
	// Unknown vertical = empty audience, never an error.
	matched, err = s.SegmentClients(ctx, tenantID, model.SegmentRules{Vertical: "HELICOPTERS"})
	if err != nil || len(matched) != 0 {
		t.Fatalf("unbooked vertical = empty segment: %+v err=%v", matched, err)
	}
}

func TestSegmentLeadVertical55(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	s := testServices(t, Deps{Now: func() time.Time { return now }})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	interested := outreachClient(t, s, tenantID, "+971500000503", true)
	other := outreachClient(t, s, tenantID, "+971500000504", true)
	// Declared interest on an OPEN lead linked to the client.
	if err := s.Store.Leads().Create(ctx, &model.Lead{
		ID: "lead-v55-1", TenantID: tenantID, Kind: "event", Name: "Art week",
		ClientID: interested, Status: model.LeadStatusNew,
		Verticals: []string{"YACHTS"}, Source: "site",
	}); err != nil {
		t.Fatal(err)
	}
	// A LOST lead's interest must not qualify.
	if err := s.Store.Leads().Create(ctx, &model.Lead{
		ID: "lead-v55-2", TenantID: tenantID, Kind: "event", Name: "Old",
		ClientID: other, Status: model.LeadStatusLost,
		Verticals: []string{"YACHTS"}, Source: "site",
	}); err != nil {
		t.Fatal(err)
	}
	matched, err := s.SegmentClients(ctx, tenantID, model.SegmentRules{LeadVertical: "YACHTS"})
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 || matched[0].ID != interested {
		t.Fatalf("declared-interest segment must match only the open lead's client: %+v", matched)
	}
}

func TestPackValidation55(t *testing.T) {
	good := []model.IndustryPack{{
		Slug: "f1-weekend", Name: "F1 Race Weekend",
		ServiceIDs: []string{"svc-1", "svc-2"},
		Pitch:      map[string]string{"en": "Cars, jets and paddock access."},
		Active:     true,
	}}
	if err := ValidatePacks(good); err != nil {
		t.Fatalf("valid pack: %v", err)
	}
	bad := map[string][]model.IndustryPack{
		"bad slug":    {{Slug: "F1 Weekend!", Name: "x", ServiceIDs: []string{"a"}}},
		"no name":     {{Slug: "ok-slug", ServiceIDs: []string{"a"}}},
		"no services": {{Slug: "ok-slug", Name: "x"}},
		"dup slug": {
			{Slug: "dup", Name: "a", ServiceIDs: []string{"a"}},
			{Slug: "dup", Name: "b", ServiceIDs: []string{"b"}},
		},
		"markup pitch": {{Slug: "ok", Name: "x", ServiceIDs: []string{"a"},
			Pitch: map[string]string{"en": "best <b>pack</b>"}}},
		"long pitch": {{Slug: "ok", Name: "x", ServiceIDs: []string{"a"},
			Pitch: map[string]string{"en": string(make([]rune, 601))}}},
	}
	for name, packs := range bad {
		if err := ValidatePacks(packs); err == nil {
			t.Fatalf("%s: must fail validation", name)
		}
	}
}
