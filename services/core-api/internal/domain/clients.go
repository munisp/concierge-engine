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
	return svc.ResolveV2(ctx, tenantID, ResolveInput{Phone: phone, Email: email, Name: name})
}

// ResolveInput extends resolution with registration context (Phase 16):
// locale/timezone from the capture surface localize checkout and outreach.
type ResolveInput struct {
	Phone    string `json:"phone"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Locale   string `json:"locale,omitempty"`   // BCP-47, e.g. from navigator.language
	Timezone string `json:"timezone,omitempty"` // IANA, e.g. from Intl.DateTimeFormat
}

// ResolveV2 is Resolve plus registration context. New clients get
// jurisdiction-aware consent defaults (GDPR opt-in vs US opt-out, derived
// from the phone calling code) and locale/timezone; existing clients are
// backfilled when their record lacks them.
func (svc *ClientService) ResolveV2(ctx context.Context, tenantID string, in ResolveInput) (*ResolveResult, error) {
	if in.Phone != "" {
		if c, err := svc.s.Store.Clients().FindByPhone(ctx, tenantID, in.Phone); err == nil {
			svc.backfillLocale(ctx, c, in)
			return &ResolveResult{Client: c, Confidence: 0.99, IsNew: false}, nil
		}
	}
	if in.Email != "" {
		if c, err := svc.s.Store.Clients().FindByEmail(ctx, tenantID, in.Email); err == nil {
			svc.backfillLocale(ctx, c, in)
			return &ResolveResult{Client: c, Confidence: 0.95, IsNew: false}, nil
		}
	}
	// New client: create a draft profile (progressive registration) with
	// regional consent defaults — EU/EEA/UK/CH are marketing opt-IN,
	// US/CA opt-OUT, unknown stays conservative (Phase 16).
	c := &model.Client{
		ID: NewID(), TenantID: tenantID, FullName: in.Name, Email: in.Email, Phone: in.Phone,
		Locale: strings.TrimSpace(in.Locale), Timezone: strings.TrimSpace(in.Timezone),
		KYCStatus: "none", Consent: ConsentDefaults(in.Phone), CreatedAt: svc.s.Now(),
	}
	if err := svc.s.Store.Clients().Upsert(ctx, c); err != nil {
		return nil, err
	}
	svc.recordRegistrationDefaults(ctx, tenantID, c.ID, c.Consent) // Phase 44 evidence genesis
	svc.s.Events.Emit(ctx, tenantID, "client", c.ID, "client.created", map[string]any{"source": "resolve"})
	return &ResolveResult{Client: c, Confidence: 0.5, IsNew: true}, nil
}

// backfillLocale fills locale/timezone on an existing record when the
// capture surface now knows them and the record does not. Advisory: a
// backfill failure never blocks resolution.
func (svc *ClientService) backfillLocale(ctx context.Context, c *model.Client, in ResolveInput) {
	if c.Erased() {
		return
	}
	changed := false
	if c.Locale == "" && in.Locale != "" {
		c.Locale = strings.TrimSpace(in.Locale)
		changed = true
	}
	if c.Timezone == "" && in.Timezone != "" {
		c.Timezone = strings.TrimSpace(in.Timezone)
		changed = true
	}
	if changed {
		_ = svc.s.Store.Clients().Upsert(ctx, c)
	}
}

func (svc *ClientService) Get(ctx context.Context, tenantID, id string) (*model.Client, error) {
	c, err := svc.s.Store.Clients().Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	// Sensitive read: audit-logged (Master Spec section 13).
	svc.s.Audit.Log(ctx, tenantID, "api", "client.read", "client", id, nil)
	return c, nil
}

// SavePreference stores a structured preference with consent provenance.
// VIP clients get field-level encryption at rest (Master Spec section 13);
// the returned copy is plaintext, the stored row carries enc:v1: ciphertext.
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
	stored := *p
	c, getErr := svc.s.Store.Clients().Get(ctx, tenantID, clientID)
	if getErr == nil {
		if err := requireNotErased(c); err != nil {
			return nil, err
		}
	}
	if getErr == nil && c.VIPFlagged() && svc.s.Cipher.Enabled() {
		enc, err := svc.s.Cipher.Encrypt(value)
		if err != nil {
			return nil, err // fail closed: never store VIP plaintext by accident
		}
		stored.Value = enc
		stored.Encrypted = true
	}
	if err := svc.s.Store.Clients().SavePreference(ctx, &stored); err != nil {
		return nil, err
	}
	p.Encrypted = stored.Encrypted
	svc.s.Events.Emit(ctx, tenantID, "client", clientID, "preference.saved",
		map[string]any{"key": key, "encrypted": stored.Encrypted})
	return p, nil
}

func (svc *ClientService) Preferences(ctx context.Context, tenantID, clientID string) ([]model.Preference, error) {
	prefs, err := svc.s.Store.Clients().Preferences(ctx, tenantID, clientID)
	if err != nil {
		return nil, err
	}
	// Sensitive read: audit-logged; VIP values are decrypted for the caller.
	svc.s.Audit.Log(ctx, tenantID, "api", "preferences.read", "client", clientID,
		map[string]any{"count": len(prefs)})
	for i := range prefs {
		if prefs[i].Encrypted {
			pt, err := svc.s.Cipher.Decrypt(prefs[i].Value)
			if err != nil {
				return nil, err // fail closed rather than serve ciphertext
			}
			prefs[i].Value = pt
		}
	}
	return prefs, nil
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
