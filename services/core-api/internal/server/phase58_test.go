package server_test

// Phase 58: ride-profile endpoint + track-token self-serve preferences.

import (
	"net/http"
	"testing"

	"concierge-engine/services/core-api/internal/model"
)

func TestRideProfileAndSelfServe58(t *testing.T) {
	env := newEnv(t)

	// Seed a client with preferences via the tenant API.
	var res struct {
		Client struct {
			ID string `json:"id"`
		} `json:"client"`
	}
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"phone": "+2348044444444", "name": "Ada Lovelace"}, http.StatusOK, &res)
	cid := res.Client.ID
	env.do("PUT", "/v1/clients/"+cid+"/preferences/ride.music", env.tenantID,
		map[string]string{"value": "afrobeats", "recorded_by": "console"}, http.StatusOK, nil)
	// Invalid curated value rejected at the API edge.
	env.do("PUT", "/v1/clients/"+cid+"/preferences/ride.conversation", env.tenantID,
		map[string]string{"value": "talkative-ish"}, http.StatusUnprocessableEntity, nil)

	// Typed ride profile.
	var rp struct {
		Music string `json:"music"`
	}
	env.do("GET", "/v1/clients/"+cid+"/ride-profile", env.tenantID, nil, http.StatusOK, &rp)
	if rp.Music != "afrobeats" {
		t.Fatalf("ride profile: %+v", rp)
	}

	// Track-token self-serve: client sets their own preferences.
	// Seed request + booking + assignment so the track token resolves.
	bookingID := seedBookingForClient58(t, env, cid)
	assignmentID := assignFleet58(t, env, bookingID)
	trackToken := env.svc.Trips.ClientTokenFor(env.tenantID, assignmentID)

	env.doBearer("PUT", "/v1/track/"+trackToken+"/preferences/ride.temperature_c", "",
		map[string]string{"value": "20"}, http.StatusOK, nil)
	env.doBearer("PUT", "/v1/track/"+trackToken+"/preferences/ride.temperature_c", "",
		map[string]string{"value": "99"}, http.StatusUnprocessableEntity, nil)

	var selfServe struct {
		Music        string `json:"music"`
		TemperatureC string `json:"temperature_c"`
	}
	env.doBearer("GET", "/v1/track/"+trackToken+"/preferences", "", nil, http.StatusOK, &selfServe)
	if selfServe.Music != "afrobeats" || selfServe.TemperatureC != "20" {
		t.Fatalf("self-serve profile: %+v", selfServe)
	}

	// The chauffeur trip view carries the comfort block.
	tripToken := env.svc.Trips.TokenFor(env.tenantID, assignmentID)
	var trip struct {
		Preferences map[string]string `json:"preferences"`
	}
	env.doBearer("GET", "/v1/trips/"+tripToken, "", nil, http.StatusOK, &trip)
	if trip.Preferences["ride.music"] != "afrobeats" || trip.Preferences["ride.temperature_c"] != "20" {
		t.Fatalf("trip view preferences: %+v", trip.Preferences)
	}

	// A track token can never drive the trip, a trip token can never read prefs.
	env.doBearer("GET", "/v1/track/"+tripToken+"/preferences", "", nil, http.StatusForbidden, nil)
	env.doBearer("GET", "/v1/trips/"+trackToken, "", nil, http.StatusForbidden, nil)
}

func seedBookingForClient58(t *testing.T, env *testEnv, clientID string) string {
	t.Helper()
	svc := env.svc
	sr, err := svc.Booking.CreateRequest(t.Context(), &model.ServiceRequest{
		TenantID: env.tenantID, ClientID: clientID, IdempotencyKey: "p58-" + clientID[:8],
		Items: []model.ServiceItem{{Vertical: "CHAUFFEUR_POINT_TO_POINT",
			PickupAddress: "Lekki", DropoffAddress: "Ikoyi",
			PickupAt: 1893456000, Passengers: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := &model.Booking{ID: "b58-" + clientID[:8], TenantID: env.tenantID,
		RequestID: sr.ID, State: "CONFIRMED", CreatedAt: svc.Now()}
	if err := svc.Store.Bookings().Create(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	return b.ID
}

func assignFleet58(t *testing.T, env *testEnv, bookingID string) string {
	t.Helper()
	fr, err := env.svc.Dispatch.AddFleetResource(t.Context(), &model.FleetResource{
		TenantID: env.tenantID, Kind: "sedan", Label: "S-58-" + bookingID[len(bookingID)-4:], Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := env.svc.Dispatch.Assign(t.Context(), &model.Assignment{
		TenantID: env.tenantID, BookingID: bookingID, FleetResourceID: fr.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}
