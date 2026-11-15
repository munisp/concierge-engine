//go:build pg

package store

// Phase 41: pg email suppressions — RLS-scoped via tenantTx (FORCE RLS from
// db/migrations/0029_phase41.sql). Idempotency rides UNIQUE (tenant_id,
// email): ON CONFLICT DO NOTHING yields no RETURNING row → ErrConflict, and
// the domain returns the existing suppression.

import (
	"context"
	"database/sql"
	"errors"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (p *postgres) EmailSuppressions() EmailSuppressionStore { return pgEmailSuppressions{p} }

type pgEmailSuppressions struct{ p *postgres }

const suppressionCols = `id, tenant_id, email, reason, source_ref, created_at`

func (s pgEmailSuppressions) Add(ctx context.Context, sup *model.EmailSuppression) error {
	tx, err := s.p.tenantTx(ctx, sup.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx,
		`INSERT INTO email_suppressions (`+suppressionCols+`)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (tenant_id, email) DO NOTHING RETURNING id`,
		sup.ID, sup.TenantID, sup.Email, sup.Reason, sup.SourceRef, sup.CreatedAt).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return httpx.ErrConflict
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgEmailSuppressions) Exists(ctx context.Context, tenantID, email string) (bool, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM email_suppressions WHERE tenant_id = $1 AND email = $2`,
		tenantID, email).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s pgEmailSuppressions) List(ctx context.Context, tenantID string, limit int) ([]model.EmailSuppression, error) {
	if limit <= 0 {
		limit = 500
	}
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+suppressionCols+` FROM email_suppressions
		 WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.EmailSuppression
	for rows.Next() {
		var sup model.EmailSuppression
		if err := rows.Scan(&sup.ID, &sup.TenantID, &sup.Email, &sup.Reason,
			&sup.SourceRef, &sup.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sup)
	}
	return out, rows.Err()
}

func (s pgEmailSuppressions) Delete(ctx context.Context, tenantID, email string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`DELETE FROM email_suppressions WHERE tenant_id = $1 AND email = $2`, tenantID, email)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}
