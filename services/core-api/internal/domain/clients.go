// Client service: identity resolution across channels + consent-scoped
// preference memory (M6, AI spec §6).
package domain

import (
	"context"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type ClientService struct{ s *Services }

type ResolveResult struct {
	Client     *model.Client `json:"client"`
	Confidence float64       `json:"confidence"` // < 0.9 -> ask, never guess
	IsNew      bool          `json:"is_new"`
}

// Resolve implements resolve_client: phone is the identity anchor for
// WhatsApp; email second. Low-confidence matches are never returned as
// resolved — the agent must ask (AI spec §3.1).
func (svc *ClientService) Resolve(ctx context.Context, tenantID, phone, email, name string) (*ResolveResult, error) {
	if phone != "" {
		if c, err := svc.s.Store.Clients().FindByPhone(ctx, tenantID, phone); err == nil {
			return &ResolveResult{Client: c, Confidence: 0.99, IsNew: false}, nil
		}
	}
	if email != "" {
		if c, err := svc.s.Store.Clients().FindByEmail(ctx, tenantID, email); err == nil {
			return &ResolveResult{Client: c, Confidence: 0.95, IsNew: false}, nil
		}
	}
	// New client: create a draft profile (progressive registration).
	c := &model.Client{
		ID: NewID(), TenantID: tenantID, FullName: name, Email: email, Phone: phone,
		KYCStatus: "none", Consent: map[string]any{}, CreatedAt: svc.s.Now(),
	}
	if err := svc.s.Store.Clients().Upsert(ctx, c); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "client", c.ID, "client.created", map[string]any{"source": "resolve"})
	return &ResolveResult{Client: c, Confidence: 0.5, IsNew: true}, nil
}

func (svc *ClientService) Get(ctx context.Context, tenantID, id string) (*model.Client, error) {
	return svc.s.Store.Clients().Get(ctx, tenantID, id)
}

// SavePreference stores a structured preference with consent provenance.
func (svc *ClientService) SavePreference(ctx context.Context, tenantID, clientID, key, value, consentScope, sourceQuote, recordedBy string) (*model.Preference, error) {
	if key == "" || value == "" {
		return nil, httpx.ErrValidation
	}
	if consentScope == "" {
		consentScope = "service"
	}
	key = strings.ToLower(strings.TrimSpace(key))
	p := &model.Preference{
		ID: NewID(), TenantID: tenantID, ClientID: clientID, Key: key, Value: value,
		ConsentScope: consentScope, SourceQuote: sourceQuote, RecordedBy: recordedBy,
		RecordedAt: svc.s.Now(),
	}
	if err := svc.s.Store.Clients().SavePreference(ctx, p); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "client", clientID, "preference.saved", map[string]any{"key": key})
	return p, nil
}

func (svc *ClientService) Preferences(ctx context.Context, tenantID, clientID string) ([]model.Preference, error) {
	return svc.s.Store.Clients().Preferences(ctx, tenantID, clientID)
}

// DeletePreferences implements the DSAR cascade for preference memory
// (AI spec §6: deletion cascades Postgres record + vector index + features).
func (svc *ClientService) DeletePreferences(ctx context.Context, tenantID, clientID string) error {
	if err := svc.s.Store.Clients().DeletePreferences(ctx, tenantID, clientID); err != nil {
		return err
	}
	svc.s.Events.Emit(ctx, tenantID, "client", clientID, "dsar.preferences_deleted", nil)
	return nil
}
