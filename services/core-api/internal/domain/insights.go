package domain

// Phase 30: tenant insights — the operational KPI layer answering "how is
// every part of my business doing": bookings and revenue, service mix,
// acquisition channels, fleet (cars) utilization, driver (crew) performance
// and review quality. Computed from the store spine on read — every number
// traces to real bookings/postings/assignments/reviews; nothing is sampled
// or estimated. The batch gold layer (analytics/, retention LTV / churn /
// channel SLA) complements this with warehouse-grade history; this is the
// real-time operational view.

import (
	"context"
	"fmt"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

type InsightsService struct{ s *Services }

func NewInsightsService(s *Services) *InsightsService { return &InsightsService{s: s} }

// InsightsOverview is the tenant dashboard payload. All money in minor
// units of the tenant's pricing currency; counts cover the window.
type InsightsOverview struct {
	Days   int       `json:"days"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	Totals struct {
		BookingsCreated   int    `json:"bookings_created"`
		BookingsConfirmed int    `json:"bookings_confirmed"`
		TripsCompleted    int    `json:"trips_completed"` // assignments COMPLETED_TRIP
		GMVMinor          int64  `json:"gmv_minor"`       // confirmed quote totals
		CollectedMinor    int64  `json:"collected_minor"` // TENANT_REVENUE credits
		Currency          string `json:"currency"`
		NewClients        int    `json:"new_clients"`
		ReviewCount       int    `json:"review_count"`
		ReviewAvg         string `json:"review_avg"` // "4.8", empty when no reviews
	} `json:"totals"`
	ByService []ServiceKPI `json:"by_service"`
	ByChannel []ChannelKPI `json:"by_channel"`
	Fleet     []FleetKPI   `json:"fleet"`
	Drivers   []DriverKPI  `json:"drivers"`
	// Coverage notes make the dashboard honest about what it cannot see.
	Notes []string `json:"notes"`
}

type ServiceKPI struct {
	ServiceID string `json:"service_id"`
	Name      string `json:"name"`
	Vertical  string `json:"vertical"`
	Bookings  int    `json:"bookings"`
	GMVMinor  int64  `json:"gmv_minor"`
}

type ChannelKPI struct {
	Channel  string `json:"channel"`
	Kind     string `json:"kind"`
	Count    int    `json:"count"`
	ValueMin int64  `json:"value_minor"`
}

type FleetKPI struct {
	ResourceID     string  `json:"resource_id"`
	Label          string  `json:"label"`
	Kind           string  `json:"kind"`
	Active         bool    `json:"active"`
	Assignments    int     `json:"assignments"`
	Completed      int     `json:"completed"`
	HoursBooked    float64 `json:"hours_booked"`
	UtilizationPct float64 `json:"utilization_pct"` // hours booked / (days × 10h service day)
}

type DriverKPI struct {
	CrewID      string  `json:"crew_id"`
	Name        string  `json:"name"`
	Active      bool    `json:"active"`
	Assignments int     `json:"assignments"`
	Completed   int     `json:"completed"`
	ReviewCount int     `json:"review_count"`
	ReviewAvg   float64 `json:"review_avg"` // 0 when unrated
}

// serviceDayHours is the utilization denominator assumption: one vehicle is
// bookable ~10h/day. Documented in the API response notes so the number is
// never mistaken for a measured capacity.
const serviceDayHours = 10.0

func (svc *InsightsService) Overview(ctx context.Context, tenantID string, days int) (*InsightsOverview, error) {
	if days <= 0 || days > 365 {
		days = 30
	}
	t, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	now := svc.s.Now()
	from := now.AddDate(0, 0, -days)
	o := &InsightsOverview{Days: days, From: from, To: now}
	o.Totals.Currency = t.PricingRules.Currency

	bookings, err := svc.s.Store.Bookings().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	quotes := map[string]*model.Quote{}
	requests := map[string]*model.ServiceRequest{}
	byService := map[string]*ServiceKPI{}
	for _, b := range bookings {
		if b.CreatedAt.Before(from) {
			continue
		}
		o.Totals.BookingsCreated++
		if b.State != "CONFIRMED" && b.State != "COMPLETED" {
			continue
		}
		o.Totals.BookingsConfirmed++
		q, ok := quotes[b.QuoteID]
		if !ok {
			q, err = svc.s.Store.Quotes().Get(ctx, tenantID, b.QuoteID)
			if err != nil {
				continue // missing quote rows don't break the dashboard
			}
			quotes[b.QuoteID] = q
		}
		o.Totals.GMVMinor += q.TotalMinor
		if o.Totals.Currency == "" {
			o.Totals.Currency = q.Currency
		}
		// service attribution: booking → request → first item vertical,
		// matched against the catalog for a display name.
		r, ok := requests[b.RequestID]
		if !ok {
			r, err = svc.s.Store.Requests().Get(ctx, tenantID, b.RequestID)
			if err != nil {
				continue
			}
			requests[b.RequestID] = r
		}
		if len(r.Items) > 0 {
			key := r.Items[0].Vertical
			row, ok := byService[key]
			if !ok {
				row = &ServiceKPI{Vertical: key, Name: humanizeVertical(key)}
				byService[key] = row
			}
			row.Bookings++
			row.GMVMinor += q.TotalMinor
		}
	}
	// upgrade service rows with catalog names/IDs when the vertical matches
	if catalog, err := svc.s.Store.Catalog().List(ctx, tenantID); err == nil {
		for _, cs := range catalog {
			if row, ok := byService[cs.Vertical]; ok {
				row.Name, row.ServiceID = cs.Name, cs.ID
			}
		}
	}
	for _, row := range byService {
		o.ByService = append(o.ByService, *row)
	}

	// revenue actually collected (credits into TENANT_REVENUE in window)
	postings, err := svc.s.Store.Payments().Postings(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	for _, p := range postings {
		if p.CreatedAt.Before(from) {
			continue
		}
		if p.CreditCode == "TENANT_REVENUE" {
			o.Totals.CollectedMinor += p.AmountMinor
		}
	}

	// new clients in window
	clients, err := svc.s.Store.Clients().List(ctx, tenantID, 100000)
	if err != nil {
		return nil, err
	}
	for _, c := range clients {
		if !c.CreatedAt.Before(from) {
			o.Totals.NewClients++
		}
	}

	// acquisition channels (reuse the Phase 14 funnel spine)
	if funnel, err := svc.s.Store.Attribution().Funnel(ctx, tenantID); err == nil {
		for _, f := range funnel {
			if f.Channel == "" {
				continue
			}
			o.ByChannel = append(o.ByChannel, ChannelKPI{
				Channel: f.Channel, Kind: f.Kind, Count: f.Count, ValueMin: f.ValueMinor,
			})
		}
	}

	// fleet + drivers from assignments in window
	assignments, err := svc.s.Store.Dispatch().ListAssignments(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	fleet, err := svc.s.Store.Dispatch().ListFleet(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	crew, err := svc.s.Store.Dispatch().ListCrew(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	fleetKPI := map[string]*FleetKPI{}
	for _, f := range fleet {
		fleetKPI[f.ID] = &FleetKPI{ResourceID: f.ID, Label: f.Label, Kind: f.Kind, Active: f.Active}
	}
	driverKPI := map[string]*DriverKPI{}
	for _, c := range crew {
		driverKPI[c.ID] = &DriverKPI{CrewID: c.ID, Name: c.FullName, Active: c.Active}
	}
	for _, a := range assignments {
		if a.CreatedAt.Before(from) {
			continue
		}
		if a.Status == "COMPLETED_TRIP" {
			o.Totals.TripsCompleted++
		}
		if fk, ok := fleetKPI[a.FleetResourceID]; ok {
			fk.Assignments++
			if a.Status == "COMPLETED_TRIP" {
				fk.Completed++
			}
			if !a.StartAt.IsZero() && a.EndAt.After(a.StartAt) {
				fk.HoursBooked += a.EndAt.Sub(a.StartAt).Hours()
			}
		}
		if dk, ok := driverKPI[a.CrewMemberID]; ok {
			dk.Assignments++
			if a.Status == "COMPLETED_TRIP" {
				dk.Completed++
			}
		}
	}
	for _, fk := range fleetKPI {
		denom := float64(days) * serviceDayHours
		if denom > 0 {
			fk.UtilizationPct = fk.HoursBooked / denom * 100
			if fk.UtilizationPct > 100 {
				fk.UtilizationPct = 100
			}
		}
		o.Fleet = append(o.Fleet, *fk)
	}

	// reviews: tenant totals + per-driver via assignment→crew join
	reviews, err := svc.s.Store.Reviews().ListAll(ctx, tenantID, 10000)
	if err != nil {
		return nil, err
	}
	crewByAssignment := map[string]string{}
	for _, a := range assignments {
		crewByAssignment[a.ID] = a.CrewMemberID
	}
	var ratingSum, ratingN int
	for _, rv := range reviews {
		if rv.CreatedAt.Before(from) {
			continue
		}
		ratingSum += rv.Rating
		ratingN++
		if dk, ok := driverKPI[crewByAssignment[rv.AssignmentID]]; ok {
			dk.ReviewAvg = (dk.ReviewAvg*float64(dk.ReviewCount) + float64(rv.Rating)) / float64(dk.ReviewCount+1)
			dk.ReviewCount++
		}
	}
	if ratingN > 0 {
		o.Totals.ReviewCount = ratingN
		o.Totals.ReviewAvg = fmt.Sprintf("%.1f", float64(ratingSum)/float64(ratingN))
	}
	for _, dk := range driverKPI {
		o.Drivers = append(o.Drivers, *dk)
	}

	o.Notes = []string{
		fmt.Sprintf("Utilization assumes a %.0fh bookable service day per vehicle.", serviceDayHours),
		"GMV counts confirmed bookings; collected counts settled TENANT_REVENUE postings.",
		"Historical trends and ML metrics (LTV, churn, channel SLA) live in the analytics gold layer (nightly batch).",
	}
	return o, nil
}
