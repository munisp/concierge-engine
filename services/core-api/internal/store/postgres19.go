//go:build pg

package store

// Phase 47: KYC / identity verification lane — pg implementation. All
// RLS-scoped via tenantTx with explicit tenant_id predicates (same
// discipline as every other pg store). The one-open-case rule and the
// idempotency key land on the partial unique indexes from migration 0033;
// a unique violation surfaces as ErrConflict so the domain can distinguish
// replay (reload via GetByIdem) from a genuine second open case.

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (p *postgres) KycCases() KycCaseStore { return pgKycCases{p} }

type pgKycCases struct{ p *postgres }

const kycCaseCols = `id, tenant_id, client_id, level, status, provider,
	document_type, document_ref, decision_reason, decided_by,
	submitted_at, decided_at, idempotency_key, created_at, updated_at`

func (s pgKycCases) Create(ctx context.Context, c *model.KycCase) error {
	tx, err := s.p.tenantTx(ctx, c.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO kyc_cases (`+kycCaseCols+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		c.ID, c.TenantID, c.ClientID, c.Level, c.Status, c.Provider,
		c.DocumentType, c.DocumentRef, c.DecisionReason, c.DecidedBy,
		nilIfZero(c.SubmittedAt), nilIfZero(c.DecidedAt),
		c.IdempotencyKey, c.CreatedAt, c.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return httpx.ErrConflict
		}
		return err
	}
	return tx.Commit()
}

func scanKycCase(row interface{ Scan(...any) error }) (*model.KycCase, error) {
	var c model.KycCase
	var submittedAt, decidedAt *time.Time
	err := row.Scan(&c.ID, &c.TenantID, &c.ClientID, &c.Level, &c.Status, &c.Provider,
		&c.DocumentType, &c.DocumentRef, &c.DecisionReason, &c.DecidedBy,
		&submittedAt, &decidedAt, &c.IdempotencyKey, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if submittedAt != nil {
		c.SubmittedAt = *submittedAt
	}
	if decidedAt != nil {
		c.DecidedAt = *decidedAt
	}
	return &c, nil
}

func (s pgKycCases) Get(ctx context.Context, tenantID, id string) (*model.KycCase, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	c, err := scanKycCase(tx.QueryRowContext(ctx,
		`SELECT `+kycCaseCols+` FROM kyc_cases WHERE tenant_id = $1 AND id = $2`, tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return c, tx.Commit()
}

func (s pgKycCases) GetByIdem(ctx context.Context, tenantID, key string) (*model.KycCase, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	c, err := scanKycCase(tx.QueryRowContext(ctx,
		`SELECT `+kycCaseCols+` FROM kyc_cases WHERE tenant_id = $1 AND idempotency_key = $2`, tenantID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return c, tx.Commit()
}

func (s pgKycCases) OpenFor(ctx context.Context, tenantID, clientID, level string) (*model.KycCase, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	c, err := scanKycCase(tx.QueryRowContext(ctx,
		`SELECT `+kycCaseCols+` FROM kyc_cases
		 WHERE tenant_id = $1 AND client_id = $2 AND level = $3
		   AND status IN ('PENDING', 'IN_REVIEW')
		 ORDER BY created_at DESC LIMIT 1`, tenantID, clientID, level))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return c, tx.Commit()
}

func (s pgKycCases) Update(ctx context.Context, c *model.KycCase) error {
	tx, err := s.p.tenantTx(ctx, c.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE kyc_cases SET status=$3, provider=$4, document_type=$5, document_ref=$6,
			decision_reason=$7, decided_by=$8, submitted_at=$9, decided_at=$10, updated_at=$11
		 WHERE tenant_id=$1 AND id=$2`,
		c.TenantID, c.ID, c.Status, c.Provider, c.DocumentType, c.DocumentRef,
		c.DecisionReason, c.DecidedBy,
		nilIfZero(c.SubmittedAt), nilIfZero(c.DecidedAt), c.UpdatedAt)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

func (s pgKycCases) List(ctx context.Context, tenantID, status string, limit int) ([]model.KycCase, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if limit <= 0 {
		limit = 200
	}
	var rows *sql.Rows
	if status == "" {
		rows, err = tx.QueryContext(ctx,
			`SELECT `+kycCaseCols+` FROM kyc_cases WHERE tenant_id = $1
			 ORDER BY created_at DESC LIMIT $2`, tenantID, limit)
	} else {
		rows, err = tx.QueryContext(ctx,
			`SELECT `+kycCaseCols+` FROM kyc_cases WHERE tenant_id = $1 AND status = $2
			 ORDER BY created_at DESC LIMIT $3`, tenantID, status, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.KycCase
	for rows.Next() {
		c, err := scanKycCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (s pgKycCases) ListForClient(ctx context.Context, tenantID, clientID string) ([]model.KycCase, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+kycCaseCols+` FROM kyc_cases WHERE tenant_id = $1 AND client_id = $2
		 ORDER BY created_at DESC`, tenantID, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.KycCase
	for rows.Next() {
		c, err := scanKycCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

// ExpireBefore flips stale PENDING/IN_REVIEW cases to EXPIRED. The status
// predicate keeps it idempotent under the hourly sweep.
func (s pgKycCases) ExpireBefore(ctx context.Context, tenantID string, before time.Time) (int, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE kyc_cases SET status = 'EXPIRED', updated_at = now()
		 WHERE tenant_id = $1 AND created_at < $2 AND status IN ('PENDING', 'IN_REVIEW')`,
		tenantID, before)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

// ScrubDocumentRefsForClient is the Art. 17 companion: document pointers go
// immediately on erasure; the case row (level, decision, timestamps)
// survives as the AML accountability record. Idempotent via the <> ” guard.
func (s pgKycCases) ScrubDocumentRefsForClient(ctx context.Context, tenantID, clientID string) (int, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE kyc_cases SET document_type = '', document_ref = '', updated_at = now()
		 WHERE tenant_id = $1 AND client_id = $2 AND (document_type <> '' OR document_ref <> '')`,
		tenantID, clientID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}
