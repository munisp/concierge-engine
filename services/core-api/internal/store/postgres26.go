//go:build pg

package store

// Phase 66: Postgres bill-to-desk folio store — upsert keyed by
// (tenant, partner, period, currency), RLS via tenantTx.

import (
	"context"
	"encoding/json"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// nullTime maps a zero time to NULL for nullable timestamptz columns.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func (p *postgres) Folios() FolioStore { return pgFolios{p} }

type pgFolios struct{ p *postgres }

// AccrueLine appends one booking's charge to the OPEN folio, creating the
// folio on first use. The booking-once guarantee is enforced in Go against
// the row the upsert returns (jsonb lines carry booking_id).
func (s pgFolios) AccrueLine(ctx context.Context, f *model.Folio, line model.FolioLine) (bool, error) {
	tx, err := s.p.tenantTx(ctx, f.TenantID)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	lineJSON, err := json.Marshal(line)
	if err != nil {
		return false, err
	}
	// Upsert the OPEN folio shell, then read its lines to decide idempotency.
	var existing []byte
	var alreadyIssued bool
	err = tx.QueryRowContext(ctx,
		`INSERT INTO partner_folios (id, tenant_id, partner_id, period, currency, state, total_minor, lines, created_at)
		 VALUES ($1,$2,$3,$4,$5,'OPEN',0,'[]',$6)
		 ON CONFLICT (tenant_id, partner_id, period, currency)
		 DO UPDATE SET id = partner_folios.id
		 RETURNING lines, state <> 'OPEN'`,
		f.ID, f.TenantID, f.PartnerID, f.Period, f.Currency, f.CreatedAt).Scan(&existing, &alreadyIssued)
	if err != nil {
		return false, err
	}
	if alreadyIssued {
		return false, httpx.ErrConflict // never accrue onto an issued/settled folio
	}
	var lines []model.FolioLine
	if err := json.Unmarshal(existing, &lines); err != nil {
		return false, err
	}
	for _, l := range lines {
		if l.BookingID == line.BookingID {
			return false, tx.Commit() // idempotent replay — one booking bills once
		}
	}
	created := len(lines) == 0
	if _, err := tx.ExecContext(ctx,
		`UPDATE partner_folios
		 SET lines = lines || $5::jsonb, total_minor = total_minor + $4
		 WHERE tenant_id=$1 AND partner_id=$2 AND period=$3 AND currency=$6`,
		f.TenantID, f.PartnerID, f.Period, line.AmountMinor, string(lineJSON), f.Currency); err != nil {
		return false, err
	}
	return created, tx.Commit()
}

func scanFolio(row interface{ Scan(...any) error }) (*model.Folio, error) {
	var f model.Folio
	var lines []byte
	var issuedAt, settledAt *time.Time
	err := row.Scan(&f.ID, &f.TenantID, &f.PartnerID, &f.Period, &f.Currency,
		&f.State, &f.TotalMinor, &lines, &f.CreatedAt, &issuedAt, &settledAt)
	if err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal(lines, &f.Lines)
	if issuedAt != nil {
		f.IssuedAt = *issuedAt
	}
	if settledAt != nil {
		f.SettledAt = *settledAt
	}
	return &f, nil
}

const folioCols = `id, tenant_id, partner_id, period, currency, state, total_minor, lines, created_at, issued_at, settled_at`

func (s pgFolios) ListForPartner(ctx context.Context, tenantID, partnerID string) ([]model.Folio, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+folioCols+` FROM partner_folios WHERE tenant_id=$1 AND partner_id=$2
		 ORDER BY period DESC, currency`, tenantID, partnerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Folio{}
	for rows.Next() {
		f, err := scanFolio(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (s pgFolios) Get(ctx context.Context, tenantID, id string) (*model.Folio, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	f, err := scanFolio(tx.QueryRowContext(ctx,
		`SELECT `+folioCols+` FROM partner_folios WHERE tenant_id=$1 AND id=$2`, tenantID, id))
	if err != nil {
		return nil, err
	}
	return f, tx.Commit()
}

func (s pgFolios) Update(ctx context.Context, f *model.Folio) error {
	tx, err := s.p.tenantTx(ctx, f.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE partner_folios SET state=$3, total_minor=$4, issued_at=$5, settled_at=$6
		 WHERE tenant_id=$1 AND id=$2`,
		f.TenantID, f.ID, f.State, f.TotalMinor,
		nullTime(f.IssuedAt), nullTime(f.SettledAt))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}
