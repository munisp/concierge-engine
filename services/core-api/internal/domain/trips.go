// Chauffeur trip links (chauffeur app v1): token-authenticated, PWA-grade
// web flow. The token carries tenant+assignment and is HMAC-signed with
// CE_TRIP_SECRET, so chauffeurs need no account and no tenant header —
// the magic link IS the credential. Links render trip details, status
// advance buttons and one-tap geostamps.
package domain

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type TripService struct {
	s      *Services
	secret []byte
}

func NewTripService(s *Services, secret string) *TripService {
	if secret == "" {
		secret = "dev-trip-secret"
	}
	return &TripService{s: s, secret: []byte(secret)}
}

// TripView is the privacy-minimized chauffeur view: no client PII beyond a
// masked name, no pricing, no payment data.
type TripView struct {
	AssignmentID string   `json:"assignment_id"`
	Status       string   `json:"status"`
	AllowedNext  []string `json:"allowed_next"`
	Pickup       string   `json:"pickup_address"`
	Dropoff      string   `json:"dropoff_address,omitempty"`
	PickupAt     int64    `json:"pickup_at"`
	Passengers   int      `json:"passengers"`
	VehicleClass string   `json:"vehicle_class,omitempty"`
	ClientName   string   `json:"client_name"` // masked: "J. de V***"
	Notes        string   `json:"notes,omitempty"`
	// Phase 58: the rider comfort block — curated ride.* / access / language
	// preferences surfaced to the person delivering the service. Absent
	// when the client has set nothing (never an empty card).
	Preferences map[string]string `json:"preferences,omitempty"`
	// Phase 60: the chauffeur's pre-pickup checklist derived from the
	// comfort block (set cabin to 21°C, install toddler seat…).
	PrepTasks []string `json:"prep_tasks,omitempty"`
	// Phase 61: journey context — the flight this trip meets, when linked.
	Journey *JourneyContext `json:"journey,omitempty"`
}

// JourneyContext (Phase 61): the flight leg chained to this ground trip —
// status, delay, ETA, and the adjusted pickup after a cascade.
type JourneyContext struct {
	FlightNumber   string `json:"flight_number"`
	FlightStatus   string `json:"flight_status"`
	DelayMinutes   int    `json:"delay_minutes,omitempty"`
	ETA            string `json:"eta,omitempty"`
	AdjustedPickup string `json:"adjusted_pickup,omitempty"`
}

// TokenFor mints the chauffeur magic-link token for an assignment.
func (svc *TripService) TokenFor(tenantID, assignmentID string) string {
	payload := tenantID + "/" + assignmentID
	mac := hmac.New(sha256.New, svc.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac.Sum(nil))
}

// ParseToken verifies and extracts (tenantID, assignmentID). Client
// tracking tokens (track/ prefix, Phase 25) and quote share tokens
// (quote/ prefix, Phase 35) are rejected here — neither a monitoring link
// nor a priced offer may ever drive the trip.
func (svc *TripService) ParseToken(token string) (string, string, error) {
	_, _, payload, err := svc.parseAny(token)
	if err != nil {
		return "", "", err
	}
	if strings.HasPrefix(payload, clientTokenPrefix) || strings.HasPrefix(payload, quoteTokenPrefix) ||
		strings.HasPrefix(payload, partnerTokenPrefix) {
		return "", "", httpx.ErrForbidden
	}
	return splitPayload(payload)
}

func (svc *TripService) parseAny(token string) (string, string, string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", "", "", httpx.ErrForbidden
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", "", "", httpx.ErrForbidden
	}
	mac := hmac.New(sha256.New, svc.secret)
	mac.Write(payload)
	want := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(want), []byte(parts[1])) != 1 {
		return "", "", "", httpx.ErrForbidden
	}
	return "", "", string(payload), nil
}

func splitPayload(payload string) (string, string, error) {
	kv := strings.SplitN(payload, "/", 2)
	if len(kv) != 2 || kv[0] == "" || kv[1] == "" {
		return "", "", httpx.ErrForbidden
	}
	return kv[0], kv[1], nil
}

// View renders the privacy-minimized trip for a verified token.
func (svc *TripService) View(ctx context.Context, token string) (*TripView, error) {
	tenantID, assignmentID, err := svc.ParseToken(token)
	if err != nil {
		return nil, err
	}
	assignments, err := svc.s.Store.Dispatch().ListAssignments(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var a *model.Assignment
	for i := range assignments {
		if assignments[i].ID == assignmentID {
			a = &assignments[i]
			break
		}
	}
	if a == nil {
		return nil, httpx.ErrNotFound
	}
	view := &TripView{
		AssignmentID: a.ID, Status: a.Status, AllowedNext: tripStatusFlow[a.Status],
	}
	// Join booking -> request -> first item for addresses/timing, client for
	// the masked name. All best-effort: the trip renders even if a join fails.
	if b, err := svc.s.Store.Bookings().Get(ctx, tenantID, a.BookingID); err == nil {
		if sr, err := svc.s.Store.Requests().Get(ctx, tenantID, b.RequestID); err == nil {
			if len(sr.Items) > 0 {
				it := sr.Items[0]
				view.Pickup = it.PickupAddress
				view.Dropoff = it.DropoffAddress
				view.PickupAt = it.PickupAt
				view.Passengers = it.Passengers
				view.VehicleClass = it.VehicleClass
				view.Notes = it.Notes
			}
			if c, err := svc.s.Store.Clients().Get(ctx, tenantID, sr.ClientID); err == nil {
				view.ClientName = maskName(c.FullName)
				// Phase 58: rider comfort block, best-effort like the rest
				// of the join — the trip renders regardless.
				if prefs, err := svc.s.Clients.RidePreferenceMap(ctx, tenantID, sr.ClientID); err == nil {
					view.Preferences = prefs
					view.PrepTasks = PrepTasks(prefs)
				}
				// Phase 61: journey context — the flight this trip meets.
				if legs, err := svc.s.Store.Itineraries().ForBooking(ctx, tenantID, b.ID); err == nil {
					for _, gl := range legs {
						if gl.LinkedLegID == "" {
							continue
						}
						if fl, err := svc.s.Store.Itineraries().Get(ctx, tenantID, gl.LinkedLegID); err == nil {
							jc := &JourneyContext{FlightNumber: fl.FlightNumber, FlightStatus: fl.Status,
								DelayMinutes: fl.DelayMinutes}
							if !fl.ActualStart.IsZero() {
								jc.ETA = fl.ActualStart.UTC().Format("2006-01-02T15:04:05Z")
							}
							if !gl.ActualStart.IsZero() {
								jc.AdjustedPickup = gl.ActualStart.UTC().Format("2006-01-02T15:04:05Z")
							}
							view.Journey = jc
						}
					}
				}
			}
		}
	}
	return view, nil
}

// Advance moves the trip status machine via token (same transition rules as
// the dispatcher path; events and SSE broadcast included).
func (svc *TripService) Advance(ctx context.Context, token, status string) error {
	tenantID, assignmentID, err := svc.ParseToken(token)
	if err != nil {
		return err
	}
	return svc.s.Dispatch.UpdateStatus(ctx, tenantID, assignmentID, status)
}

// Stamp records a chauffeur geostamp via token.
func (svc *TripService) Stamp(ctx context.Context, token, kind string, lat, lng, accuracy float64) error {
	tenantID, assignmentID, err := svc.ParseToken(token)
	if err != nil {
		return err
	}
	if kind == "" {
		kind = "PING"
	}
	_, err = svc.s.Dispatch.GeoStamp(ctx, &model.GeoStamp{
		TenantID: tenantID, AssignmentID: assignmentID, Kind: strings.ToUpper(kind),
		Lat: lat, Lng: lng, AccuracyM: accuracy, At: svc.s.Now(),
	})
	return err
}

// maskName: "James de Vries" -> "James d. V***" — chauffeurs need to
// recognize, not to identify.
func maskName(full string) string {
	full = strings.TrimSpace(full)
	if full == "" {
		return "client"
	}
	parts := strings.Fields(full)
	if len(parts) == 1 {
		return parts[0]
	}
	last := parts[len(parts)-1]
	if len(last) > 2 {
		last = last[:1] + "***"
	}
	return strings.Join(append(parts[:len(parts)-1], last), " ")
}

// ------------------------------------------------------------- client tracking (Phase 25)
//
// The client side of the trip: the same HMAC token discipline as the
// chauffeur link, but a distinct "track/" payload prefix — a monitoring
// link can never advance status or stamp positions (ParseToken rejects it).

const clientTokenPrefix = "track/"

// ------------------------------------------------------------- partner portal (Phase 57)
//
// The partner side of the network: the same HMAC token discipline as the
// chauffeur and client links, with a distinct "partner/" payload prefix —
// a partner portal token can never drive a trip or monitor one, and
// neither of those can open the partner portal (ParseToken/ParseClientToken
// reject it below).

const partnerTokenPrefix = "partner/"

// PartnerTokenFor mints the partner portal magic-link token. Stateless HMAC,
// no expiry — revocation is operational: suspending the partner closes the
// portal (the portal view checks partner status on every request).
func (svc *TripService) PartnerTokenFor(tenantID, partnerID string) string {
	payload := partnerTokenPrefix + tenantID + "/" + partnerID
	mac := hmac.New(sha256.New, svc.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac.Sum(nil))
}

// ParsePartnerToken verifies a portal token and extracts
// (tenantID, partnerID). Chauffeur and client tokens are rejected here.
func (svc *TripService) ParsePartnerToken(token string) (string, string, error) {
	_, _, payload, err := svc.parseAny(token)
	if err != nil {
		return "", "", err
	}
	rest, ok := strings.CutPrefix(payload, partnerTokenPrefix)
	if !ok {
		return "", "", httpx.ErrForbidden
	}
	return splitPayload(rest)
}

// ClientTokenFor mints the client tracking token for an assignment.
func (svc *TripService) ClientTokenFor(tenantID, assignmentID string) string {
	payload := clientTokenPrefix + tenantID + "/" + assignmentID
	mac := hmac.New(sha256.New, svc.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac.Sum(nil))
}

// ParseClientToken verifies a tracking token and extracts
// (tenantID, assignmentID). Driver tokens are rejected here.
func (svc *TripService) ParseClientToken(token string) (string, string, error) {
	_, _, payload, err := svc.parseAny(token)
	if err != nil {
		return "", "", err
	}
	rest, ok := strings.CutPrefix(payload, clientTokenPrefix)
	if !ok {
		return "", "", httpx.ErrForbidden
	}
	return splitPayload(rest)
}

// TrackView is what the client sees: their own trip status, the vehicle and
// crew label, and the latest position. Clients get full addresses (it is
// their journey); the crew member is first-name only.
type TrackView struct {
	AssignmentID string    `json:"assignment_id"`
	Status       string    `json:"status"`
	Pickup       string    `json:"pickup_address"`
	Dropoff      string    `json:"dropoff_address,omitempty"`
	PickupAt     int64     `json:"pickup_at"`
	VehicleClass string    `json:"vehicle_class,omitempty"`
	VehicleLabel string    `json:"vehicle_label,omitempty"`
	DriverName   string    `json:"driver_name,omitempty"` // first name only
	WindowStart  time.Time `json:"window_start,omitempty"`
	WindowEnd    time.Time `json:"window_end,omitempty"`
	// Position is nil until the chauffeur's first stamp — the page says
	// "position pending", never invents one.
	Position *TrackPosition `json:"position,omitempty"`
	// Phase 35 (A11): review deep link — the anchor URL of the review form
	// on this very page, plus whether a review already landed (the form
	// hides when one has; one review per assignment structurally).
	ReviewURL       string `json:"review_url,omitempty"`
	ReviewSubmitted bool   `json:"review_submitted"`
}

type TrackPosition struct {
	Lat  float64   `json:"lat"`
	Lng  float64   `json:"lng"`
	Kind string    `json:"kind"`
	At   time.Time `json:"at"`
}

// Track renders the client view for a verified tracking token.
func (svc *TripService) Track(ctx context.Context, token string) (*TrackView, error) {
	tenantID, assignmentID, err := svc.ParseClientToken(token)
	if err != nil {
		return nil, err
	}
	assignments, err := svc.s.Store.Dispatch().ListAssignments(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var a *model.Assignment
	for i := range assignments {
		if assignments[i].ID == assignmentID {
			a = &assignments[i]
			break
		}
	}
	if a == nil {
		return nil, httpx.ErrNotFound
	}
	view := &TrackView{
		AssignmentID: a.ID, Status: a.Status, WindowStart: a.StartAt, WindowEnd: a.EndAt,
	}
	// Join booking -> request -> first item for addresses/timing.
	if b, err := svc.s.Store.Bookings().Get(ctx, tenantID, a.BookingID); err == nil {
		if sr, err := svc.s.Store.Requests().Get(ctx, tenantID, b.RequestID); err == nil && len(sr.Items) > 0 {
			it := sr.Items[0]
			view.Pickup = it.PickupAddress
			view.Dropoff = it.DropoffAddress
			view.PickupAt = it.PickupAt
			view.VehicleClass = it.VehicleClass
		}
	}
	// Vehicle + crew labels, best-effort.
	if a.FleetResourceID != "" {
		if fleet, err := svc.s.Store.Dispatch().ListFleet(ctx, tenantID); err == nil {
			for _, f := range fleet {
				if f.ID == a.FleetResourceID {
					view.VehicleLabel = f.Label
				}
			}
		}
	}
	if a.CrewMemberID != "" {
		if crew, err := svc.s.Store.Dispatch().ListCrew(ctx, tenantID); err == nil {
			for _, c := range crew {
				if c.ID == a.CrewMemberID {
					view.DriverName = firstName(c.FullName)
				}
			}
		}
	}
	// Latest position, best-effort (nil until the first stamp).
	if g, err := svc.s.Store.Dispatch().LatestGeoStamp(ctx, tenantID, a.ID); err == nil && g != nil {
		view.Position = &TrackPosition{Lat: g.Lat, Lng: g.Lng, Kind: g.Kind, At: g.At}
	}
	// Phase 35 (A11): review deep link once the trip is reviewable
	// (completed). The URL anchors the review form on this track page.
	if a.Status == "COMPLETED_TRIP" {
		view.ReviewURL = svc.s.ConsoleBase() + "/track?token=" + token + "#review"
		if rv, err := svc.s.Store.Reviews().GetByAssignment(ctx, tenantID, a.ID); err == nil && rv != nil {
			view.ReviewSubmitted = true
		}
	}
	return view, nil
}

func firstName(full string) string {
	if parts := strings.Fields(strings.TrimSpace(full)); len(parts) > 0 {
		return parts[0]
	}
	return ""
}
