//go:build pg

package store

// Postgres integration tests — Phase 60: tenants.mode_presets jsonb from
// migration 0039 round-trips through Create/Get/GetBySlug/UpdateConfig.

import (
	"context"
	"testing"

	"concierge-engine/services/core-api/internal/model"
)

func TestPGTenantModePresets60(t *testing.T) {
	s := pgTestStore(t)
	ctx := context.Background()

	tn := &model.Tenant{
		ID: newTestUUID(), Slug: "pg-modes60", Name: "Modes PG", Archetype: "flagship",
		ModePresets: map[string]model.ModePreset{
			"celebrate": {Label: "Owambe", Prefs: map[string]string{
				"ride.conversation": "chatty", "ride.music": "afrobeats owambe"}},
		},
	}
	if err := s.Tenants().Create(ctx, tn); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.Tenants().Get(ctx, tn.ID)
	if err != nil {
		t.Fatal(err)
	}
	preset, ok := got.ModePresets["celebrate"]
	if !ok || preset.Label != "Owambe" || preset.Prefs["ride.music"] != "afrobeats owambe" {
		t.Fatalf("round-trip: %+v", got.ModePresets)
	}
	bySlug, err := s.Tenants().GetBySlug(ctx, "pg-modes60")
	if err != nil || bySlug.ModePresets["celebrate"].Label != "Owambe" {
		t.Fatalf("by-slug: %+v %v", bySlug.ModePresets, err)
	}

	// UpdateConfig persists edits.
	got.ModePresets["relax"] = model.ModePreset{Label: "Chill NG", Prefs: map[string]string{
		"ride.temperature_c": "23"}}
	if err := s.Tenants().UpdateConfig(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, err := s.Tenants().Get(ctx, tn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.ModePresets["relax"].Prefs["ride.temperature_c"] != "23" {
		t.Fatalf("update: %+v", again.ModePresets)
	}

	// Tenants without overrides read back an empty map, never NULL junk.
	plain := pgTenant(t, s, "pg-modes60-plain")
	gotPlain, err := s.Tenants().Get(ctx, plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotPlain.ModePresets) != 0 {
		t.Fatalf("plain tenant: %+v", gotPlain.ModePresets)
	}
}
