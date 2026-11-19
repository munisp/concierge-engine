package domain

// Phase 58: curated preference vocabulary, typed ride profile, and
// consent-filtered partner propagation.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPreferenceVocabulary58(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	res, err := s.Clients.Resolve(ctx, tenantID, "+2348011111111", "", "Ada Lovelace")
	if err != nil {
		t.Fatal(err)
	}
	cid := res.Client.ID

	save := func(key, value string) error {
		_, err := s.Clients.SavePreference(ctx, tenantID, cid, key, value, "service", "", "test")
		return err
	}

	// Curated enums: valid passes, invalid rejected at write time.
	if err := save("ride.conversation", "quiet"); err != nil {
		t.Fatal(err)
	}
	if err := save("ride.conversation", "talkative-ish"); err != httpx.ErrValidation {
		t.Fatalf("bad enum: %v", err)
	}
	// Temperature range.
	if err := save("ride.temperature_c", "21"); err != nil {
		t.Fatal(err)
	}
	if err := save("ride.temperature_c", "30"); err != httpx.ErrValidation {
		t.Fatalf("out-of-range temperature: %v", err)
	}
	if err := save("ride.temperature_c", "warm"); err != httpx.ErrValidation {
		t.Fatalf("non-numeric temperature: %v", err)
	}
	// Free-text curated keys: bounded.
	if err := save("ride.music", "afrobeats"); err != nil {
		t.Fatal(err)
	}
	if err := save("comms.language", "en-NG"); err != nil {
		t.Fatal(err)
	}
	// custom.* free-form, capped.
	if err := save("custom.favorite_magazine", "Monocle"); err != nil {
		t.Fatal(err)
	}
	// Legacy unknown keys keep working.
	if err := save("allergies", "peanuts"); err != nil {
		t.Fatal(err)
	}
}

func TestRideProfile58(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	res, _ := s.Clients.Resolve(ctx, tenantID, "+2348022222222", "", "Chidi Okonkwo")
	cid := res.Client.ID

	// Empty profile: typed view is empty, map is nil.
	rp, err := s.Clients.RideProfile(ctx, tenantID, cid)
	if err != nil || !rp.Empty() {
		t.Fatalf("empty profile: %+v %v", rp, err)
	}

	for kv, v := range map[string]string{
		"ride.music": "amapiano", "ride.temperature_c": "20",
		"ride.conversation": "quiet", "ride.child_seat": "toddler",
		"dining.dietary": "halal", "dining.allergens": "peanuts, shellfish",
	} {
		if _, err := s.Clients.SavePreference(ctx, tenantID, cid, kv, v, "service", "", "test"); err != nil {
			t.Fatal(err)
		}
	}

	rp, err = s.Clients.RideProfile(ctx, tenantID, cid)
	if err != nil {
		t.Fatal(err)
	}
	if rp.Music != "amapiano" || rp.TemperatureC != "20" || rp.Conversation != "quiet" ||
		rp.ChildSeat != "toddler" || rp.Dietary != "halal" || rp.Allergens != "peanuts, shellfish" {
		t.Fatalf("ride profile: %+v", rp)
	}

	// Chauffeur subset excludes dining; partner subset includes it.
	ride, err := s.Clients.RidePreferenceMap(ctx, tenantID, cid)
	if err != nil {
		t.Fatal(err)
	}
	if ride["ride.music"] != "amapiano" || ride["dining.dietary"] != "" {
		t.Fatalf("ride subset: %+v", ride)
	}
	partner, err := s.Clients.PartnerPreferenceMap(ctx, tenantID, cid)
	if err != nil {
		t.Fatal(err)
	}
	if partner["dining.dietary"] != "halal" || partner["dining.allergens"] != "peanuts, shellfish" {
		t.Fatalf("partner subset: %+v", partner)
	}

	// "private" consent scope never leaves the tenant.
	if _, err := s.Clients.SavePreference(ctx, tenantID, cid, "dining.allergens", "peanuts", "private", "", "test"); err != nil {
		t.Fatal(err)
	}
	partner, _ = s.Clients.PartnerPreferenceMap(ctx, tenantID, cid)
	if partner["dining.allergens"] != "" {
		t.Fatalf("private preference leaked to partner subset: %+v", partner)
	}
	ride, _ = s.Clients.RidePreferenceMap(ctx, tenantID, cid)
	if ride["ride.music"] != "amapiano" {
		t.Fatalf("ride subset after private save: %+v", ride)
	}
}

// Trip view surfaces the comfort block to the chauffeur.
func TestTripViewPreferences58(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	res, _ := s.Clients.Resolve(ctx, tenantID, "+2348033333333", "", "Funke Akindele")
	cid := res.Client.ID
	if _, err := s.Clients.SavePreference(ctx, tenantID, cid, "ride.music", "jazz", "service", "", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Clients.SavePreference(ctx, tenantID, cid, "ride.conversation", "chatty", "service", "", "test"); err != nil {
		t.Fatal(err)
	}

	bookingID := bookAt(t, s, tenantID, cid, "pref-58", s.Now().Add(24*time.Hour).Unix(), 2)
	fr, err := s.Dispatch.AddFleetResource(ctx, &model.FleetResource{
		ID: NewID(), TenantID: tenantID, Kind: "sedan", Label: "S-58", Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: bookingID, FleetResourceID: fr.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	view, err := s.Trips.View(ctx, s.Trips.TokenFor(tenantID, a.ID))
	if err != nil {
		t.Fatal(err)
	}
	if view.Preferences["ride.music"] != "jazz" || view.Preferences["ride.conversation"] != "chatty" {
		t.Fatalf("trip view preferences: %+v", view.Preferences)
	}
}
