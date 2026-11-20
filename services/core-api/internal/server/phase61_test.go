package server_test

// Phase 61: journey-orchestration endpoints — add legs, link ground legs,
// flight-update cascade, watch pass, and the rider's track journey view.

import (
	"net/http"
	"testing"
	"time"
)

func TestJourneyAPI61(t *testing.T) {
	env := newEnv(t)

	// A client.
	var res struct {
		Client struct {
			ID string `json:"id"`
		} `json:"client"`
	}
	env.do("POST", "/v1/clients/resolve", env.tenantID,
		map[string]string{"phone": "+2348055555555", "name": "Femi Kuti"}, http.StatusOK, &res)
	cid := res.Client.ID

	// Flight leg resolves through the GDS chain (mock in CI → source "scheduled").
	dep := time.Now().Add(20 * time.Hour).UTC() // inside the watch's 24h window
	arr := dep.Add(6 * time.Hour)
	var added struct {
		Leg struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
			Source string `json:"source"`
		} `json:"leg"`
	}
	env.do("POST", "/v1/clients/"+cid+"/journey/legs", env.tenantID, map[string]any{
		"kind": "flight", "flight_number": "ba75",
		"origin": "LHR", "destination": "LOS",
		"sched_start": dep, "sched_end": arr,
	}, http.StatusCreated, &added)
	if added.Leg.Title != "BA75 LHR → LOS" || added.Leg.Status != "SCHEDULED" {
		t.Fatalf("flight leg: %+v", added.Leg)
	}
	if added.Leg.Source != "scheduled" { // mock GDS labels provenance honestly
		t.Fatalf("expected scheduled provenance, got %q", added.Leg.Source)
	}
	flightLegID := added.Leg.ID

	// Bad payloads rejected at the edge.
	env.do("POST", "/v1/clients/"+cid+"/journey/legs", env.tenantID, map[string]any{
		"kind": "spacecraft", "sched_start": dep,
	}, http.StatusUnprocessableEntity, nil)
	env.do("POST", "/v1/clients/"+cid+"/journey/legs", env.tenantID, map[string]any{
		"kind": "flight", "origin": "LHR", "destination": "LOS", "sched_start": dep,
	}, http.StatusUnprocessableEntity, nil) // no flight number

	// Chain the booking's ground leg to the flight.
	bookingID := seedBookingForClient58(t, env, cid)
	var linked struct {
		Leg struct {
			ID          string `json:"id"`
			Kind        string `json:"kind"`
			BookingID   string `json:"booking_id"`
			LinkedLegID string `json:"linked_leg_id"`
		} `json:"leg"`
	}
	env.do("POST", "/v1/bookings/"+bookingID+"/journey/link", env.tenantID,
		map[string]string{"flight_leg_id": flightLegID}, http.StatusCreated, &linked)
	if linked.Leg.Kind != "ground" || linked.Leg.LinkedLegID != flightLegID || linked.Leg.BookingID != bookingID {
		t.Fatalf("linked ground leg: %+v", linked.Leg)
	}

	// Flight update: 40-minute delay cascades to the ground pickup.
	var upd struct {
		Leg struct {
			Status       string `json:"status"`
			DelayMinutes int    `json:"delay_minutes"`
		} `json:"leg"`
	}
	env.do("POST", "/v1/journeys/legs/"+flightLegID+"/flight-update", env.tenantID, map[string]any{
		"status": "DELAYED", "delay_minutes": 40,
	}, http.StatusOK, &upd)
	if upd.Leg.Status != "DELAYED" || upd.Leg.DelayMinutes != 40 {
		t.Fatalf("flight update: %+v", upd.Leg)
	}

	// Timeline shows both legs; ground pickup moved by delay + buffer.
	var journey struct {
		Legs []struct {
			Kind        string `json:"kind"`
			LinkedLegID string `json:"linked_leg_id"`
			ActualStart string `json:"actual_start"`
		} `json:"legs"`
	}
	env.do("GET", "/v1/clients/"+cid+"/journey", env.tenantID, nil, http.StatusOK, &journey)
	if len(journey.Legs) != 2 {
		t.Fatalf("journey legs: %+v", journey.Legs)
	}
	var groundActual time.Time
	for _, l := range journey.Legs {
		if l.Kind == "ground" {
			if l.ActualStart == "" {
				t.Fatalf("ground leg not cascaded: %+v", l)
			}
			groundActual, _ = time.Parse(time.RFC3339, l.ActualStart)
		}
	}
	// Expected: arrival + 40m delay + buffer (base 45, no comfort prefs).
	want := arr.Add(40*time.Minute + 45*time.Minute)
	if groundActual.IsZero() || groundActual.Sub(want).Abs() > time.Minute {
		t.Fatalf("cascaded pickup %v, want ~%v", groundActual, want)
	}

	// The chauffeur trip view carries the journey context.
	assignmentID := assignFleet58(t, env, bookingID)
	tripToken := env.svc.Trips.TokenFor(env.tenantID, assignmentID)
	var trip struct {
		Journey struct {
			FlightNumber   string `json:"flight_number"`
			FlightStatus   string `json:"flight_status"`
			DelayMinutes   int    `json:"delay_minutes"`
			AdjustedPickup string `json:"adjusted_pickup"`
		} `json:"journey"`
	}
	env.doBearer("GET", "/v1/trips/"+tripToken, "", nil, http.StatusOK, &trip)
	if trip.Journey.FlightNumber != "BA75" || trip.Journey.FlightStatus != "DELAYED" ||
		trip.Journey.DelayMinutes != 40 || trip.Journey.AdjustedPickup == "" {
		t.Fatalf("trip journey context: %+v", trip.Journey)
	}

	// The rider's track page sees the same timeline.
	trackToken := env.svc.Trips.ClientTokenFor(env.tenantID, assignmentID)
	var riderJourney struct {
		Legs []struct {
			Kind string `json:"kind"`
		} `json:"legs"`
	}
	env.doBearer("GET", "/v1/track/"+trackToken+"/journey", "", nil, http.StatusOK, &riderJourney)
	if len(riderJourney.Legs) != 2 {
		t.Fatalf("track journey: %+v", riderJourney.Legs)
	}

	// One watch pass: mock reports on-time → delay resets to 0 → cascade again.
	var watch struct {
		Watched int `json:"watched"`
		Updated int `json:"updated"`
	}
	env.do("POST", "/v1/journeys/watch", env.tenantID, nil, http.StatusOK, &watch)
	if watch.Watched != 1 || watch.Updated != 1 {
		t.Fatalf("watch: %+v", watch)
	}
}
