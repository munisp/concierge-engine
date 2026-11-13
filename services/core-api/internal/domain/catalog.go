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
	if err := svc.s.Store.Catalog().Add(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (svc *CatalogService) List(ctx context.Context, tenantID string) ([]model.CatalogService, error) {
	return svc.s.Store.Catalog().List(ctx, tenantID)
}
