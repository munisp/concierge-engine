package model

import "time"

// Phase 61: journey orchestration — the itinerary as a first-class entity.
// A trip is typed legs (flight / hotel / ground / partner) with scheduled
// AND actual times; when an upstream leg slips, downstream legs re-time.
// Source records provenance per leg: "live" (GDS), "manual" (operator),
// "scheduled" (no live status available — never a fabricated ETA).

const (
	LegFlight  = "flight"
	LegHotel   = "hotel"
	LegGround  = "ground"
	LegPartner = "partner"

	LegScheduled = "SCHEDULED"
	LegEnRoute   = "EN_ROUTE"
	LegDelayed   = "DELAYED"
	LegArrived   = "ARRIVED"
	LegCompleted = "COMPLETED"
	LegCancelled = "CANCELLED"
)

type ItineraryLeg struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	ClientID string `json:"client_id"`
	// BookingID links a ground leg to its platform booking (empty for
	// flight/hotel/partner legs).
	BookingID string `json:"booking_id,omitempty"`
	// LinkedLegID chains legs — an arrival-transfer ground leg points at
	// the flight leg it meets, so a delay can cascade.
	LinkedLegID  string    `json:"linked_leg_id,omitempty"`
	Kind         string    `json:"kind"`
	Title        string    `json:"title"` // "BA75 LHR → LOS", "Eko Hotel (check-out 11:00)"
	Ref          string    `json:"ref,omitempty"` // PNR / confirmation
	FlightNumber string    `json:"flight_number,omitempty"`
	Origin       string    `json:"origin,omitempty"`
	Destination  string    `json:"destination,omitempty"`
	SchedStart   time.Time `json:"sched_start"`
	SchedEnd     time.Time `json:"sched_end,omitempty"`
	// ActualStart is the live value once known — flight ETA, or the
	// adjusted pickup time after a delay cascade. Zero = on schedule.
	ActualStart  time.Time `json:"actual_start,omitempty"`
	Status       string    `json:"status"`
	DelayMinutes int       `json:"delay_minutes,omitempty"`
	Source       string    `json:"source"` // live | manual | scheduled
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// FlightStatusReport is what a GDS flight-status lookup returns. Source
// labels provenance: "live" when the provider answered, "scheduled" when
// only timetable data exists — the platform never invents an ETA.
type FlightStatusReport struct {
	FlightNumber string `json:"flight_number"`
	Date         string `json:"date"`
	Status       string `json:"status"` // Leg* statuses
	DelayMinutes int    `json:"delay_minutes"`
	ETALocal     string `json:"eta_local,omitempty"` // ISO local time when known
	Terminal     string `json:"terminal,omitempty"`
	Gate         string `json:"gate,omitempty"`
	Source       string `json:"source"` // live | scheduled
}
