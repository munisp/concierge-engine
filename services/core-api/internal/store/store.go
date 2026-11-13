// Package store defines the persistence contracts. Default build ships an
// in-memory implementation; build with -tags pg for Postgres (RLS-enforced).
package store

import (
	"context"

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
	Close()
}
