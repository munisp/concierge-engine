// New-entrant instant-on (Phase 3, Master Spec §14 — "new-entrant
// instant-on"). One call provisions a tenant with a working default stack:
// sane pricing rules, a starter catalog, and the sandbox WhatsApp BSP
// channel so a new operator can take their first booking in minutes.
package domain

import (
	"context"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type InstantOnService struct{ s *Services }

// instantOnCatalog is the starter menu every new entrant gets — the three
// services that cover ~80% of VIP ground demand in the pilot cities.
var instantOnCatalog = []model.CatalogService{
	{Vertical: "ground", Name: "Airport Transfer (Business Class)", BaseMinor: 9500, PerKMMinor: 210, AutoQuotable: true},
	{Vertical: "ground", Name: "Hourly Chauffeur (Business Class)", BaseMinor: 6500, PerHourMinor: 5500, AutoQuotable: true},
	{Vertical: "ground", Name: "Meet & Greet (Arrivals)", BaseMinor: 7500, AutoQuotable: true},
	{Vertical: "air", Name: "Helicopter Transfer (on request)", BaseMinor: 0, AutoQuotable: false},
}

// Provision creates a fully-configured new-entrant tenant. Currency and
// region default to the platform's EU flagship values; both are overridable.
func (svc *InstantOnService) Provision(ctx context.Context, name, slug, region, currency string) (*model.Tenant, int, error) {
	if name == "" || slug == "" {
		return nil, 0, httpx.ErrValidation
	}
	t, err := svc.s.Tenants.Create(ctx, &model.Tenant{
		Name:      name,
		Slug:      slug,
		Archetype: "new_entrant",
		Region:    region, // tenants.Create validates + defaults EU
		Brand: map[string]any{
			"instant_on":       true,
			"sandbox_whatsapp": true, // sandbox BSP until production number approved
		},
		PricingRules: model.PricingRules{Currency: currency},
	})
	if err != nil {
		return nil, 0, err
	}
	seeded := 0
	for i := range instantOnCatalog {
		c := instantOnCatalog[i] // copy; Add assigns ID/Active
		c.TenantID = t.ID
		c.Currency = t.PricingRules.Currency
		if _, err := svc.s.Catalog.Add(ctx, &c); err != nil {
			return t, seeded, err
		}
		seeded++
	}
	svc.s.Events.Emit(ctx, t.ID, "tenant", t.ID, "tenant.instant_on",
		map[string]any{"slug": t.Slug, "catalog_seeded": seeded, "region": t.Region})
	return t, seeded, nil
}
