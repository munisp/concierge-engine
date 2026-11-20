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
	// GetByDomain (Phase 28): resolve a tenant by custom domain (host header
	// of an inbound request). Host is normalized (lowercased, port and
	// leading www. stripped) before matching against site_config domains.
	GetByDomain(ctx context.Context, host string) (*model.Tenant, error)
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
	// Phase 15: CRM-360.
	// Search finds clients by name/email/phone substring (case-insensitive).
	Search(ctx context.Context, tenantID, query string, limit int) ([]model.Client, error)
	// List returns up to limit clients for a tenant, newest first (Phase 25
	// lifecycle sweeps need the whole book, not a substring search).
	List(ctx context.Context, tenantID string, limit int) ([]model.Client, error)
	// Merge re-points every client-scoped record (requests, cases,
	// conversations, preferences, attribution, outreach) from loser to
	// winner, then deletes the loser. Fixes cross-channel duplicates.
	Merge(ctx context.Context, tenantID, winnerID, loserID string) error
	SavePreference(ctx context.Context, p *model.Preference) error
	Preferences(ctx context.Context, tenantID, clientID string) ([]model.Preference, error)
	DeletePreferences(ctx context.Context, tenantID, clientID string) error // DSAR cascade
}

type CatalogStore interface {
	Add(ctx context.Context, s *model.CatalogService) error
	List(ctx context.Context, tenantID string) ([]model.CatalogService, error)
	// Get (Phase 32): one service by id, INCLUDING inactive rows. List
	// filters on Active (it feeds the quote engine and public site), but
	// the update path must be able to find — and reactivate — a
	// deactivated service.
	Get(ctx context.Context, tenantID, id string) (*model.CatalogService, error)
	// ListAll (Phase 33): every service including inactive. Used by
	// reference-scrubbing (media delete) which must reach deactivated
	// services too — a dangling image_id rots just the same there.
	ListAll(ctx context.Context, tenantID string) ([]model.CatalogService, error)
	// Update (Phase 30): persist mutable catalog fields — currently the
	// i18n presentation maps the design studio writes. Tenant-scoped.
	Update(ctx context.Context, s *model.CatalogService) error
}

type RequestStore interface {
	Create(ctx context.Context, r *model.ServiceRequest) error // idempotent on IdempotencyKey
	Get(ctx context.Context, tenantID, id string) (*model.ServiceRequest, error)
	Update(ctx context.Context, r *model.ServiceRequest) error
	// ForClient: a client's requests, newest first (Phase 15 CRM-360).
	ForClient(ctx context.Context, tenantID, clientID string, limit int) ([]model.ServiceRequest, error)
	// ForSource: demand one partner has sourced, newest first (Phase 63).
	ForSource(ctx context.Context, tenantID, partnerID string, limit int) ([]model.ServiceRequest, error)
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
	// SetSource reattributes a booking's demand channel (Phase 64).
	SetSource(ctx context.Context, tenantID, id, kind, partnerID string) error
	// ForClient: bookings on the client's requests, newest first (Phase 15).
	ForClient(ctx context.Context, tenantID, clientID string, limit int) ([]model.Booking, error)
}

type DispatchStore interface {
	AddFleetResource(ctx context.Context, f *model.FleetResource) error
	ListFleet(ctx context.Context, tenantID string) ([]model.FleetResource, error)
	AddCrewMember(ctx context.Context, c *model.CrewMember) error
	ListCrew(ctx context.Context, tenantID string) ([]model.CrewMember, error)
	CreateAssignment(ctx context.Context, a *model.Assignment) error // idempotent on IdempotencyKey (BookingID+resource)
	UpdateAssignmentStatus(ctx context.Context, tenantID, id, status string) error
	ListAssignments(ctx context.Context, tenantID string) ([]model.Assignment, error)
	// CountClientTrips (Phase 62): completed trips this client took with
	// this crew member — the matcher's familiarity signal.
	CountClientTrips(ctx context.Context, tenantID, clientID, crewMemberID string) (int, error)
	AddGeoStamp(ctx context.Context, g *model.GeoStamp) error
	// LatestGeoStamp returns the newest stamp for an assignment (Phase 25
	// client tracking); ErrNotFound when the trip has no positions yet.
	LatestGeoStamp(ctx context.Context, tenantID, assignmentID string) (*model.GeoStamp, error)
	// PurgeGeoStampsBefore (Phase 46 retention): permanently delete the
	// tenant's stamps recorded before `before`. Location history is the most
	// sensitive data we hold — it has no retention justification once the
	// trip is complete. Returns rows removed.
	PurgeGeoStampsBefore(ctx context.Context, tenantID string, before time.Time) (int, error)
}

// ChauffeurLinkStore (Phase 62): rider↔chauffeur relationships —
// favorite is a strong positive match signal, blocked is a hard
// exclusion. One row per (tenant, client, crew member).
type ChauffeurLinkStore interface {
	Set(ctx context.Context, l *model.ChauffeurLink) error // upsert
	Clear(ctx context.Context, tenantID, clientID, crewMemberID string) error
	ListForClient(ctx context.Context, tenantID, clientID string) ([]model.ChauffeurLink, error)
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
	// ForClient: a client's conversations, newest first (Phase 15 CRM-360).
	ForClient(ctx context.Context, tenantID, clientID string, limit int) ([]model.Conversation, error)
	// FindActive (Phase 22): the client's newest non-CLOSED conversation on
	// a channel — the get-or-create anchor that keeps one thread per client
	// per channel instead of one conversation per message.
	FindActive(ctx context.Context, tenantID, clientID, channel string) (*model.Conversation, error)
	// ScrubMessageBodiesBefore (Phase 46 retention): overwrite message
	// bodies with `marker` in the tenant's CLOSED conversations whose
	// message was sent before `before`. The message ROW (id, author,
	// sent_at) survives — threading and counts are metadata, not content.
	// Idempotent: an already-scrubbed body is not re-counted. Returns rows
	// scrubbed.
	ScrubMessageBodiesBefore(ctx context.Context, tenantID string, before time.Time, marker string) (int, error)
	// ScrubMessageBodiesForClient (Phase 46): overwrite every message body
	// in the client's conversations regardless of state — the erasure
	// (GDPR Art. 17) companion. Returns rows scrubbed.
	ScrubMessageBodiesForClient(ctx context.Context, tenantID, clientID, marker string) (int, error)
}

type HandoffStore interface {
	Open(ctx context.Context, h *model.HandoffItem) error
	Get(ctx context.Context, tenantID, id string) (*model.HandoffItem, error)
	Resolve(ctx context.Context, tenantID, id, status, owner string) error
	ListOpen(ctx context.Context, tenantID string) ([]model.HandoffItem, error)
}

// RewardStore is client referral credit (Phase 18): accrual on conversion,
// balance for payment-link application, oldest-first redemption at capture.
type RewardStore interface {
	// Accrue credits a reward; idempotent per (tenant, client, source
	// booking) — a replayed conversion never double-credits.
	Accrue(ctx context.Context, r *model.ClientReward) error
	// Balance sums ACCRUED rewards for a client.
	Balance(ctx context.Context, tenantID, clientID string) (int64, error)
	// Redeem marks up to amount of ACCRUED rewards REDEEMED against a
	// payment intent, oldest first. Returns the amount actually covered.
	// Expired rewards are swept to EXPIRED and never redeemed.
	Redeem(ctx context.Context, tenantID, clientID, intentID string, amount int64) (int64, error)
	// RefundRedemption returns rewards redeemed on a refunded intent to
	// ACCRUED — the client never loses credit to a refund.
	RefundRedemption(ctx context.Context, tenantID, intentID string) error
	// List returns the client's reward history, newest first (Phase 19).
	List(ctx context.Context, tenantID, clientID string) ([]model.ClientReward, error)
}

// PromoStore is fixed-credit promo campaigns (Phase 20): codes clients
// redeem for reward credit. Redemption idempotency lives on the reward row
// (partial UNIQUE on promo_code).
type PromoStore interface {
	Create(ctx context.Context, c *model.PromoCampaign) error // idempotent on (tenant, code)
	GetByCode(ctx context.Context, tenantID, code string) (*model.PromoCampaign, error)
	List(ctx context.Context, tenantID string) ([]model.PromoCampaign, error)
	// Redemptions counts rewards accrued under a code (cap enforcement).
	Redemptions(ctx context.Context, tenantID, code string) (int, error)
}

// ReviewStore (Phase 27): post-trip client reviews. Create is idempotent
// per (tenant, assignment) — one review per trip, replays return the
// original. ConsentedOnly lists publishable reviews (public SEO pages);
// ListAll is the operator's full view.
type ReviewStore interface {
	Create(ctx context.Context, r *model.Review) error
	GetByAssignment(ctx context.Context, tenantID, assignmentID string) (*model.Review, error)
	Consented(ctx context.Context, tenantID string) ([]model.Review, error)
	ListAll(ctx context.Context, tenantID string, limit int) ([]model.Review, error)
}

// MediaStore (Phase 31): tenant-owned site imagery. Put stores the decoded
// bytes (already validated for type + size by the upload handler); Get
// returns the full record including bytes for the public serving path;
// List returns metadata only (no Data) for the console picker. Delete is
// idempotent-scoped to the tenant — a dangling catalog image_id falls back
// to the monogram tile rather than erroring.
//
// Phase 49 (OpenDAL): WithBlobMedia decorates this store so bytes land in
// the blob-adapter (Apache OpenDAL) and the row keeps only metadata +
// storage_key. MarkExternal is the backfill primitive: after the object's
// bytes are written to the adapter it records key/engine/size and clears
// the inline data.
type MediaStore interface {
	Put(ctx context.Context, m *model.Media) error
	Get(ctx context.Context, tenantID, id string) (*model.Media, error)
	List(ctx context.Context, tenantID string) ([]model.Media, error)
	Delete(ctx context.Context, tenantID, id string) error
	// MarkExternal (Phase 49) flips a legacy inline row to object storage:
	// sets storage_key/storage_engine/size_bytes and clears the bytea.
	MarkExternal(ctx context.Context, tenantID, id, storageKey, engine string) error
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
	// CountSince (Phase 40) counts events of one kind — optionally narrowed
	// to one channel ("" = all channels) — recorded at or after since. The
	// email deliverability guard reads its 7-day bounce/complaint window
	// from this.
	CountSince(ctx context.Context, tenantID, kind, channel string, since time.Time) (int, error)
	// PurgeBefore (Phase 46 retention): permanently delete the tenant's
	// attribution events recorded before `before`. Note the deliberate
	// interaction with lifecycle hygiene: engagement older than the
	// attribution window stops counting as engagement.
	PurgeBefore(ctx context.Context, tenantID string, before time.Time) (int, error)
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
	// UpdateSendResult (Phase 35): the due-send sweep flips a "scheduled"
	// send to its terminal outcome (sent/held/failed). Guarded on the
	// scheduled status so a double-fire is a harmless no-op.
	UpdateSendResult(ctx context.Context, tenantID, sendID, status, detail, externalRef string) error
	// SendByExternalRef (Phase 42): resolve a channel message id (wamid)
	// back to its send — receipt correlation for the WA hygiene rules.
	// ErrNotFound when the ref belongs to no send (e.g. a case reply).
	SendByExternalRef(ctx context.Context, tenantID, externalRef string) (*model.OutreachSend, error)
}

// TemplateStore (Phase 21): tenant-managed outreach message templates.
// Upsert is keyed by (tenant, name) so re-saving a name is an update, not
// a conflict — the console save button is naturally idempotent.
type TemplateStore interface {
	Upsert(ctx context.Context, t *model.MessageTemplate) error
	Get(ctx context.Context, tenantID, id string) (*model.MessageTemplate, error)
	// GetByName resolves the composer picker's selection at send time.
	GetByName(ctx context.Context, tenantID, name string) (*model.MessageTemplate, error)
	List(ctx context.Context, tenantID string) ([]model.MessageTemplate, error)
	Delete(ctx context.Context, tenantID, id string) error
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
	// ListPendingForPartner (Phase 48): the partner's PENDING payouts —
	// the batching candidate pool.
	ListPendingForPartner(ctx context.Context, tenantID, partnerID string) ([]model.Payout, error)
	// ListByBatch (Phase 48): the member payouts of one settlement batch.
	ListByBatch(ctx context.Context, tenantID, batchID string) ([]model.Payout, error)
}

// SettlementBatchStore (Phase 48): netted partner settlement batches.
// Create is idempotent on (tenant, idempotency_key) — replay surfaces
// ErrConflict and the caller reloads via GetByIdem.
type SettlementBatchStore interface {
	Create(ctx context.Context, b *model.SettlementBatch) error
	Get(ctx context.Context, tenantID, id string) (*model.SettlementBatch, error)
	GetByIdem(ctx context.Context, tenantID, key string) (*model.SettlementBatch, error)
	GetByTransfer(ctx context.Context, tenantID, transferID string) (*model.SettlementBatch, error)
	Update(ctx context.Context, b *model.SettlementBatch) error
	List(ctx context.Context, tenantID string) ([]model.SettlementBatch, error)
}

// AuditStore is append-only (Master Spec section 13).
type AuditStore interface {
	Log(ctx context.Context, e *model.AuditEntry) error
	List(ctx context.Context, tenantID string, limit int) ([]model.AuditEntry, error)
}

// LeadStore (Phase 36): pre-client prospects. Create is idempotent on
// (tenant, idempotency_key) when the key is set — replay surfaces
// ErrConflict and the caller reloads. Unkeyed creates always insert (a
// repeat inquiry is signal; dedup is a domain decision).
type LeadStore interface {
	Create(ctx context.Context, l *model.Lead) error
	Get(ctx context.Context, tenantID, id string) (*model.Lead, error)
	Update(ctx context.Context, l *model.Lead) error
	List(ctx context.Context, tenantID string, limit int) ([]model.Lead, error)
}

// CampaignSpendStore (Phase 40): the campaign spend ledger. Add is
// idempotent on (tenant, idempotency_key) when the key is set — replay
// surfaces ErrConflict and the domain reloads the original row, so a
// retried POST never double-books cost. Unkeyed adds always insert.
// List filters by campaign ("" = all campaigns of the tenant), newest
// spend first.
type CampaignSpendStore interface {
	Add(ctx context.Context, s *model.CampaignSpend) error
	List(ctx context.Context, tenantID, campaign string, limit int) ([]model.CampaignSpend, error)
}

// EmailSuppressionStore (Phase 41): permanently suppressed addresses.
// Add is idempotent per (tenant, email) — re-suppressing surfaces
// ErrConflict and the domain returns the existing row. Emails are
// normalized (lowercase, trimmed) by the domain before they reach here.
type EmailSuppressionStore interface {
	Add(ctx context.Context, s *model.EmailSuppression) error
	Exists(ctx context.Context, tenantID, email string) (bool, error)
	List(ctx context.Context, tenantID string, limit int) ([]model.EmailSuppression, error)
	// Delete removes one suppression (operator recovery when an address
	// comes back to life). Missing row = ErrNotFound.
	Delete(ctx context.Context, tenantID, email string) error
}

// ConsentEventStore (Phase 44): the append-only consent evidence ledger.
// Record inserts one evidence row; there is deliberately no update or
// delete — evidence is immutable. ListForClient returns a client's
// history newest-first. The domain records only ACTUAL changes (a
// replayed consent PUT with the same value adds no row), so idempotency
// lives in the domain, not the store.
type ConsentEventStore interface {
	Record(ctx context.Context, e *model.ConsentEvent) error
	ListForClient(ctx context.Context, tenantID, clientID string, limit int) ([]model.ConsentEvent, error)
}

// KycCaseStore (Phase 47): identity-verification cases. Create is
// idempotent on (tenant, idempotency_key) — replay surfaces ErrConflict
// and the caller reloads via GetByIdem. At most one OPEN case (PENDING or
// IN_REVIEW) per (tenant, client, level) — a second Open at the same
// level while one is in flight is ErrConflict, not a duplicate row.
type KycCaseStore interface {
	Create(ctx context.Context, c *model.KycCase) error
	Get(ctx context.Context, tenantID, id string) (*model.KycCase, error)
	GetByIdem(ctx context.Context, tenantID, key string) (*model.KycCase, error)
	// OpenFor returns the open (PENDING/IN_REVIEW) case for the client at
	// the level, or ErrNotFound.
	OpenFor(ctx context.Context, tenantID, clientID, level string) (*model.KycCase, error)
	Update(ctx context.Context, c *model.KycCase) error
	// List: operator queue; status "" = all statuses, newest first.
	List(ctx context.Context, tenantID, status string, limit int) ([]model.KycCase, error)
	ListForClient(ctx context.Context, tenantID, clientID string) ([]model.KycCase, error)
	// ExpireBefore flips PENDING/IN_REVIEW cases created before `before`
	// to EXPIRED (document freshness). Idempotent; returns rows expired.
	ExpireBefore(ctx context.Context, tenantID string, before time.Time) (int, error)
	// ScrubDocumentRefsForClient (Art. 17 companion): blank the document
	// metadata on every case the client holds. The case row (level,
	// decision, timestamps) survives as the AML record. Returns rows
	// scrubbed.
	ScrubDocumentRefsForClient(ctx context.Context, tenantID, clientID string) (int, error)
}

// ItineraryStore (Phase 61): typed journey legs — flight / hotel /
// ground / partner — with scheduled and actual times.
type ItineraryStore interface {
	Create(ctx context.Context, l *model.ItineraryLeg) error
	Get(ctx context.Context, tenantID, id string) (*model.ItineraryLeg, error)
	Update(ctx context.Context, l *model.ItineraryLeg) error
	// ListForClient: the journey timeline, soonest first.
	ListForClient(ctx context.Context, tenantID, clientID string, limit int) ([]model.ItineraryLeg, error)
	// ForBooking: the ground leg(s) linked to a platform booking.
	ForBooking(ctx context.Context, tenantID, bookingID string) ([]model.ItineraryLeg, error)
	// FlightLegsDue: flight legs whose scheduled departure falls in
	// [from, to] and whose status is still watchable (not ARRIVED /
	// COMPLETED / CANCELLED) — the flight-watch scan set.
	FlightLegsDue(ctx context.Context, tenantID string, from, to time.Time) ([]model.ItineraryLeg, error)
}

// SoundscapeStore (Phase 59): one AI-composed track per booking.
// Create is idempotent on (tenant, IdempotencyKey) — re-Create with the
// same key returns ErrConflict so generation never double-renders.
type SoundscapeStore interface {
	Create(ctx context.Context, s *model.Soundscape) error
	Get(ctx context.Context, tenantID, id string) (*model.Soundscape, error)
	// ForBooking returns the newest soundscape for the booking, or
	// ErrNotFound (booking has none yet — client-facing surfaces render
	// "composing" rather than an error).
	ForBooking(ctx context.Context, tenantID, bookingID string) (*model.Soundscape, error)
	Update(ctx context.Context, s *model.Soundscape) error
	ListForClient(ctx context.Context, tenantID, clientID string, limit int) ([]model.Soundscape, error)
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
	// GetDelivery (Phase 15): single delivery for replay tooling.
	GetDelivery(ctx context.Context, tenantID, id string) (*model.WebhookDelivery, error)
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
	// ForClient: a client's cases, newest first (Phase 15 CRM-360).
	ForClient(ctx context.Context, tenantID, clientID string, limit int) ([]model.Case, error)
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
	Settlements() SettlementBatchStore
	Audit() AuditStore
	ApiKeys() ApiKeyStore
	Webhooks() WebhookStore
	Rewards() RewardStore
	Promos() PromoStore
	Referrals() ReferralStore
	Cases() CaseStore
	Embeddings() EmbeddingStore
	Geo() GeoStore
	Receipts() ReceiptStore
	Outreach() OutreachStore
	Templates() TemplateStore
	Reviews() ReviewStore
	Media() MediaStore
	Leads() LeadStore
	CampaignSpend() CampaignSpendStore
	EmailSuppressions() EmailSuppressionStore
	ConsentEvents() ConsentEventStore
	KycCases() KycCaseStore
	Soundscapes() SoundscapeStore
	Itineraries() ItineraryStore
	ChauffeurLinks() ChauffeurLinkStore
	Close()
}
