// Public API keys (Phase 3 — public API + webhooks marketplace). Keys are
// tenant-scoped bearer credentials for the public surface. Secrets are shown
// once at creation; only the SHA-256 hash persists. The prefix ("cek_" + 8
// chars) is the lookup key — safe to log, useless without the secret.
package domain

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type ApiKeyService struct{ s *Services }

var validScopes = map[string]bool{
	"read": true, "write": true, "webhooks:manage": true,
	// Phase 56: "partner" scopes a key to the /v1/partner/* surface for one
	// partner; it grants nothing on the tenant-wide API.
	"partner": true,
}

// Create mints a key and returns the full secret exactly once.
func (svc *ApiKeyService) Create(ctx context.Context, tenantID, name string, scopes []string) (*model.ApiKey, string, error) {
	return svc.create(ctx, tenantID, "", name, scopes)
}

// CreatePartnerKey mints a key bound to one partner (Phase 56). The key
// authenticates only /v1/partner/* requests; the middleware rejects it
// everywhere else. Secret shown once, as with tenant keys.
func (svc *ApiKeyService) CreatePartnerKey(ctx context.Context, tenantID, partnerID, name string) (*model.ApiKey, string, error) {
	if partnerID == "" {
		return nil, "", httpx.ErrValidation
	}
	if _, err := svc.s.Store.Partners().Get(ctx, tenantID, partnerID); err != nil {
		return nil, "", err
	}
	return svc.create(ctx, tenantID, partnerID, name, []string{"partner"})
}

func (svc *ApiKeyService) create(ctx context.Context, tenantID, partnerID, name string, scopes []string) (*model.ApiKey, string, error) {
	if name == "" {
		return nil, "", httpx.ErrValidation
	}
	for _, sc := range scopes {
		if !validScopes[sc] {
			return nil, "", httpx.ErrValidation
		}
	}
	if len(scopes) == 0 {
		scopes = []string{"read"}
	}
	raw := make([]byte, 24) // 192-bit secret
	if _, err := rand.Read(raw); err != nil {
		return nil, "", err
	}
	secret := "cek_" + hex.EncodeToString(raw)
	k := &model.ApiKey{
		ID: NewID(), TenantID: tenantID, Name: name,
		Prefix:     secret[:12], // "cek_" + 8 hex chars
		SecretHash: hashApiKey(secret),
		Scopes:     scopes, PartnerID: partnerID, CreatedAt: svc.s.Now(),
	}
	if err := svc.s.Store.ApiKeys().Create(ctx, k); err != nil {
		return nil, "", err
	}
	svc.s.Audit.Log(ctx, tenantID, "api", "apikey.created", "apikey", k.ID, map[string]any{"name": name, "partner_id": partnerID})
	return k, secret, nil
}

func hashApiKey(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// Authenticate resolves "Bearer cek_..." to (tenantID, scopes). Constant-time
// hash comparison; revoked keys never authenticate.
func (svc *ApiKeyService) Authenticate(ctx context.Context, header string) (string, []string, error) {
	tid, scopes, _, err := svc.AuthenticateTenant(ctx, header)
	return tid, scopes, err
}

// AuthenticateTenant is the tenantctx.KeyAuthenticator: it additionally
// returns the partner the key is bound to ("" for tenant-wide keys).
func (svc *ApiKeyService) AuthenticateTenant(ctx context.Context, header string) (string, []string, string, error) {
	const prefix = "Bearer cek_"
	if !strings.HasPrefix(header, prefix) {
		return "", nil, "", httpx.ErrForbidden
	}
	secret := strings.TrimPrefix(header, "Bearer ")
	k, err := svc.s.Store.ApiKeys().GetByPrefix(ctx, secret[:12])
	if err != nil {
		return "", nil, "", httpx.ErrForbidden
	}
	if k.Revoked || subtle.ConstantTimeCompare([]byte(k.SecretHash), []byte(hashApiKey(secret))) != 1 {
		return "", nil, "", httpx.ErrForbidden
	}
	_ = svc.s.Store.ApiKeys().TouchUsed(ctx, k.ID, svc.s.Now())
	return k.TenantID, k.Scopes, k.PartnerID, nil
}

// HasScope reports whether scopes cover need ("write" implies "read").
func HasScope(scopes []string, need string) bool {
	for _, s := range scopes {
		if s == need || (need == "read" && s == "write") {
			return true
		}
	}
	return false
}

func (svc *ApiKeyService) List(ctx context.Context, tenantID string) ([]model.ApiKey, error) {
	return svc.s.Store.ApiKeys().List(ctx, tenantID)
}

func (svc *ApiKeyService) Revoke(ctx context.Context, tenantID, id string) error {
	if err := svc.s.Store.ApiKeys().Revoke(ctx, tenantID, id); err != nil {
		return err
	}
	svc.s.Audit.Log(ctx, tenantID, "api", "apikey.revoked", "apikey", id, nil)
	return nil
}
