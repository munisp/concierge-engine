package server_test

// Phase 60: ride modes — tenant API + track-token self-serve, preset
// listing, chauffeur prep tasks, enum guard at the edge.

import (
	"net/http"
	"testing"
)

func TestRideModeEndpoints60(t *testing.T) {
	env := newEnv(t)

	// Preset catalog (house defaults — no tenant overrides in the test env).
	var modes struct {
		Modes map[string]struct {
			Label string            `json:"label"`
			Prefs map[string]string `json:"prefs"`
		} `json:"modes"`
	}
	env.do("GET", "/v1/ride-modes", env.tenantID, nil, http.StatusOK, &modes)
	if modes.Modes["focus"].Prefs["ride.music"] != "none" || modes.Modes["celebrate"].Label == "" {
		t.Fatalf("modes: %+v", modes.Modes)
	}

	// Seed a rider.
	var res struct {
		Client struct {
			ID string `json:"id"`
		} `json:"client"`
	}
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"phone": "+2348020202020", "name": "Mode Tester"}, http.StatusOK, &res)
	cid := res.Client.ID

	// Tenant API applies a mode; the profile shows the expansion.
	var applied struct {
		Applied map[string]string `json:"applied"`
	}
	env.do("POST", "/v1/clients/"+cid+"/ride-mode", env.tenantID,
		map[string]string{"mode": "focus"}, http.StatusOK, &applied)
	if applied.Applied["ride.mode"] != "focus" || applied.Applied["ride.conversation"] != "quiet" {
		t.Fatalf("applied: %+v", applied.Applied)
	}
	// Invalid mode → 422, nothing written.
	env.do("POST", "/v1/clients/"+cid+"/ride-mode", env.tenantID,
		map[string]string{"mode": "rave"}, http.StatusUnprocessableEntity, nil)

	// Rider self-serve via the tracking link.
	bookingID := seedBookingForClient58(t, env, cid)
	assignmentID := assignFleet58(t, env, bookingID)
	trackToken := env.svc.Trips.ClientTokenFor(env.tenantID, assignmentID)
	applied = struct {
		Applied map[string]string `json:"applied"`
	}{}
	env.doBearer("POST", "/v1/track/"+trackToken+"/ride-mode", "",
		map[string]string{"mode": "celebrate"}, http.StatusOK, &applied)
	if applied.Applied["ride.mode"] != "celebrate" || applied.Applied["ride.conversation"] != "chatty" {
		t.Fatalf("self-serve applied: %+v", applied.Applied)
	}

	// The chauffeur trip view carries the comfort block AND prep tasks.
	env.do("PUT", "/v1/clients/"+cid+"/preferences/ride.child_seat", env.tenantID,
		map[string]string{"value": "toddler", "recorded_by": "test"}, http.StatusOK, nil)
	tripToken := env.svc.Trips.TokenFor(env.tenantID, assignmentID)
	var trip struct {
		Preferences map[string]string `json:"preferences"`
		PrepTasks   []string          `json:"prep_tasks"`
	}
	env.doBearer("GET", "/v1/trips/"+tripToken, "", nil, http.StatusOK, &trip)
	if trip.Preferences["ride.mode"] != "celebrate" {
		t.Fatalf("trip prefs: %+v", trip.Preferences)
	}
	joined := ""
	for _, task := range trip.PrepTasks {
		joined += task + ";"
	}
	if joined == "" {
		t.Fatal("no prep tasks on trip view")
	}
	foundSeat, foundTemp := false, false
	for _, task := range trip.PrepTasks {
		if task == "Install toddler child seat" {
			foundSeat = true
		}
		if task == "Set cabin to 21°C before pickup" {
			foundTemp = true
		}
	}
	if !foundSeat || !foundTemp {
		t.Fatalf("prep tasks: %+v", trip.PrepTasks)
	}

	// Cross-token rejection: a trip token never applies a mode.
	env.doBearer("POST", "/v1/track/"+tripToken+"/ride-mode", "",
		map[string]string{"mode": "relax"}, http.StatusForbidden, nil)
}
