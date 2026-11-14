package domain

import (
	"context"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type CatalogService struct{ s *Services }

func (svc *CatalogService) Add(ctx context.Context, c *model.CatalogService) (*model.CatalogService, error) {
	if c.Vertical == "" || c.Name == "" || c.Currency == "" {
		return nil, httpx.ErrValidation
	}
	if c.ID == "" {
		c.ID = NewID()
	}
	// A service you add is live by default — deactivation is a separate,
	// deliberate operation. Without this, POST /v1/catalog creates an entry
	// that is invisible to the quote engine (List filters on Active).
	c.Active = true
	// Phase 31: a card image must reference an existing tenant media row —
	// dangling ids are rejected at write time, not discovered on the site.
	if c.ImageID != "" {
		if _, err := svc.s.Store.Media().Get(ctx, c.TenantID, c.ImageID); err != nil {
			return nil, httpx.ErrValidation
		}
	}
	if err := svc.s.Store.Catalog().Add(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (svc *CatalogService) List(ctx context.Context, tenantID string) ([]model.CatalogService, error) {
	return svc.s.Store.Catalog().List(ctx, tenantID)
}
