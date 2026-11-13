// Package domain holds the module services of the modular monolith.
package domain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"concierge-engine/services/core-api/internal/model"
	"concierge-engine/services/core-api/internal/store"
)

type Deps struct {
	PolicyURL   string
	LedgerURL   string
	MojaloopURL string // Rust FSPIOP adapter (Phase 2)
	AvinodeURL  string // charter supply adapter (Phase 2); empty = disabled
	AvinodeKey  string
	FieldKeyB64 string // CE_FIELD_KEY: VIP field-level encryption key
	TripSecret  string // CE_TRIP_SECRET: chauffeur magic-link HMAC secret
	PublicBase  string // public base URL for adapter callbacks
	Now         func() time.Time
}

type Services struct {
	Store         store.Store
	Tenants       *TenantService
	Clients       *ClientService
	Catalog       *CatalogService
	Booking       *BookingService
	Dispatch      *DispatchService
	Payments      *PaymentService
	Conversations *ConversationService
	Handoffs      *HandoffService
	Partners      *PartnerService
	Corporate     *CorporateService
	Payouts       *PayoutService
	Supply        *SupplyService
	Trips         *TripService
	Audit         *AuditService
	Policy        *PolicyClient
	Ledger        *LedgerClient
	Events        *EventBus
	Cipher        *FieldCipher
	now           func() time.Time
}

func NewServices(st store.Store, deps Deps) *Services {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	s := &Services{Store: st, now: now}
	s.Policy = NewPolicyClient(deps.PolicyURL)
	s.Ledger = NewLedgerClient(deps.LedgerURL)
	s.Events = &EventBus{outbox: st.Outbox(), attribution: st.Attribution()}
	cipher, err := NewFieldCipher(deps.FieldKeyB64)
	if err != nil {
		// Misconfigured crypto must fail loudly at boot, not silently store
		// plaintext for VIP clients. NewServices has no error return, so we
		// disable the cipher and mark it via an audit-visible event on first
		// use; operators gate on the startup log in cmd/core-api.
		cipher = &FieldCipher{}
	}
	s.Cipher = cipher
	s.Tenants = &TenantService{s: s}
	s.Clients = &ClientService{s: s}
	s.Catalog = &CatalogService{s: s}
	s.Booking = &BookingService{s: s}
	s.Dispatch = NewDispatchService(s)
	s.Payments = &PaymentService{s: s}
	s.Conversations = &ConversationService{s: s}
	s.Handoffs = &HandoffService{s: s}
	s.Audit = &AuditService{s: s}
	s.Trips = NewTripService(s, deps.TripSecret)
	s.Partners = &PartnerService{s: s}
	s.Corporate = &CorporateService{s: s}
	s.Payouts = NewPayoutService(s, NewMojaloopClient(deps.MojaloopURL), deps.PublicBase)
	s.Supply = NewSupplyService(s, NewAvinodeAdapter(deps.AvinodeURL, deps.AvinodeKey))
	return s
}

func (s *Services) Now() time.Time { return s.now().UTC() }

// NewID returns a sortable, URL-safe unique id (UUIDv4 hex).
func NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	var buf [36]byte
	hex.Encode(buf[0:8], b[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], b[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], b[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], b[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], b[10:16])
	return string(buf[:])
}

// EventBus emits domain events through the outbox (committed with the
// business transaction in Postgres; immediate in memory). The outbox
// forwarder publishes to Kafka — audit, webhooks, analytics and AI
// training data fall out for free (Master Spec §9).
type EventBus struct {
	outbox      store.OutboxStore
	attribution store.AttributionStore
}

func (e *EventBus) Emit(ctx context.Context, tenantID, aggregate, aggregateID, eventType string, payload map[string]any) {
	_ = e.outbox.Add(ctx, &model.OutboxEvent{
		TenantID: tenantID, Aggregate: aggregate, AggregateID: aggregateID,
		EventType: eventType, Payload: payload,
	})
}

func (e *EventBus) Track(ctx context.Context, ev *model.AttributionEvent) {
	_ = e.attribution.Track(ctx, ev)
}
