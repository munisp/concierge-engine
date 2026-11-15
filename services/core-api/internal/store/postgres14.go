//go:build pg

package store

// Phase 40: pg campaign spend ledger + attribution CountSince. RLS-scoped
// via tenantTx (FORCE RLS from db/migrations/0028_phase40.sql). Spend
// idempotency rides the partial UNIQUE (tenant_id, idempotency_key) WHERE
// key <> '': ON CONFLICT names the partial index by predicate, DO NOTHING
// yields no RETURNING row, and the domain reloads the original.

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (p *postgres) CampaignSpend() CampaignSpendStore { return pgCampaignSpend{p} }

type pgCampaignSpend struct{ p *postgres }

const campaignSpendCols = `id, tenant_id, campaign, channel, amount_minor,
	currency, spent_on, note, idempotency_key, created_at`

func (s pgCampaignSpend) Add(ctx context.Context, sp *model.CampaignSpend) error {
	tx, err := s.p.tenantTx(ctx, sp.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx,
		`INSERT INTO campaign_spend (`+campaignSpendCols+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7::date,$8,$9,$10)
		 ON CONFLICT (tenant_id, idempotency_key) WHERE idempotency_key <> ''
		 DO NOTHING RETURNING id`,
		sp.ID, sp.TenantID, sp.Campaign, sp.Channel, sp.AmountMinor,
		sp.Currency, sp.SpentOn, sp.Note, sp.IdempotencyKey, sp.CreatedAt).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return httpx.ErrConflict
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgCampaignSpend) List(ctx context.Context, tenantID, campaign string, limit int) ([]model.CampaignSpend, error) {
	if limit <= 0 {
		limit = 500
	}
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+campaignSpendCols+` FROM campaign_spend
		 WHERE tenant_id = $1 AND ($2 = '' OR campaign = $2)
		 ORDER BY spent_on DESC, created_at DESC LIMIT $3`, tenantID, campaign, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CampaignSpend
	for rows.Next() {
		var sp model.CampaignSpend
		var spentOn time.Time
		if err := rows.Scan(&sp.ID, &sp.TenantID, &sp.Campaign, &sp.Channel,
			&sp.AmountMinor, &sp.Currency, &spentOn, &sp.Note,
			&sp.IdempotencyKey, &sp.CreatedAt); err != nil {
			return nil, err
		}
		sp.SpentOn = spentOn.Format("2006-01-02")
		out = append(out, sp)
	}
	return out, rows.Err()
}

// CountSince (Phase 40): the deliverability guard's 7-day window. Events
// counted on their recorded `at` (default now() at insert).
func (s pgAttribution) CountSince(ctx context.Context, tenantID, kind, channel string, since time.Time) (int, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM attribution_events
		 WHERE tenant_id = $1 AND kind = $2
		   AND ($3 = '' OR channel = $3) AND at >= $4`,
		tenantID, kind, channel, since).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
