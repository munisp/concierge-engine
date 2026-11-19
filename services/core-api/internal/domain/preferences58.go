// Phase 58: rider preference profiles. The preference store (key/value,
// consent-scoped, VIP-encrypted, DSAR-cascaded) existed since early phases
// but was free-form: nothing standardized the vocabulary and — the real
// gap — preferences never reached the person delivering the service. This
// phase adds a curated vocabulary with validation, a typed ride-profile
// view, and propagation to the chauffeur trip view and partner surfaces.
package domain

import (
	"context"
	"strconv"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// Curated preference vocabulary. Curated namespaces (ride., dining.,
// access., comms., avi.) are validated strictly — a typo'd enum is rejected
// at write time instead of silently rotting in the profile. custom.* stays
// free-form for tenant-specific needs; unknown legacy keys keep working
// (no breaking change for existing records).
var curatedPreferenceValues = map[string][]string{
	"ride.conversation": {"quiet", "light", "chatty"},
	"ride.route":        {"fastest", "scenic", "no_tolls"},
	"ride.seat":         {"front_passenger", "rear_left", "rear_right"},
	"ride.child_seat":   {"none", "infant", "toddler", "booster"},
	"ride.luggage":      {"light", "standard", "heavy"},
	"access.mobility":   {"none", "assistance", "wheelchair"},
	"dining.dietary":    {"none", "vegetarian", "vegan", "halal", "kosher", "gluten_free"},
}

// Free-text curated keys (bounded length instead of enum).
var curatedPreferenceFreeText = map[string]int{
	"ride.music":        60,  // genre or "off" / "client playlist"
	"dining.allergens":  200, // comma-separated allergen list
	"comms.language":    16,  // BCP-47
	"avi.amenities":     200, // press, catering notes
	"ride.music_volume": 10,  // low | medium | high
}

const (
	maxCustomPreferenceLen = 200
	maxPreferenceValueLen  = 280 // absolute ceiling, any key
	minRideTemperatureC    = 16
	maxRideTemperatureC    = 26
)

// ValidatePreference checks a key/value pair against the curated
// vocabulary. Curated namespaces are strict; custom.* is length-capped;
// other keys pass through (legacy tolerance).
func ValidatePreference(key, value string) error {
	if allowed, ok := curatedPreferenceValues[key]; ok {
		for _, a := range allowed {
			if value == a {
				return nil
			}
		}
		return httpx.ErrValidation
	}
	if _, ok := curatedPreferenceFreeText[key]; ok {
		max := curatedPreferenceFreeText[key]
		if value == "" || len([]rune(value)) > max {
			return httpx.ErrValidation
		}
		return nil
	}
	if key == "ride.temperature_c" {
		n, err := strconv.Atoi(value)
		if err != nil || n < minRideTemperatureC || n > maxRideTemperatureC {
			return httpx.ErrValidation
		}
		return nil
	}
	if strings.HasPrefix(key, "custom.") {
		if value == "" || len([]rune(value)) > maxCustomPreferenceLen {
			return httpx.ErrValidation
		}
		return nil
	}
	if len([]rune(value)) > maxPreferenceValueLen {
		return httpx.ErrValidation
	}
	return nil
}

// rideProfileKeys surface to the chauffeur trip view — the person actually
// delivering the ride.
var rideProfileKeys = []string{
	"ride.music", "ride.music_volume", "ride.temperature_c", "ride.conversation",
	"ride.route", "ride.seat", "ride.child_seat", "ride.luggage",
	"access.mobility", "comms.language",
}

// partnerPreferenceKeys propagate to partner assignment views and webhook
// offers — what a tour operator or DMC needs to deliver well.
var partnerPreferenceKeys = []string{
	"ride.music", "ride.temperature_c", "ride.conversation", "ride.child_seat",
	"ride.luggage", "access.mobility", "comms.language",
	"dining.dietary", "dining.allergens",
}

// RideProfile is the typed comfort view merged from the preference store.
type RideProfile struct {
	Music        string `json:"music,omitempty"`
	MusicVolume  string `json:"music_volume,omitempty"`
	TemperatureC string `json:"temperature_c,omitempty"`
	Conversation string `json:"conversation,omitempty"`
	Route        string `json:"route,omitempty"`
	Seat         string `json:"seat,omitempty"`
	ChildSeat    string `json:"child_seat,omitempty"`
	Luggage      string `json:"luggage,omitempty"`
	Mobility     string `json:"mobility,omitempty"`
	Language     string `json:"language,omitempty"`
	Dietary      string `json:"dietary,omitempty"`
	Allergens    string `json:"allergens,omitempty"`
}

// Empty reports whether the client has any curated preferences at all —
// callers render nothing instead of a blank card.
func (p RideProfile) Empty() bool {
	return p == RideProfile{}
}

// RideProfile merges the client's curated preferences into the typed view.
// Values come through Preferences() — decrypted for VIPs, audit-logged.
func (svc *ClientService) RideProfile(ctx context.Context, tenantID, clientID string) (*RideProfile, error) {
	prefs, err := svc.Preferences(ctx, tenantID, clientID)
	if err != nil {
		return nil, err
	}
	rp := &RideProfile{}
	for _, p := range prefs {
		switch p.Key {
		case "ride.music":
			rp.Music = p.Value
		case "ride.music_volume":
			rp.MusicVolume = p.Value
		case "ride.temperature_c":
			rp.TemperatureC = p.Value
		case "ride.conversation":
			rp.Conversation = p.Value
		case "ride.route":
			rp.Route = p.Value
		case "ride.seat":
			rp.Seat = p.Value
		case "ride.child_seat":
			rp.ChildSeat = p.Value
		case "ride.luggage":
			rp.Luggage = p.Value
		case "access.mobility":
			rp.Mobility = p.Value
		case "comms.language":
			rp.Language = p.Value
		case "dining.dietary":
			rp.Dietary = p.Value
		case "dining.allergens":
			rp.Allergens = p.Value
		}
	}
	return rp, nil
}

// preferenceMap filters a preference list to the given key set. Keys are
// deduplicated by the store (upsert per key), so last-write-wins holds.
func preferenceMap(prefs []model.Preference, keys []string) map[string]string {
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	out := map[string]string{}
	for _, p := range prefs {
		if want[p.Key] && p.Value != "" {
			out[p.Key] = p.Value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// RidePreferenceMap is the chauffeur-facing subset.
func (svc *ClientService) RidePreferenceMap(ctx context.Context, tenantID, clientID string) (map[string]string, error) {
	prefs, err := svc.Preferences(ctx, tenantID, clientID)
	if err != nil {
		return nil, err
	}
	return preferenceMap(prefs, rideProfileKeys), nil
}

// PartnerPreferenceMap is the partner-facing subset. Preferences scoped
// "private" never leave the tenant — consent governs propagation.
func (svc *ClientService) PartnerPreferenceMap(ctx context.Context, tenantID, clientID string) (map[string]string, error) {
	prefs, err := svc.Preferences(ctx, tenantID, clientID)
	if err != nil {
		return nil, err
	}
	public := make([]model.Preference, 0, len(prefs))
	for _, p := range prefs {
		if p.ConsentScope == "private" {
			continue
		}
		public = append(public, p)
	}
	return preferenceMap(public, partnerPreferenceKeys), nil
}
