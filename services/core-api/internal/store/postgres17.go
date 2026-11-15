//go:build pg

package store

// Phase 44: pg consent evidence ledger — RLS-scoped via tenantTx (FORCE
// RLS from db/migrations/0031_phase44.sql). Append-only: Record inserts,
// there is no update/delete path by design.

import (
	"context"

	"concierge-engine/services/core-api/internal/model"
)

func (p *postgres) ConsentEvents() ConsentEventStore { return pgConsentEvents{p} }

type pgConsentEvents struct{ p *postgres }

const consentEventCols = `id, tenant_id, client_id, scope, granted, source, detail, created_at`

func (s pgConsentEvents) Record(ctx context.Context, e *model.ConsentEvent) error {
	tx, err := s.p.tenantTx(ctx, e.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO consent_events (`+consentEventCols+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7, COALESCE($8, now()))`,
		e.ID, e.TenantID, e.ClientID, e.Scope, e.Granted, e.Source, e.Detail, nilIfZero(e.CreatedAt))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgConsentEvents) ListForClient(ctx context.Context, tenantID, clientID string, limit int) ([]model.ConsentEvent, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if limit <= 0 {
		limit = 500
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT `+consentEventCols+` FROM consent_events
		 WHERE tenant_id = $1 AND client_id = $2
		 ORDER BY created_at DESC LIMIT $3`, tenantID, clientID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ConsentEvent
	for rows.Next() {
		var e model.ConsentEvent
		if err := rows.Scan(&e.ID, &e.TenantID, &e.ClientID, &e.Scope,
			&e.Granted, &e.Source, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}
