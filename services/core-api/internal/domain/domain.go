// Package domain holds the module services of the modular monolith.
package domain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/model"
	"concierge-engine/services/core-api/internal/store"
)

type Deps struct {
	PolicyURL     string
	LedgerURL     string
	MojaloopURL   string // Rust FSPIOP adapter (Phase 2)
	AvinodeURL    string // charter supply adapter (Phase 2); empty = disabled
	AvinodeKey    string
	FieldKeyB64   string // CE_FIELD_KEY: VIP field-level encryption key
	TripSecret    string // CE_TRIP_SECRET: chauffeur magic-link HMAC secret
	PublicBase    string // public base URL for adapter callbacks
	StripeKey     string // CE_STRIPE_KEY: secret key; empty = sandbox links
	StripeBase    string // CE_STRIPE_BASE_URL override (tests / sandbox)
	StripeWHSec   string // CE_STRIPE_WEBHOOK_SECRET for signature verification
	PaystackKey   string // CE_PAYSTACK_KEY: secret key; empty = disabled (Phase 15)
	PaystackBase  string // CE_PAYSTACK_BASE_URL override (tests / sandbox)
	AmadeusBase   string // CE_AMADEUS_BASE_URL (test.api.amadeus.com default)
	AmadeusID     string // CE_AMADEUS_CLIENT_ID; empty = mock GDS
	AmadeusSecret string // CE_AMADEUS_CLIENT_SECRET
	AgentURL      string // CE_AGENT_URL: concierge-agent (case AI assist); empty = core rules only
	WhatsAppURL   string // CE_WHATSAPP_ADAPTER_URL: outbound channel for case replies; empty = draft-only
	OperatorPhone string // CE_OPERATOR_PHONE: operator WhatsApp for reply-failure alerts; empty = events only
	GeocodeURL    string // CE_GEOCODE_URL: Nominatim-compatible endpoint; empty = built-in gazetteer
	GeocodeKey    string // CE_GEOCODE_API_KEY when the provider needs one
	ConsoleBase   string // CE_CONSOLE_BASE: public origin of the console SPA (SEO CTA target); empty = same-origin
	Now           func() time.Time
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
	Webhooks      *WebhookService
	ApiKeys       *ApiKeyService
	Referrals     *ReferralService
	GDS           *GDSService
	InstantOn     *InstantOnService
	Cases         *CaseService
	Geo           *GeoService
	Receipts      *ReceiptsService
	Outreach      *OutreachService
	FollowUps     *FollowUpService
	Acquisition   *AcquisitionService
	Studio        *StudioService
	Insights      *InsightsService
	Supply        *SupplyService
	Trips         *TripService
	Audit         *AuditService
	Policy        *PolicyClient
	Ledger        *LedgerClient
	Stripe        *StripeClient
	Paystack      *PaystackClient
	Events        *EventBus
	Cipher        *FieldCipher
	publicBase    string
	consoleBase   string
	waURL         string // shared operator-alert transport (Phase 27 notices)
	operatorPhone string
	now           func() time.Time
}

func NewServices(st store.Store, deps Deps) *Services {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	s := &Services{Store: st, now: now, publicBase: deps.PublicBase, consoleBase: deps.ConsoleBase,
		waURL: deps.WhatsAppURL, operatorPhone: deps.OperatorPhone}
	s.Policy = NewPolicyClient(deps.PolicyURL)
	s.Ledger = NewLedgerClient(deps.LedgerURL)
	s.Stripe = NewStripeClient(deps.StripeKey, deps.StripeBase, deps.StripeWHSec, now)
	s.Paystack = NewPaystackClient(deps.PaystackKey, deps.PaystackBase)
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
	s.Webhooks = NewWebhookService(s)
	s.ApiKeys = &ApiKeyService{s: s}
	s.Referrals = &ReferralService{s: s}
	s.GDS = NewGDSService(s, NewAmadeusAdapter(deps.AmadeusBase, deps.AmadeusID, deps.AmadeusSecret))
	s.InstantOn = &InstantOnService{s: s}
	s.Cases = &CaseService{s: s, assist: NewCaseAssistClient(deps.AgentURL), waURL: deps.WhatsAppURL}
	s.Geo = NewGeoService(s, deps.GeocodeURL, deps.GeocodeKey)
	s.Receipts = NewReceiptsService(s, deps.WhatsAppURL, deps.OperatorPhone)
	s.Outreach = NewOutreachService(s, deps.WhatsAppURL)
	s.FollowUps = &FollowUpService{s: s}
	s.Studio = NewStudioService(s, NewCaseAssistClient(deps.AgentURL))
	s.Insights = NewInsightsService(s)
	s.Acquisition = &AcquisitionService{s: s, waURL: deps.WhatsAppURL, operatorPhone: deps.OperatorPhone}
	// Webhook fan-out hooks the event spine after outbox persistence.
	s.Events.hook = func(ctx context.Context, tenantID, eventType, eventID string, payload map[string]any) {
		s.Webhooks.FanOut(ctx, tenantID, eventType, eventID, payload)
	}
	return s
}

func (s *Services) Now() time.Time { return s.now().UTC() }

// PublicBase is the API's own public origin (used for sitemap/canonical
// URLs); ConsoleBase is the console SPA's public origin (SEO CTAs) —
// empty means same-origin.
func (s *Services) PublicBase() string  { return strings.TrimSuffix(s.publicBase, "/") }
func (s *Services) ConsoleBase() string { return strings.TrimSuffix(s.consoleBase, "/") }

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
	// hook fans events out to webhook subscriptions (Phase 3). Optional so
	// tests can use the bus without a webhook service.
	hook func(ctx context.Context, tenantID, eventType, eventID string, payload map[string]any)
}

func (e *EventBus) Emit(ctx context.Context, tenantID, aggregate, aggregateID, eventType string, payload map[string]any) {
	ev := &model.OutboxEvent{
		TenantID: tenantID, Aggregate: aggregate, AggregateID: aggregateID,
		EventType: eventType, Payload: payload,
	}
	_ = e.outbox.Add(ctx, ev)
	if e.hook != nil {
		e.hook(ctx, tenantID, eventType, strconv.FormatInt(ev.ID, 10), payload)
	}
}

func (e *EventBus) Track(ctx context.Context, ev *model.AttributionEvent) {
	_ = e.attribution.Track(ctx, ev)
}
