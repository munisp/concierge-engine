//go:build pg

// Phase 18: pg client rewards — accrual (idempotent on the UNIQUE key),
// balance, transactional oldest-first redemption inside the tenant RLS
// transaction.
package store

import (
	"context"
	"database/sql"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (p *postgres) Rewards() RewardStore { return pgRewards{p} }

type pgRewards struct{ p *postgres }

func (s pgRewards) Accrue(ctx context.Context, r *model.ClientReward) error {
	tx, err := s.p.tenantTx(ctx, r.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx,
		`INSERT INTO client_rewards (id, tenant_id, client_id, source_booking_id, kind, amount_minor, currency)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (tenant_id, client_id, source_booking_id) DO NOTHING RETURNING id`,
		r.ID, r.TenantID, r.ClientID, r.SourceBookingID, r.Kind, r.AmountMinor, r.Currency).Scan(&id)
	if err == sql.ErrNoRows {
		return httpx.ErrConflict // replayed conversion — never double-credit
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgRewards) Balance(ctx context.Context, tenantID, clientID string) (int64, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var bal int64
	if err := tx.QueryRowContext(ctx,
		`SELECT coalesce(sum(amount_minor),0) FROM client_rewards
		 WHERE client_id=$1 AND state='ACCRUED'`, clientID).Scan(&bal); err != nil {
		return 0, err
	}
	return bal, tx.Commit()
}

func (s pgRewards) Redeem(ctx context.Context, tenantID, clientID, intentID string, amount int64) (int64, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, amount_minor FROM client_rewards
		 WHERE client_id=$1 AND state='ACCRUED' ORDER BY created_at ASC, id ASC FOR UPDATE`, clientID)
	if err != nil {
		return 0, err
	}
	type row struct {
		id     string
		amount int64
	}
	var accrued []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.amount); err != nil {
			rows.Close()
			return 0, err
		}
		accrued = append(accrued, r)
	}
	rows.Close()
	var covered int64
	for _, r := range accrued {
		if covered >= amount {
			break
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE client_rewards SET state='REDEEMED', redeemed_intent_id=$2, redeemed_at=now()
			 WHERE id=$1`, r.id, intentID); err != nil {
			return 0, err
		}
		covered += r.amount
	}
	return covered, tx.Commit()
}

func (s pgRewards) RefundRedemption(ctx context.Context, tenantID, intentID string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE client_rewards SET state='ACCRUED', redeemed_intent_id='', redeemed_at=NULL
		 WHERE redeemed_intent_id=$1 AND state='REDEEMED'`, intentID); err != nil {
		return err
	}
	return tx.Commit()
}
