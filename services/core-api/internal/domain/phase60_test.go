package domain

// Phase 60: ride modes — preset expansion through the validated path,
// tenant overrides, prep tasks, mode-aware soundscape composition.

import (
	"context"
	"strings"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestRideModes60(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	res, err := s.Clients.Resolve(ctx, tenantID, "+2348099999999", "", "Mode Rider")
	if err != nil {
		t.Fatal(err)
	}
	cid := res.Client.ID

	// Applying Focus expands the preset through the validated path.
	applied, err := s.Clients.ApplyMode(ctx, tenantID, cid, "focus", "test")
	if err != nil {
		t.Fatal(err)
	}
	if applied["ride.mode"] != "focus" || applied["ride.conversation"] != "quiet" ||
		applied["ride.music"] != "none" || applied["ride.temperature_c"] != "21" {
		t.Fatalf("applied: %+v", applied)
	}
	// The profile reflects it; validation still guards the enum.
	rp, err := s.Clients.RideProfile(ctx, tenantID, cid)
	if err != nil {
		t.Fatal(err)
	}
	if rp.Mode != "focus" || rp.Conversation != "quiet" || rp.Music != "none" {
		t.Fatalf("profile: %+v", rp)
	}
	if _, err := s.Clients.ApplyMode(ctx, tenantID, cid, "party-hard", "test"); err != httpx.ErrValidation {
		t.Fatalf("bad mode: %v", err)
	}

	// Tenant override replaces preset CONTENT, never the enum.
	tenant, err := s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	tenant.ModePresets = map[string]model.ModePreset{
		"celebrate": {Label: "Owambe", Prefs: map[string]string{
			"ride.conversation": "chatty", "ride.music": "afrobeats owambe"}},
		"rogue-mode": {Label: "Nope", Prefs: map[string]string{"ride.music": "x"}},
	}
	if err := s.Store.Tenants().UpdateConfig(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	modes, err := s.Clients.EffectiveModes(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if modes["celebrate"].Label != "Owambe" || modes["celebrate"].Prefs["ride.music"] != "afrobeats owambe" {
		t.Fatalf("override: %+v", modes["celebrate"])
	}
	if _, ok := modes["rogue-mode"]; ok {
		t.Fatal("tenant-defined mode names must not enter the validated enum")
	}
	if modes["focus"].Prefs["ride.music"] != "none" {
		t.Fatal("house presets must survive unrelated overrides")
	}
	applied2, err := s.Clients.ApplyMode(ctx, tenantID, cid, "celebrate", "test")
	if err != nil || applied2["ride.music"] != "afrobeats owambe" {
		t.Fatalf("tenant preset apply: %v %+v", err, applied2)
	}
}

func TestPrepTasks60(t *testing.T) {
	tasks := PrepTasks(map[string]string{
		"ride.temperature_c": "20", "ride.child_seat": "toddler",
		"ride.luggage": "heavy", "access.mobility": "wheelchair",
		"ride.music": "afrobeats", "ride.music_volume": "low",
	})
	joined := ""
	for _, task := range tasks {
		joined += task + "\n"
	}
	for _, want := range []string{"20°C", "toddler child seat", "luggage space", "Wheelchair", "afrobeats (low)"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
	quiet := PrepTasks(map[string]string{"ride.music": "none", "ride.conversation": "quiet"})
	if len(quiet) != 2 || !strings.Contains(quiet[0]+quiet[1], "No music") || !strings.Contains(quiet[0]+quiet[1], "Quiet ride") {
		t.Fatalf("quiet tasks: %+v", quiet)
	}
	if got := PrepTasks(map[string]string{}); len(got) != 0 {
		t.Fatalf("empty profile must yield no tasks: %+v", got)
	}
}

func TestSoundscapeModes60(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	// Mode overrides conversation-derived mood: celebrate wins over quiet.
	res, _ := s.Clients.Resolve(ctx, tenantID, "+2348010101010", "", "Party Rider")
	cid := res.Client.ID
	if _, err := s.Clients.ApplyMode(ctx, tenantID, cid, "celebrate", "test"); err != nil {
		t.Fatal(err)
	}
	// celebration preset sets chatty; force conversation quiet to prove mode wins.
	if _, err := s.Clients.SavePreference(ctx, tenantID, cid, "ride.conversation", "quiet", "service", "", "test"); err != nil {
		t.Fatal(err)
	}
	bookingID := bookAt(t, s, tenantID, cid, "mode60a", time.Now().Add(48*time.Hour).Unix(), 2)
	spec, _, err := s.Music.CompositionFor(ctx, tenantID, bookingID)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Mood != "upbeat" || spec.TempoBPM != 115 || spec.Skip != "" {
		t.Fatalf("celebrate spec: %+v", spec)
	}

	// Focus sets ride.music=none → no render, honest skip (provider present
	// but must never be called — failProvider proves it by erroring).
	res2, _ := s.Clients.Resolve(ctx, tenantID, "+2348011111112", "", "Focus Rider")
	cid2 := res2.Client.ID
	if _, err := s.Clients.ApplyMode(ctx, tenantID, cid2, "focus", "test"); err != nil {
		t.Fatal(err)
	}
	s.Music.SetProvider(failProvider{})
	bookingID2 := bookAt(t, s, tenantID, cid2, "mode60b", time.Now().Add(49*time.Hour).Unix(), 2)
	sc, err := s.Music.Generate(ctx, tenantID, bookingID2)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Status != model.SoundscapeDegraded || sc.Detail != "rider-prefers:no-music" || sc.AiComposed {
		t.Fatalf("focus skip: %+v", sc)
	}
	s.Music.SetProvider(nil)
}
