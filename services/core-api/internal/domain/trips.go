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
}

// TokenFor mints the chauffeur magic-link token for an assignment.
func (svc *TripService) TokenFor(tenantID, assignmentID string) string {
	payload := tenantID + "/" + assignmentID
	mac := hmac.New(sha256.New, svc.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac.Sum(nil))
}

// ParseToken verifies and extracts (tenantID, assignmentID).
func (svc *TripService) ParseToken(token string) (string, string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", "", httpx.ErrForbidden
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", "", httpx.ErrForbidden
	}
	mac := hmac.New(sha256.New, svc.secret)
	mac.Write(payload)
	want := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(want), []byte(parts[1])) != 1 {
		return "", "", httpx.ErrForbidden
	}
	kv := strings.SplitN(string(payload), "/", 2)
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
