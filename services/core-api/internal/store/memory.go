// In-memory store: default for local dev, demos and tests. All stores are
// tenant-scoped by construction; the RLS test suite runs the same operations
// against Postgres to verify isolation (Master Spec §13).
package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type memory struct {
	mu            sync.RWMutex
	tenants       map[string]*model.Tenant
	tenantSlug    map[string]string
	clients       map[string]*model.Client
	preferences   map[string]*model.Preference
	catalog       map[string]*model.CatalogService
	requests      map[string]*model.ServiceRequest
	requestIdem   map[string]string
	quotes        map[string]*model.Quote
	bookings      map[string]*model.Booking
	fleet         map[string]*model.FleetResource
	crew          map[string]*model.CrewMember
	assignments   map[string]*model.Assignment
	geostamps     []model.GeoStamp
	intents       map[string]*model.PaymentIntent
	intentIdem    map[string]string
	postings      map[string]*model.LedgerPosting
	conversations map[string]*model.Conversation
	messages      map[string][]model.Message
	handoffs      map[string]*model.HandoffItem
	outbox        []model.OutboxEvent
	attribution   []model.AttributionEvent
	partners      map[string]*model.Partner
	corporate     map[string]*model.CorporateAccount
	invoices      map[string]*model.CorporateInvoice
	invoiceIdem   map[string]string
	apiKeys       map[string]*model.ApiKey
	webhookSubs   map[string]*model.WebhookSubscription
	webhookDlvs   map[string]*model.WebhookDelivery
	referrals     map[string]*model.Referral
	referralIdem  map[string]string
	cases         map[string]*model.Case
	caseIdem      map[string]string
	caseTasks     map[string]*model.CaseTask
	caseNotes     map[string][]model.CaseNote
	embeddings    map[string]map[string][]float32  // tenantID -> caseID -> vector
	geoAreas      map[string]*model.ServiceArea    // tenantID|areaID
	receipts      map[string]*model.MessageReceipt // tenantID|externalRef|status (idempotent)
	receiptLinks  map[string]string                // tenantID|externalRef -> caseID (first link wins)
	payouts       map[string]*model.Payout
	payoutIdem    map[string]string
	audit         []model.AuditEntry
}

func NewMemory() Store {
	return &memory{
		tenants:       map[string]*model.Tenant{},
		tenantSlug:    map[string]string{},
		clients:       map[string]*model.Client{},
		preferences:   map[string]*model.Preference{},
		catalog:       map[string]*model.CatalogService{},
		requests:      map[string]*model.ServiceRequest{},
		requestIdem:   map[string]string{},
		quotes:        map[string]*model.Quote{},
		bookings:      map[string]*model.Booking{},
		fleet:         map[string]*model.FleetResource{},
		crew:          map[string]*model.CrewMember{},
		assignments:   map[string]*model.Assignment{},
		intents:       map[string]*model.PaymentIntent{},
		intentIdem:    map[string]string{},
		postings:      map[string]*model.LedgerPosting{},
		conversations: map[string]*model.Conversation{},
		messages:      map[string][]model.Message{},
		handoffs:      map[string]*model.HandoffItem{},
		partners:      map[string]*model.Partner{},
		corporate:     map[string]*model.CorporateAccount{},
		invoices:      map[string]*model.CorporateInvoice{},
		invoiceIdem:   map[string]string{},
		payouts:       map[string]*model.Payout{},
		payoutIdem:    map[string]string{},
		apiKeys:       map[string]*model.ApiKey{},
		webhookSubs:   map[string]*model.WebhookSubscription{},
		webhookDlvs:   map[string]*model.WebhookDelivery{},
		referrals:     map[string]*model.Referral{},
		referralIdem:  map[string]string{},
		cases:         map[string]*model.Case{},
		caseIdem:      map[string]string{},
		caseTasks:     map[string]*model.CaseTask{},
		caseNotes:     map[string][]model.CaseNote{},
		embeddings:    map[string]map[string][]float32{},
		geoAreas:      map[string]*model.ServiceArea{},
		receipts:      map[string]*model.MessageReceipt{},
		receiptLinks:  map[string]string{},
	}
}

func (m *memory) Close() {}

func (m *memory) Tenants() TenantStore             { return &memTenants{m} }
func (m *memory) Clients() ClientStore             { return &memClients{m} }
func (m *memory) Catalog() CatalogStore            { return &memCatalog{m} }
func (m *memory) Requests() RequestStore           { return &memRequests{m} }
func (m *memory) Quotes() QuoteStore               { return &memQuotes{m} }
func (m *memory) Bookings() BookingStore           { return &memBookings{m} }
func (m *memory) Dispatch() DispatchStore          { return &memDispatch{m} }
func (m *memory) Payments() PaymentStore           { return &memPayments{m} }
func (m *memory) Conversations() ConversationStore { return &memConversations{m} }
func (m *memory) Handoffs() HandoffStore           { return &memHandoffs{m} }
func (m *memory) Outbox() OutboxStore              { return &memOutbox{m} }
func (m *memory) Attribution() AttributionStore    { return &memAttribution{m} }
func (m *memory) Partners() PartnerStore           { return &memPartners{m} }
func (m *memory) Corporate() CorporateStore        { return &memCorporate{m} }
func (m *memory) Payouts() PayoutStore             { return &memPayouts{m} }
func (m *memory) Audit() AuditStore                { return &memAudit{m} }
func (m *memory) ApiKeys() ApiKeyStore             { return &memApiKeys{m} }
func (m *memory) Webhooks() WebhookStore           { return &memWebhooks{m} }
func (m *memory) Referrals() ReferralStore         { return &memReferrals{m} }
func (m *memory) Cases() CaseStore                 { return &memCases{m} }
func (m *memory) Embeddings() EmbeddingStore       { return &memEmbeddings{m} }
func (m *memory) Geo() GeoStore                    { return &memGeo{m} }
func (m *memory) Receipts() ReceiptStore           { return &memReceipts{m} }

// ------------------------------------------------------------- tenants
type memTenants struct{ m *memory }

func (s *memTenants) Create(_ context.Context, t *model.Tenant) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if _, ok := s.m.tenantSlug[t.Slug]; ok {
		return fmt.Errorf("tenant slug %q: %w", t.Slug, httpx.ErrConflict)
	}
	cp := *t
	s.m.tenants[t.ID] = &cp
	s.m.tenantSlug[t.Slug] = t.ID
	return nil
}

func (s *memTenants) Get(_ context.Context, tenantID string) (*model.Tenant, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	t, ok := s.m.tenants[tenantID]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *t
	return &cp, nil
}

func (s *memTenants) GetBySlug(_ context.Context, slug string) (*model.Tenant, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	id, ok := s.m.tenantSlug[slug]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *s.m.tenants[id]
	return &cp, nil
}

func (s *memTenants) UpdateConfig(_ context.Context, t *model.Tenant) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if _, ok := s.m.tenants[t.ID]; !ok {
		return httpx.ErrNotFound
	}
	cp := *t
	s.m.tenants[t.ID] = &cp
	return nil
}

func (s *memTenants) List(_ context.Context) ([]model.Tenant, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	out := make([]model.Tenant, 0, len(s.m.tenants))
	for _, t := range s.m.tenants {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// ------------------------------------------------------------- clients
type memClients struct{ m *memory }

func normPhone(p string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r == '+' {
			return r
		}
		return -1
	}, p)
}

func (s *memClients) Upsert(_ context.Context, c *model.Client) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *c
	s.m.clients[c.TenantID+"/"+c.ID] = &cp
	return nil
}

func (s *memClients) Get(_ context.Context, tenantID, id string) (*model.Client, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	c, ok := s.m.clients[tenantID+"/"+id]
	if !ok {
		return nil, httpx.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (s *memClients) FindByPhone(_ context.Context, tenantID, phone string) (*model.Client, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	want := normPhone(phone)
	for _, c := range s.m.clients {
		if c.TenantID == tenantID && c.Phone != "" && normPhone(c.Phone) == want {
			cp := *c
			return &cp, nil
		}
	}
	return nil, httpx.ErrNotFound
}

func (s *memClients) FindByEmail(_ context.Context, tenantID, email string) (*model.Client, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	want := strings.ToLower(strings.TrimSpace(email))
	for _, c := range s.m.clients {
		if c.TenantID == tenantID && c.Email != "" && strings.ToLower(c.Email) == want {
			cp := *c
			return &cp, nil
		}
	}
	return nil, httpx.ErrNotFound
}

func (s *memClients) SavePreference(_ context.Context, p *model.Preference) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *p
	s.m.preferences[p.TenantID+"/"+p.ClientID+"/"+p.Key] = &cp
	return nil
}

func (s *memClients) Preferences(_ context.Context, tenantID, clientID string) ([]model.Preference, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	pfx := tenantID + "/" + clientID + "/"
	out := []model.Preference{}
	for k, p := range s.m.preferences {
		if strings.HasPrefix(k, pfx) {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (s *memClients) DeletePreferences(_ context.Context, tenantID, clientID string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	pfx := tenantID + "/" + clientID + "/"
	for k := range s.m.preferences {
		if strings.HasPrefix(k, pfx) {
			delete(s.m.preferences, k)
		}
	}
	return nil
}

// ------------------------------------------------------------- catalog
type memCatalog struct{ m *memory }

func (s *memCatalog) Add(_ context.Context, svc *model.CatalogService) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *svc
	s.m.catalog[svc.TenantID+"/"+svc.ID] = &cp
	return nil
}

func (s *memCatalog) List(_ context.Context, tenantID string) ([]model.CatalogService, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	out := []model.CatalogService{}
	for _, svc := range s.m.catalog {
		if svc.TenantID == tenantID && svc.Active {
			out = append(out, *svc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ------------------------------------------------------------- requests
type memRequests struct{ m *memory }

func (s *memRequests) Create(_ context.Context, r *model.ServiceRequest) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	key := r.TenantID + "/" + r.IdempotencyKey
	if existingID, ok := s.m.requestIdem[key]; ok {
		cp := *s.m.requests[existingID]
		*r = cp // idempotent replay returns the original
		return nil
	}
	cp := *r
	cp.Items = append([]model.ServiceItem(nil), r.Items...)
	s.m.requests[r.ID] = &cp
	s.m.requestIdem[key] = r.ID
	return nil
}

func (s *memRequests) Get(_ context.Context, tenantID, id string) (*model.ServiceRequest, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	r, ok := s.m.requests[id]
	if !ok || r.TenantID != tenantID {
		return nil, httpx.ErrNotFound
	}
	cp := *r
	cp.Items = append([]model.ServiceItem(nil), r.Items...)
	return &cp, nil
}

func (s *memRequests) Update(_ context.Context, r *model.ServiceRequest) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if _, ok := s.m.requests[r.ID]; !ok {
		return httpx.ErrNotFound
	}
	cp := *r
	cp.Items = append([]model.ServiceItem(nil), r.Items...)
	s.m.requests[r.ID] = &cp
	return nil
}

// ------------------------------------------------------------- quotes
type memQuotes struct{ m *memory }

func (s *memQuotes) Create(_ context.Context, q *model.Quote) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *q
	cp.Lines = append([]model.QuoteLine(nil), q.Lines...)
	s.m.quotes[q.ID] = &cp
	return nil
}

func (s *memQuotes) Get(_ context.Context, tenantID, id string) (*model.Quote, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	q, ok := s.m.quotes[id]
	if !ok || q.TenantID != tenantID {
		return nil, httpx.ErrNotFound
	}
	cp := *q
	return &cp, nil
}

func (s *memQuotes) LatestForRequest(_ context.Context, tenantID, requestID string) (*model.Quote, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	var best *model.Quote
	for _, q := range s.m.quotes {
		if q.TenantID == tenantID && q.RequestID == requestID {
			if best == nil || q.Version > best.Version {
				best = q
			}
		}
	}
	if best == nil {
		return nil, httpx.ErrNotFound
	}
	cp := *best
	return &cp, nil
}

// ------------------------------------------------------------- bookings
type memBookings struct{ m *memory }

func (s *memBookings) Create(_ context.Context, b *model.Booking) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *b
	s.m.bookings[b.ID] = &cp
	return nil
}

func (s *memBookings) Get(_ context.Context, tenantID, id string) (*model.Booking, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	b, ok := s.m.bookings[id]
	if !ok || b.TenantID != tenantID {
		return nil, httpx.ErrNotFound
	}
	cp := *b
	return &cp, nil
}

func (s *memBookings) UpdateState(_ context.Context, tenantID, id, state string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	b, ok := s.m.bookings[id]
	if !ok || b.TenantID != tenantID {
		return httpx.ErrNotFound
	}
	b.State = state
	return nil
}

// ------------------------------------------------------------- dispatch
type memDispatch struct{ m *memory }

func (s *memDispatch) AddFleetResource(_ context.Context, f *model.FleetResource) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *f
	s.m.fleet[f.TenantID+"/"+f.ID] = &cp
	return nil
}

func (s *memDispatch) ListFleet(_ context.Context, tenantID string) ([]model.FleetResource, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	out := []model.FleetResource{}
	for _, f := range s.m.fleet {
		if f.TenantID == tenantID {
			out = append(out, *f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, nil
}

func (s *memDispatch) AddCrewMember(_ context.Context, c *model.CrewMember) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *c
	s.m.crew[c.TenantID+"/"+c.ID] = &cp
	return nil
}

func (s *memDispatch) ListCrew(_ context.Context, tenantID string) ([]model.CrewMember, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	out := []model.CrewMember{}
	for _, c := range s.m.crew {
		if c.TenantID == tenantID {
			out = append(out, *c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FullName < out[j].FullName })
	return out, nil
}

func (s *memDispatch) CreateAssignment(_ context.Context, a *model.Assignment) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *a
	s.m.assignments[a.ID] = &cp
	return nil
}

func (s *memDispatch) UpdateAssignmentStatus(_ context.Context, tenantID, id, status string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	a, ok := s.m.assignments[id]
	if !ok || a.TenantID != tenantID {
		return httpx.ErrNotFound
	}
	a.Status = status
	return nil
}

func (s *memDispatch) ListAssignments(_ context.Context, tenantID string) ([]model.Assignment, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	out := []model.Assignment{}
	for _, a := range s.m.assignments {
		if a.TenantID == tenantID {
			out = append(out, *a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *memDispatch) AddGeoStamp(_ context.Context, g *model.GeoStamp) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	s.m.geostamps = append(s.m.geostamps, *g)
	return nil
}

// ------------------------------------------------------------- payments
type memPayments struct{ m *memory }

func (s *memPayments) CreateIntent(_ context.Context, p *model.PaymentIntent) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	key := p.TenantID + "/" + p.IdempotencyKey
	if existingID, ok := s.m.intentIdem[key]; ok {
		cp := *s.m.intents[existingID]
		*p = cp
		return nil
	}
	cp := *p
	s.m.intents[p.ID] = &cp
	s.m.intentIdem[key] = p.ID
	return nil
}

func (s *memPayments) GetIntent(_ context.Context, tenantID, id string) (*model.PaymentIntent, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	p, ok := s.m.intents[id]
	if !ok || p.TenantID != tenantID {
		return nil, httpx.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (s *memPayments) UpdateIntentState(_ context.Context, tenantID, id, state, pspRef string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	p, ok := s.m.intents[id]
	if !ok || p.TenantID != tenantID {
		return httpx.ErrNotFound
	}
	p.State = state
	if pspRef != "" {
		p.PSPRef = pspRef
	}
	return nil
}

func (s *memPayments) AddPosting(_ context.Context, p *model.LedgerPosting) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if existing, ok := s.m.postings[p.IdempotencyKey]; ok {
		*p = *existing
		return nil
	}
	cp := *p
	s.m.postings[p.IdempotencyKey] = &cp
	return nil
}

func (s *memPayments) Postings(_ context.Context, tenantID string) ([]model.LedgerPosting, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	out := []model.LedgerPosting{}
	for _, p := range s.m.postings {
		if p.TenantID == tenantID {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// ------------------------------------------------------------- conversations
type memConversations struct{ m *memory }

func (s *memConversations) CreateConversation(_ context.Context, c *model.Conversation) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *c
	s.m.conversations[c.ID] = &cp
	return nil
}

func (s *memConversations) GetConversation(_ context.Context, tenantID, id string) (*model.Conversation, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	c, ok := s.m.conversations[id]
	if !ok || c.TenantID != tenantID {
		return nil, httpx.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (s *memConversations) SetState(_ context.Context, tenantID, id, state string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	c, ok := s.m.conversations[id]
	if !ok || c.TenantID != tenantID {
		return httpx.ErrNotFound
	}
	c.State = state
	return nil
}

func (s *memConversations) AppendMessage(_ context.Context, msg *model.Message) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	s.m.messages[msg.ConversationID] = append(s.m.messages[msg.ConversationID], *msg)
	return nil
}

func (s *memConversations) Timeline(_ context.Context, tenantID, conversationID string) ([]model.Message, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	msgs := s.m.messages[conversationID]
	out := append([]model.Message(nil), msgs...)
	return out, nil
}

// ------------------------------------------------------------- handoffs
type memHandoffs struct{ m *memory }

func (s *memHandoffs) Open(_ context.Context, h *model.HandoffItem) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	cp := *h
	s.m.handoffs[h.ID] = &cp
	return nil
}

func (s *memHandoffs) Get(_ context.Context, tenantID, id string) (*model.HandoffItem, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	h, ok := s.m.handoffs[id]
	if !ok || h.TenantID != tenantID {
		return nil, httpx.ErrNotFound
	}
	cp := *h
	return &cp, nil
}

func (s *memHandoffs) Resolve(_ context.Context, tenantID, id, status, owner string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	h, ok := s.m.handoffs[id]
	if !ok || h.TenantID != tenantID {
		return httpx.ErrNotFound
	}
	h.Status = status
	h.Owner = owner
	return nil
}

func (s *memHandoffs) ListOpen(_ context.Context, tenantID string) ([]model.HandoffItem, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	out := []model.HandoffItem{}
	for _, h := range s.m.handoffs {
		if h.TenantID == tenantID && h.Status == "OPEN" {
			out = append(out, *h)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// ------------------------------------------------------------- outbox
type memOutbox struct{ m *memory }

func (s *memOutbox) Add(_ context.Context, e *model.OutboxEvent) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	e.ID = int64(len(s.m.outbox)) + 1
	e.CreatedAt = time.Now().UTC()
	s.m.outbox = append(s.m.outbox, *e)
	return nil
}

func (s *memOutbox) Unpublished(_ context.Context, limit int) ([]model.OutboxEvent, error) {
	s.m.mu.RLock()
	defer s.m.mu.RUnlock()
	out := s.m.outbox
	if len(out) > limit {
		out = out[:limit]
	}
	return append([]model.OutboxEvent(nil), out...), nil
}

func (s *memOutbox) MarkPublished(_ context.Context, ids []int64) error { return nil }

// ------------------------------------------------------------- attribution
type memAttribution struct{ m *memory }

func (s *memAttribution) Track(_ context.Context, e *model.AttributionEvent) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	s.m.attribution = append(s.m.attribution, *e)
	return nil
}
