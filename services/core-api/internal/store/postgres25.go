//go:build pg

package store

// Phase 65: Postgres partner rate-card store — upsert keyed by
// (tenant, partner, vertical), RLS via tenantTx.

import (
	"context"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (p *postgres) RateCards() RateCardStore { return pgRateCards{p} }

type pgRateCards struct{ p *postgres }

func (s pgRateCards) Upsert(ctx context.Context, c *model.RateCard) error {
	tx, err := s.p.tenantTx(ctx, c.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO partner_rate_cards (tenant_id, partner_id, vertical, kind, value, currency, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,nullif($6,''),$7,$8)
		 ON CONFLICT (tenant_id, partner_id, vertical)
		 DO UPDATE SET kind = EXCLUDED.kind, value = EXCLUDED.value,
		               currency = EXCLUDED.currency, updated_at = EXCLUDED.updated_at`,
		c.TenantID, c.PartnerID, c.Vertical, c.Kind, c.Value, c.Currency, c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgRateCards) ListForPartner(ctx context.Context, tenantID, partnerID string) ([]model.RateCard, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT tenant_id, partner_id, vertical, kind, value, coalesce(currency,''), created_at, updated_at
		 FROM partner_rate_cards WHERE tenant_id=$1 AND partner_id=$2
		 ORDER BY vertical`, tenantID, partnerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.RateCard{}
	for rows.Next() {
		var c model.RateCard
		if err := rows.Scan(&c.TenantID, &c.PartnerID, &c.Vertical, &c.Kind, &c.Value, &c.Currency, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (s pgRateCards) Delete(ctx context.Context, tenantID, partnerID, vertical string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`DELETE FROM partner_rate_cards WHERE tenant_id=$1 AND partner_id=$2 AND vertical=$3`,
		tenantID, partnerID, vertical)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}
