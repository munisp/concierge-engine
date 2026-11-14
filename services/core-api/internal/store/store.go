// Package store defines the persistence contracts. Default build ships an
// in-memory implementation; build with -tags pg for Postgres (RLS-enforced).
package store

import (
	"context"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

type TenantStore interface {
	Create(ctx context.Context, t *model.Tenant) error
	Get(ctx context.Context, tenantID string) (*model.Tenant, error)
	GetBySlug(ctx context.Context, slug string) (*model.Tenant, error)
	UpdateConfig(ctx context.Context, t *model.Tenant) error
	List(ctx context.Context) ([]model.Tenant, error)
}

type ClientStore interface {
	Upsert(ctx context.Context, c *model.Client) error
	Get(ctx context.Context, tenantID, id string) (*model.Client, error)
	FindByPhone(ctx context.Context, tenantID, phone string) (*model.Client, error)
	FindByEmail(ctx context.Context, tenantID, email string) (*model.Client, error)
	// Phase 14: client referral codes. Code is derived from the client uuid
	// ("VIP-" + first 8 hex chars, uppercase) — see model.ClientReferralCode.
	FindByReferralCode(ctx context.Context, tenantID, code string) (*model.Client, error)
	SavePreference(ctx context.Context, p *model.Preference) error
	Preferences(ctx context.Context, tenantID, clientID string) ([]model.Preference, error)
	DeletePreferences(ctx context.Context, tenantID, clientID string) error // DSAR cascade
}

type CatalogStore interface {
	Add(ctx context.Context, s *model.CatalogService) error
	List(ctx context.Context, tenantID string) ([]model.CatalogService, error)
}

type RequestStore interface {
	Create(ctx context.Context, r *model.ServiceRequest) error // idempotent on IdempotencyKey
	Get(ctx context.Context, tenantID, id string) (*model.ServiceRequest, error)
	Update(ctx context.Context, r *model.ServiceRequest) error
}

type QuoteStore interface {
	Create(ctx context.Context, q *model.Quote) error
	Get(ctx context.Context, tenantID, id string) (*model.Quote, error)
	LatestForRequest(ctx context.Context, tenantID, requestID string) (*model.Quote, error)
}

type BookingStore interface {
	Create(ctx context.Context, b *model.Booking) error
	Get(ctx context.Context, tenantID, id string) (*model.Booking, error)
	UpdateState(ctx context.Context, tenantID, id, state string) error
	List(ctx context.Context, tenantID string) ([]model.Booking, error)
}

type DispatchStore interface {
	AddFleetResource(ctx context.Context, f *model.FleetResource) error
	ListFleet(ctx context.Context, tenantID string) ([]model.FleetResource, error)
	AddCrewMember(ctx context.Context, c *model.CrewMember) error
	ListCrew(ctx context.Context, tenantID string) ([]model.CrewMember, error)
	CreateAssignment(ctx context.Context, a *model.Assignment) error // idempotent on IdempotencyKey (BookingID+resource)
	UpdateAssignmentStatus(ctx context.Context, tenantID, id, status string) error
	ListAssignments(ctx context.Context, tenantID string) ([]model.Assignment, error)
	AddGeoStamp(ctx context.Context, g *model.GeoStamp) error
}

type PaymentStore interface {
	CreateIntent(ctx context.Context, p *model.PaymentIntent) error // idempotent on IdempotencyKey
	GetIntent(ctx context.Context, tenantID, id string) (*model.PaymentIntent, error)
	UpdateIntentState(ctx context.Context, tenantID, id, state, pspRef string) error
	AddPosting(ctx context.Context, p *model.LedgerPosting) error // idempotent on IdempotencyKey
	Postings(ctx context.Context, tenantID string) ([]model.LedgerPosting, error)
}

type ConversationStore interface {
	CreateConversation(ctx context.Context, c *model.Conversation) error
	GetConversation(ctx context.Context, tenantID, id string) (*model.Conversation, error)
	SetState(ctx context.Context, tenantID, id, state string) error
	AppendMessage(ctx context.Context, m *model.Message) error
	Timeline(ctx context.Context, tenantID, conversationID string) ([]model.Message, error)
}

type HandoffStore interface {
	Open(ctx context.Context, h *model.HandoffItem) error
	Get(ctx context.Context, tenantID, id string) (*model.HandoffItem, error)
	Resolve(ctx context.Context, tenantID, id, status, owner string) error
	ListOpen(ctx context.Context, tenantID string) ([]model.HandoffItem, error)
}

type OutboxStore interface {
	Add(ctx context.Context, e *model.OutboxEvent) error
	Unpublished(ctx context.Context, limit int) ([]model.OutboxEvent, error)
	MarkPublished(ctx context.Context, ids []int64) error
}

type AttributionStore interface {
	Track(ctx context.Context, e *model.AttributionEvent) error
	// Phase 14: acquisition analytics.
	// List returns a client's attribution events, oldest first (first-touch).
	List(ctx context.Context, tenantID, clientID string, limit int) ([]model.AttributionEvent, error)
	// Funnel aggregates events by (kind, channel, campaign).
	Funnel(ctx context.Context, tenantID string) ([]FunnelRow, error)
}

// FunnelRow is one acquisition funnel cell: how many events of a kind came
// through a channel/campaign, and the value they carried.
type FunnelRow struct {
	Kind       string `json:"kind"`
	Channel    string `json:"channel"`
	Campaign   string `json:"campaign"`
	Count      int    `json:"count"`
	ValueMinor int64  `json:"value_minor"`
}

// ---------------------------------------------------------- Phase 14 outreach

type OutreachStore interface {
	// AddSend is idempotent per (tenant, idempotency_key): on replay it
	// returns ErrConflict and the caller reloads the original.
	AddSend(ctx context.Context, o *model.OutreachSend) error
	// SendsForClient: recent sends to one client (frequency-cap window).
	SendsForClient(ctx context.Context, tenantID, clientID string, since time.Time) ([]model.OutreachSend, error)
	// ListSends: recent sends for the tenant, newest first.
	ListSends(ctx context.Context, tenantID string, limit int) ([]model.OutreachSend, error)
}

// ---------------------------------------------------------- Phase 6 geo

type GeoStore interface {
	AddArea(ctx context.Context, a *model.ServiceArea) error
	Areas(ctx context.Context, tenantID string) ([]model.ServiceArea, error)
}

// ---------------------------------------------------------- Phase 11 receipts

type ReceiptStore interface {
	// AddReceipt is idempotent per (tenant, external_ref, status): channel
	// redelivery must not double-count.
	AddReceipt(ctx context.Context, rc *model.MessageReceipt) error
	Receipts(ctx context.Context, tenantID, externalRef string) ([]model.MessageReceipt, error)
	ReceiptCounts(ctx context.Context, tenantID string) (map[string]int, error)
	// Phase 12: external_ref -> case correlation (set by SendReply when the
	// channel returns a message id) so receipts can alert on the right case.
	// LinkReceipt is idempotent per (tenant, external_ref) — first link wins.
	LinkReceipt(ctx context.Context, tenantID, externalRef, caseID string) error
	// LinkedCase returns the case a channel message id belongs to, or ""
	// (no error) when the ref was never linked (e.g. template sends).
	LinkedCase(ctx context.Context, tenantID, externalRef string) (string, error)
	// ReceiptsByStatus lists recent receipts of one status (failures board).
	ReceiptsByStatus(ctx context.Context, tenantID, status string, limit int) ([]model.MessageReceipt, error)
	// RefsForCase lists channel message ids linked to a case.
	RefsForCase(ctx context.Context, tenantID, caseID string) ([]string, error)
}

// ---------------------------------------------------------- Phase 2 stores

type PartnerStore interface {
	Create(ctx context.Context, p *model.Partner) error
	Get(ctx context.Context, tenantID, id string) (*model.Partner, error)
	List(ctx context.Context, tenantID string) ([]model.Partner, error)
	Update(ctx context.Context, p *model.Partner) error
}

type CorporateStore interface {
	CreateAccount(ctx context.Context, a *model.CorporateAccount) error
	GetAccount(ctx context.Context, tenantID, id string) (*model.CorporateAccount, error)
	ListAccounts(ctx context.Context, tenantID string) ([]model.CorporateAccount, error)
	CreateInvoice(ctx context.Context, inv *model.CorporateInvoice) error // idempotent on IdempotencyKey
	GetInvoice(ctx context.Context, tenantID, id string) (*model.CorporateInvoice, error)
	UpdateInvoiceState(ctx context.Context, tenantID, id, state string) error
	ListInvoices(ctx context.Context, tenantID, accountID string) ([]model.CorporateInvoice, error)
}

type PayoutStore interface {
	Create(ctx context.Context, p *model.Payout) error // idempotent on IdempotencyKey
	Get(ctx context.Context, tenantID, id string) (*model.Payout, error)
	GetByTransfer(ctx context.Context, tenantID, transferID string) (*model.Payout, error)
	Update(ctx context.Context, p *model.Payout) error
	List(ctx context.Context, tenantID string) ([]model.Payout, error)
}

// AuditStore is append-only (Master Spec section 13).
type AuditStore interface {
	Log(ctx context.Context, e *model.AuditEntry) error
	List(ctx context.Context, tenantID string, limit int) ([]model.AuditEntry, error)
}

// ------------------------------------------------------------- Phase 3 stores

type ApiKeyStore interface {
	Create(ctx context.Context, k *model.ApiKey) error
	GetByPrefix(ctx context.Context, prefix string) (*model.ApiKey, error)
	List(ctx context.Context, tenantID string) ([]model.ApiKey, error)
	Revoke(ctx context.Context, tenantID, id string) error
	TouchUsed(ctx context.Context, id string, at time.Time) error
}

type WebhookStore interface {
	CreateSubscription(ctx context.Context, s *model.WebhookSubscription) error
	GetSubscription(ctx context.Context, tenantID, id string) (*model.WebhookSubscription, error)
	ListSubscriptions(ctx context.Context, tenantID string) ([]model.WebhookSubscription, error)
	UpdateSubscription(ctx context.Context, s *model.WebhookSubscription) error
	// Deliveries: due returns pending deliveries whose NextAttemptAt <= now.
	EnqueueDelivery(ctx context.Context, d *model.WebhookDelivery) error
	DueDeliveries(ctx context.Context, now time.Time, limit int) ([]model.WebhookDelivery, error)
	UpdateDelivery(ctx context.Context, d *model.WebhookDelivery) error
	ListDeliveries(ctx context.Context, tenantID string, limit int) ([]model.WebhookDelivery, error)
}

type ReferralStore interface {
	Create(ctx context.Context, r *model.Referral) error
	Get(ctx context.Context, tenantID, id string) (*model.Referral, error)
	// List returns referrals where the tenant is referrer or receiver.
	List(ctx context.Context, tenantID string) ([]model.Referral, error)
	Update(ctx context.Context, r *model.Referral) error
}

// ------------------------------------------------------------- Phase 4 stores

// CaseStore persists the case-management aggregate. Create is idempotent on
// (tenant, IdempotencyKey). Overdue lists open-state cases past their SLA.
type CaseStore interface {
	Create(ctx context.Context, c *model.Case) error
	Get(ctx context.Context, tenantID, id string) (*model.Case, error)
	Update(ctx context.Context, c *model.Case) error
	List(ctx context.Context, tenantID, state, kind string) ([]model.Case, error)
	Overdue(ctx context.Context, tenantID string, now time.Time) ([]model.Case, error)
	AddTask(ctx context.Context, t *model.CaseTask) error
	Tasks(ctx context.Context, tenantID, caseID string) ([]model.CaseTask, error)
	UpdateTask(ctx context.Context, t *model.CaseTask) error
	AddNote(ctx context.Context, n *model.CaseNote) error
	Notes(ctx context.Context, tenantID, caseID string) ([]model.CaseNote, error)
}

// EmbeddingStore holds per-case semantic vectors (Phase 4 similar-case
// recall). Vectors are internal — never API-exposed raw. Postgres stores
// them as jsonb; pgvector is the scale-out path (deferred-register).
type EmbeddingStore interface {
	StoreEmbedding(ctx context.Context, tenantID, caseID string, vec []float32) error
	// All returns caseID -> vector for the tenant.
	All(ctx context.Context, tenantID string) (map[string][]float32, error)
}

type Store interface {
	Tenants() TenantStore
	Clients() ClientStore
	Catalog() CatalogStore
	Requests() RequestStore
	Quotes() QuoteStore
	Bookings() BookingStore
	Dispatch() DispatchStore
	Payments() PaymentStore
	Conversations() ConversationStore
	Handoffs() HandoffStore
	Outbox() OutboxStore
	Attribution() AttributionStore
	Partners() PartnerStore
	Corporate() CorporateStore
	Payouts() PayoutStore
	Audit() AuditStore
	ApiKeys() ApiKeyStore
	Webhooks() WebhookStore
	Referrals() ReferralStore
	Cases() CaseStore
	Embeddings() EmbeddingStore
	Geo() GeoStore
	Receipts() ReceiptStore
	Outreach() OutreachStore
	Close()
}
