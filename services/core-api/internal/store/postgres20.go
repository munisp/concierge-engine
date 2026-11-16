//go:build pg

package store

// Phase 48: settlement batches (pg) + the payout list helpers the batching
// lane needs. Same RLS discipline as every other pg store: tenantTx +
// explicit tenant_id predicates.

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (s pgPayouts) ListPendingForPartner(ctx context.Context, tenantID, partnerID string) ([]model.Payout, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+payoutCols+` FROM payouts
		 WHERE tenant_id=$1 AND partner_id=$2 AND state='PENDING' AND batch_id IS NULL ORDER BY created_at`, tenantID, partnerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Payout
	for rows.Next() {
		p, err := scanPayout(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (s pgPayouts) ListByBatch(ctx context.Context, tenantID, batchID string) ([]model.Payout, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+payoutCols+` FROM payouts
		 WHERE tenant_id=$1 AND batch_id=$2 ORDER BY created_at`, tenantID, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Payout
	for rows.Next() {
		p, err := scanPayout(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (p *postgres) Settlements() SettlementBatchStore { return pgBatches{p} }

type pgBatches struct{ p *postgres }

const batchCols = `id, tenant_id, partner_id, currency, amount_minor, receive_minor, fees_minor,
	state, quote_id, transfer_id, quote_expires_at, idempotency_key, created_at, updated_at`

func scanBatch(row interface{ Scan(...any) error }) (*model.SettlementBatch, error) {
	var b model.SettlementBatch
	var quoteExpiresAt *time.Time
	err := row.Scan(&b.ID, &b.TenantID, &b.PartnerID, &b.Currency, &b.AmountMinor,
		&b.ReceiveMinor, &b.FeesMinor, &b.State, &b.QuoteID, &b.TransferID,
		&quoteExpiresAt, &b.IdempotencyKey, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if quoteExpiresAt != nil {
		b.QuoteExpiresAt = *quoteExpiresAt
	}
	return &b, nil
}

func (s pgBatches) Create(ctx context.Context, b *model.SettlementBatch) error {
	tx, err := s.p.tenantTx(ctx, b.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO settlement_batches (`+batchCols+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		b.ID, b.TenantID, b.PartnerID, b.Currency, b.AmountMinor, b.ReceiveMinor, b.FeesMinor,
		b.State, b.QuoteID, b.TransferID, nilIfZero(b.QuoteExpiresAt), b.IdempotencyKey,
		b.CreatedAt, b.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return httpx.ErrConflict
		}
		return err
	}
	return tx.Commit()
}

func (s pgBatches) Get(ctx context.Context, tenantID, id string) (*model.SettlementBatch, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	b, err := scanBatch(tx.QueryRowContext(ctx,
		`SELECT `+batchCols+` FROM settlement_batches WHERE tenant_id=$1 AND id=$2`, tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return b, tx.Commit()
}

func (s pgBatches) GetByIdem(ctx context.Context, tenantID, key string) (*model.SettlementBatch, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	b, err := scanBatch(tx.QueryRowContext(ctx,
		`SELECT `+batchCols+` FROM settlement_batches WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return b, tx.Commit()
}

func (s pgBatches) GetByTransfer(ctx context.Context, tenantID, transferID string) (*model.SettlementBatch, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	b, err := scanBatch(tx.QueryRowContext(ctx,
		`SELECT `+batchCols+` FROM settlement_batches WHERE tenant_id=$1 AND transfer_id=$2`, tenantID, transferID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return b, tx.Commit()
}

func (s pgBatches) Update(ctx context.Context, b *model.SettlementBatch) error {
	tx, err := s.p.tenantTx(ctx, b.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE settlement_batches SET receive_minor=$3, fees_minor=$4, state=$5, quote_id=$6,
			transfer_id=$7, quote_expires_at=$8, updated_at=$9
		 WHERE tenant_id=$1 AND id=$2`,
		b.TenantID, b.ID, b.ReceiveMinor, b.FeesMinor, b.State, b.QuoteID,
		b.TransferID, nilIfZero(b.QuoteExpiresAt), b.UpdatedAt)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

func (s pgBatches) List(ctx context.Context, tenantID string) ([]model.SettlementBatch, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+batchCols+` FROM settlement_batches WHERE tenant_id=$1 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SettlementBatch
	for rows.Next() {
		b, err := scanBatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}
