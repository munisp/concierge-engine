//go:build pg

// Phase 20: pg promo campaigns — RLS-scoped CRUD + redemption counting.
// Codes are stored uppercase; lookups normalize.
package store

import (
	"context"
	"database/sql"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (p *postgres) Promos() PromoStore { return pgPromos{p} }

type pgPromos struct{ p *postgres }

func (s pgPromos) Create(ctx context.Context, c *model.PromoCampaign) error {
	tx, err := s.p.tenantTx(ctx, c.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx,
		`INSERT INTO promo_campaigns (id, tenant_id, code, credit_minor, currency, max_redemptions, expires_at, active, gift, switch_offer)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		 ON CONFLICT (tenant_id, code) DO NOTHING RETURNING id`,
		c.ID, c.TenantID, strings.ToUpper(c.Code), c.CreditMinor, c.Currency,
		c.MaxRedemptions, nilIfZero(c.ExpiresAt), c.Active, c.Gift, c.SwitchOffer).Scan(&id)
	if err == sql.ErrNoRows {
		return httpx.ErrConflict
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgPromos) GetByCode(ctx context.Context, tenantID, code string) (*model.PromoCampaign, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var c model.PromoCampaign
	var expiresAt sql.NullTime
	err = tx.QueryRowContext(ctx,
		`SELECT id, tenant_id, code, credit_minor, currency, max_redemptions, expires_at, active, gift, switch_offer, created_at
		 FROM promo_campaigns WHERE code=$1`, strings.ToUpper(code)).
		Scan(&c.ID, &c.TenantID, &c.Code, &c.CreditMinor, &c.Currency,
			&c.MaxRedemptions, &expiresAt, &c.Active, &c.Gift, &c.SwitchOffer, &c.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	if expiresAt.Valid {
		c.ExpiresAt = expiresAt.Time
	}
	return &c, tx.Commit()
}

func (s pgPromos) List(ctx context.Context, tenantID string) ([]model.PromoCampaign, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, code, credit_minor, currency, max_redemptions, expires_at, active, gift, switch_offer, created_at
		 FROM promo_campaigns ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.PromoCampaign
	for rows.Next() {
		var c model.PromoCampaign
		var expiresAt sql.NullTime
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Code, &c.CreditMinor, &c.Currency,
			&c.MaxRedemptions, &expiresAt, &c.Active, &c.Gift, &c.SwitchOffer, &c.CreatedAt); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			c.ExpiresAt = expiresAt.Time
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s pgPromos) Redemptions(ctx context.Context, tenantID, code string) (int, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM client_rewards WHERE promo_code=$1`, strings.ToUpper(code)).Scan(&n); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}
