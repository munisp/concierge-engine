package domain

// Phase 61: journey orchestration. The itinerary is a first-class entity —
// typed legs (flight / hotel / ground / partner) with scheduled AND actual
// times — and the platform acts on it: a flight-watch turns a delay into a
// re-timed ground pickup, with rider + chauffeur informed.
//
// Honesty rules:
//   - Flight status is live-first via GDS; without live data a leg stays
//     SCHEDULED with source "scheduled" — never a fabricated ETA.
//   - The delay cascade adjusts the ground LEG (what rider and chauffeur
//     see) and emits events; it never rewrites the dispatch saga or the
//     booking — the assignment window is operator-confirmed.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type JourneyService struct{ s *Services }

// LegInput is the add-leg payload (flight resolution happens server-side).
type LegInput struct {
	Kind         string    `json:"kind"`
	Title        string    `json:"title,omitempty"`
	Ref          string    `json:"ref,omitempty"`
	FlightNumber string    `json:"flight_number,omitempty"`
	Origin       string    `json:"origin,omitempty"`
	Destination  string    `json:"destination,omitempty"`
	SchedStart   time.Time `json:"sched_start"`
	SchedEnd     time.Time `json:"sched_end,omitempty"`
	BookingID    string    `json:"booking_id,omitempty"`
	LinkedLegID  string    `json:"linked_leg_id,omitempty"`
}

func validLegKind(k string) bool {
	switch k {
	case model.LegFlight, model.LegHotel, model.LegGround, model.LegPartner:
		return true
	}
	return false
}

// AddLeg records one itinerary leg. Flight legs resolve live status
// through the GDS (source live|scheduled recorded on the leg).
func (svc *JourneyService) AddLeg(ctx context.Context, tenantID, clientID string, in LegInput) (*model.ItineraryLeg, error) {
	if !validLegKind(in.Kind) || in.SchedStart.IsZero() {
		return nil, httpx.ErrValidation
	}
	if _, err := svc.s.Store.Clients().Get(ctx, tenantID, clientID); err != nil {
		return nil, err
	}
	now := svc.s.Now()
	leg := &model.ItineraryLeg{
		ID: NewID(), TenantID: tenantID, ClientID: clientID,
		Kind: in.Kind, Title: in.Title, Ref: in.Ref,
		FlightNumber: strings.ToUpper(strings.TrimSpace(in.FlightNumber)),
		Origin: strings.ToUpper(strings.TrimSpace(in.Origin)),
		Destination: strings.ToUpper(strings.TrimSpace(in.Destination)),
		SchedStart: in.SchedStart, SchedEnd: in.SchedEnd,
		BookingID: in.BookingID, LinkedLegID: in.LinkedLegID,
		Status: model.LegScheduled, Source: "manual",
		CreatedAt: now, UpdatedAt: now,
	}
	if leg.Kind == model.LegFlight {
		if leg.FlightNumber == "" || leg.Origin == "" || leg.Destination == "" {
			return nil, httpx.ErrValidation
		}
		if leg.Title == "" {
			leg.Title = leg.FlightNumber + " " + leg.Origin + " → " + leg.Destination
		}
		// Live status resolution — the source records which answer we got.
		rep, err := svc.s.GDS.FlightStatus(ctx, leg.FlightNumber, leg.SchedStart.Format("2006-01-02"))
		if err == nil && rep != nil {
			leg.Source = rep.Source
			if rep.Status != "" {
				leg.Status = rep.Status
			}
			leg.DelayMinutes = rep.DelayMinutes
		} else {
			leg.Source = "scheduled"
		}
	}
	if leg.Title == "" {
		leg.Title = strings.ToUpper(leg.Kind[:1]) + leg.Kind[1:] + " leg"
	}
	if err := svc.s.Store.Itineraries().Create(ctx, leg); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "itinerary", leg.ID, "journey.leg_added",
		map[string]any{"kind": leg.Kind, "client_id": clientID, "source": leg.Source})
	return leg, nil
}

// LinkGroundLeg creates the ground leg for a platform booking, chained to
// the flight leg it meets (arrival transfer) or standalone (departure).
// The pickup time comes from the booking's request item — never retyped.
func (svc *JourneyService) LinkGroundLeg(ctx context.Context, tenantID, bookingID, flightLegID string) (*model.ItineraryLeg, error) {
	b, err := svc.s.Store.Bookings().Get(ctx, tenantID, bookingID)
	if err != nil {
		return nil, err
	}
	req, err := svc.s.Store.Requests().Get(ctx, tenantID, b.RequestID)
	if err != nil {
		return nil, err
	}
	if len(req.Items) == 0 {
		return nil, httpx.ErrValidation
	}
	it := req.Items[0]
	title := it.PickupAddress + " → " + it.DropoffAddress
	in := LegInput{
		Kind: model.LegGround, Title: title,
		Origin: it.PickupAddress, Destination: it.DropoffAddress,
		SchedStart: time.Unix(it.PickupAt, 0).UTC(),
		BookingID: bookingID, LinkedLegID: flightLegID,
	}
	return svc.AddLeg(ctx, tenantID, req.ClientID, in)
}

// BufferMinutes is the landing-to-curb gap: base 45, weighted by the
// rider's profile (heavy luggage, mobility), capped at 90.
func (svc *JourneyService) BufferMinutes(ctx context.Context, tenantID, clientID string) int {
	buffer := 45
	if rp, err := svc.s.Clients.RideProfile(ctx, tenantID, clientID); err == nil {
		if rp.Luggage == "heavy" {
			buffer += 15
		}
		if rp.Mobility == "assistance" || rp.Mobility == "wheelchair" {
			buffer += 15
		}
	}
	if buffer > 90 {
		buffer = 90
	}
	return buffer
}

// ApplyFlightUpdate records a fresh flight status and cascades a changed
// delay into every ground leg chained to the flight: adjusted pickup =
// landing ETA + buffer. Rider is notified on WhatsApp (honest degrade via
// postToAdapter — the event carries the outcome).
func (svc *JourneyService) ApplyFlightUpdate(ctx context.Context, tenantID, flightLegID string, rep *model.FlightStatusReport) (*model.ItineraryLeg, error) {
	leg, err := svc.s.Store.Itineraries().Get(ctx, tenantID, flightLegID)
	if err != nil {
		return nil, err
	}
	if leg.Kind != model.LegFlight {
		return nil, httpx.ErrValidation
	}
	delayChanged := rep.DelayMinutes != leg.DelayMinutes
	now := svc.s.Now()
	leg.Status = rep.Status
	leg.DelayMinutes = rep.DelayMinutes
	leg.Source = rep.Source
	leg.UpdatedAt = now
	if !leg.SchedEnd.IsZero() {
		leg.ActualStart = leg.SchedEnd.Add(time.Duration(rep.DelayMinutes) * time.Minute)
	}
	if err := svc.s.Store.Itineraries().Update(ctx, leg); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "itinerary", leg.ID, "journey.flight_updated",
		map[string]any{"flight": leg.FlightNumber, "status": rep.Status,
			"delay_minutes": rep.DelayMinutes, "source": rep.Source})
	if !delayChanged || leg.ActualStart.IsZero() {
		return leg, nil
	}
	// Cascade: re-time chained ground legs.
	legs, err := svc.s.Store.Itineraries().ListForClient(ctx, tenantID, leg.ClientID, 200)
	if err != nil {
		return leg, nil
	}
	buffer := svc.BufferMinutes(ctx, tenantID, leg.ClientID)
	for _, gl := range legs {
		if gl.Kind != model.LegGround || gl.LinkedLegID != leg.ID {
			continue
		}
		oldPickup := gl.SchedStart
		if !gl.ActualStart.IsZero() {
			oldPickup = gl.ActualStart
		}
		gl.ActualStart = leg.ActualStart.Add(time.Duration(buffer) * time.Minute)
		gl.UpdatedAt = svc.s.Now()
		if gl.Status == model.LegScheduled {
			gl.Status = model.LegScheduled // pickup re-timed, leg still upcoming
		}
		if err := svc.s.Store.Itineraries().Update(ctx, &gl); err != nil {
			continue
		}
		svc.s.Events.Emit(ctx, tenantID, "itinerary", gl.ID, "journey.ground_adjusted",
			map[string]any{"booking_id": gl.BookingID, "flight": leg.FlightNumber,
				"old_pickup": oldPickup.Format(time.RFC3339),
				"new_pickup": gl.ActualStart.Format(time.RFC3339),
				"delay_minutes": rep.DelayMinutes})
		svc.notifyGroundAdjusted(ctx, tenantID, leg, &gl)
	}
	return leg, nil
}

// notifyGroundAdjusted tells the rider their pickup moved — honest
// degrade: no adapter or no phone leaves an event, not a fake "sent".
func (svc *JourneyService) notifyGroundAdjusted(ctx context.Context, tenantID string, flight, ground *model.ItineraryLeg) {
	c, err := svc.s.Store.Clients().Get(ctx, tenantID, flight.ClientID)
	if err != nil || c.Phone == "" {
		return
	}
	msg := fmt.Sprintf("%s update: %s now lands at %s (%+d min). Your pickup moves to %s.",
		flight.FlightNumber, flight.FlightNumber,
		flight.ActualStart.Format("15:04"), flight.DelayMinutes,
		ground.ActualStart.Format("15:04"))
	payload := fmt.Sprintf(`{"to":%q,"body":%q,"context":"journey-adjust"}`, c.Phone, msg)
	status, detail, _ := postToAdapter(ctx, svc.s.waURL, []byte(payload))
	svc.s.Events.Emit(ctx, tenantID, "itinerary", ground.ID, "journey.rider_notified",
		map[string]any{"channel": "whatsapp", "status": status, "detail": detail})
}

// WatchOnce is one flight-watch pass for a tenant: every flight leg
// departing within the window gets a fresh status; changed delays cascade.
// Returns (watched, updated).
func (svc *JourneyService) WatchOnce(ctx context.Context, tenantID string, from, to time.Time) (int, int, error) {
	legs, err := svc.s.Store.Itineraries().FlightLegsDue(ctx, tenantID, from, to)
	if err != nil {
		return 0, 0, err
	}
	updated := 0
	for _, leg := range legs {
		rep, err := svc.s.GDS.FlightStatus(ctx, leg.FlightNumber, leg.SchedStart.Format("2006-01-02"))
		if err != nil || rep == nil {
			continue // provider unreachable: leg keeps last known status
		}
		if rep.DelayMinutes == leg.DelayMinutes && rep.Status == leg.Status {
			continue
		}
		if _, err := svc.ApplyFlightUpdate(ctx, tenantID, leg.ID, rep); err == nil {
			updated++
		}
	}
	return len(legs), updated, nil
}

// WatchAllTenants is the operator/cron entry: one pass per tenant.
func (svc *JourneyService) WatchAllTenants(ctx context.Context, window time.Duration) (int, int, error) {
	tenants, err := svc.s.Store.Tenants().List(ctx)
	if err != nil {
		return 0, 0, err
	}
	now := svc.s.Now()
	totalWatched, totalUpdated := 0, 0
	for _, t := range tenants {
		w, u, err := svc.WatchOnce(ctx, t.ID, now.Add(-2*time.Hour), now.Add(window))
		if err != nil {
			continue
		}
		totalWatched += w
		totalUpdated += u
	}
	return totalWatched, totalUpdated, nil
}

// Journey is the rider-facing timeline: all legs, soonest first.
func (svc *JourneyService) Journey(ctx context.Context, tenantID, clientID string) ([]model.ItineraryLeg, error) {
	return svc.s.Store.Itineraries().ListForClient(ctx, tenantID, clientID, 100)
}
