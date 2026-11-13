package domain

import (
	"context"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type TenantService struct{ s *Services }

func (svc *TenantService) Create(ctx context.Context, t *model.Tenant) (*model.Tenant, error) {
	if t.Slug == "" || t.Name == "" {
		return nil, httpx.ErrValidation
	}
	t.Slug = strings.ToLower(t.Slug)
	if t.ID == "" {
		t.ID = NewID()
	}
	if t.Archetype == "" {
		t.Archetype = "new_entrant"
	}
	if t.PricingRules.Currency == "" {
		t.PricingRules.Currency = "EUR"
		t.PricingRules.DepositPct = 0.30
		t.PricingRules.QuoteTTLSeconds = 3600
		t.PricingRules.PlatformFeePct = 0.03
	}
	if t.SLAConfig.QuoteSLASeconds == 0 {
		t.SLAConfig.QuoteSLASeconds = 3600 // Taylor's 1-hour promise
	}
	if t.Tier2LimitMinor == 0 {
		t.Tier2LimitMinor = 50000
	}
	// Phase 3: regional residency (Master Spec §13). EU is the default home;
	// UAE is the only other residency region currently offered.
	if t.Region == "" {
		t.Region = "EU"
	}
	if t.Region != "EU" && t.Region != "UAE" {
		return nil, httpx.ErrValidation
	}
	if t.ReferralFeePct == 0 {
		t.ReferralFeePct = 0.10 // network default; 0 means "unset", not "free"
	}
	t.CreatedAt = svc.s.Now()
	if err := svc.s.Store.Tenants().Create(ctx, t); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, t.ID, "tenant", t.ID, "tenant.created", map[string]any{"slug": t.Slug})
	return t, nil
}

func (svc *TenantService) Get(ctx context.Context, id string) (*model.Tenant, error) {
	return svc.s.Store.Tenants().Get(ctx, id)
}

func (svc *TenantService) List(ctx context.Context) ([]model.Tenant, error) {
	return svc.s.Store.Tenants().List(ctx)
}

func (svc *TenantService) UpdateConfig(ctx context.Context, t *model.Tenant) (*model.Tenant, error) {
	if err := svc.s.Store.Tenants().UpdateConfig(ctx, t); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, t.ID, "tenant", t.ID, "tenant.config_updated", nil)
	return t, nil
}

// SetAutonomyDial changes the AI autonomy dial (0..3). Raised only on eval
// evidence (AI spec §7) — enforced here by requiring an evidence reference.
func (svc *TenantService) SetAutonomyDial(ctx context.Context, tenantID string, dial int, evidenceRef string) error {
	if dial < 0 || dial > 3 {
		return httpx.ErrValidation
	}
	if dial > 0 && evidenceRef == "" {
		return httpx.ErrValidation // autonomy dial rises only on eval evidence
	}
	t, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return err
	}
	t.AutonomyDial = dial
	if err := svc.s.Store.Tenants().UpdateConfig(ctx, t); err != nil {
		return err
	}
	svc.s.Events.Emit(ctx, tenantID, "tenant", tenantID, "tenant.autonomy_changed",
		map[string]any{"dial": dial, "evidence": evidenceRef})
	return nil
}
