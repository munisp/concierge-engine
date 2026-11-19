package model

import "time"

// ------------------------------------------------------------- Phase 3 models

// ApiKey is a tenant-scoped public-API credential (public API + webhooks
// marketplace). The secret is shown once at creation; only the SHA-256 hash
// is stored. Scope strings gate the public surface ("read", "write",
// "webhooks:manage").
type ApiKey struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	Name       string    `json:"name"`
	Prefix     string    `json:"prefix"` // "cek_" + 8 — safe to log/display
	SecretHash string    `json:"-"`
	Scopes     []string  `json:"scopes"`
	Revoked    bool      `json:"revoked"`
	// Phase 56: when set, the key is partner-scoped — it authenticates only
	// the /v1/partner/* surface for this partner, never the tenant API.
	PartnerID  string    `json:"partner_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
}

// WebhookSubscription is a tenant's outbound event subscription. Secret signs
// every delivery (X-Concierge-Signature: sha256=<hmac>).
type WebhookSubscription struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"` // e.g. ["booking.*", "quote.created"]; "*" = all
	Secret    string    `json:"-"`      // returned once at creation, never re-exposed
	Active    bool      `json:"active"`
	// Phase 56: when set, this subscription receives only events whose
	// payload carries a matching partner_id — the partner-facing feed.
	PartnerID string    `json:"partner_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// Phase 21: dual-secret rotation window. RotateSecret keeps the previous
	// secret here; while SecretRotatedAt is within WebhookRotationWindow the
	// dispatcher signs with BOTH secrets (comma-separated header) so
	// receivers can cut over without dropping deliveries. PrevSecret is
	// never exposed over the API.
	PrevSecret      string    `json:"-"`
	SecretRotatedAt time.Time `json:"secret_rotated_at,omitempty"`
}

// WebhookDelivery is one outbound attempt record (at-least-once). The
// dispatcher retries with backoff until 2xx or MaxAttempts.
type WebhookDelivery struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	SubscriptionID string    `json:"subscription_id"`
	EventID        string    `json:"event_id"`
	EventKind      string    `json:"event_kind"`
	Payload        string    `json:"payload"`
	Attempts       int       `json:"attempts"`
	MaxAttempts    int       `json:"max_attempts"`
	NextAttemptAt  time.Time `json:"next_attempt_at"`
	DeliveredAt    time.Time `json:"delivered_at,omitempty"`
	LastStatus     int       `json:"last_status,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// Referral is a cross-tenant demand handoff (Phase 3 network effect): the
// referring tenant cannot serve the request (coverage, fleet saturation) and
// offers it to a receiving tenant that opted in. Commission settles on the
// receiving tenant's confirmed booking.
type Referral struct {
	ID                string    `json:"id"`
	ReferringTenantID string    `json:"referring_tenant_id"`
	ReceivingTenantID string    `json:"receiving_tenant_id"`
	ClientName        string    `json:"client_name"`
	ClientContact     string    `json:"client_contact"` // email or phone anchor
	RequestSummary    string    `json:"request_summary"`
	EstValueMinor     int64     `json:"est_value_minor"`
	Currency          string    `json:"currency"`
	FeePct            float64   `json:"fee_pct"`
	State             string    `json:"state"`                // OFFERED|ACCEPTED|DECLINED|FULFILLED|SETTLED
	RequestID         string    `json:"request_id,omitempty"` // receiving tenant's ServiceRequest
	BookingID         string    `json:"booking_id,omitempty"` // receiving tenant's booking
	CommissionMinor   int64     `json:"commission_minor,omitempty"`
	IdempotencyKey    string    `json:"-"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

const (
	ReferralOffered   = "OFFERED"
	ReferralAccepted  = "ACCEPTED"
	ReferralDeclined  = "DECLINED"
	ReferralFulfilled = "FULFILLED"
	ReferralSettled   = "SETTLED"
)

// FlightOffer / HotelOffer are normalized GDS results (Amadeus NDC seed).
type FlightOffer struct {
	ID          string `json:"id"`
	Source      string `json:"source"` // amadeus | mock
	Origin      string `json:"origin"`
	Destination string `json:"destination"`
	DepartureAt string `json:"departure_at"`
	ArrivalAt   string `json:"arrival_at"`
	Carrier     string `json:"carrier"`
	FlightNo    string `json:"flight_no"`
	Cabin       string `json:"cabin"`
	TotalMinor  int64  `json:"total_minor"`
	Currency    string `json:"currency"`
	OfferRef    string `json:"offer_ref"` // opaque provider reference for booking
}

type HotelOffer struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	HotelName  string `json:"hotel_name"`
	City       string `json:"city"`
	CheckIn    string `json:"check_in"`
	CheckOut   string `json:"check_out"`
	RoomType   string `json:"room_type"`
	TotalMinor int64  `json:"total_minor"`
	Currency   string `json:"currency"`
	OfferRef   string `json:"offer_ref"`
}
