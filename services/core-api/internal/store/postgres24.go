//go:build pg

package store

import (
	"context"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// Phase 63: demand-channel listings. RLS scopes the tenant inside
// tenantTx; the partial index service_requests_source_partner_idx covers
// this exact shape. Source columns ride along on every request read.
func (s pgRequests) ForSource(ctx context.Context, tenantID, partnerID string, limit int) ([]model.ServiceRequest, error) {
	if limit <= 0 {
		limit = 50
	}
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, client_id, coalesce(conversation_id::text,''), state, version,
		        idempotency_key, source_kind, coalesce(source_partner_id::text,''), created_at, updated_at
		 FROM service_requests WHERE source_partner_id = $1
		 ORDER BY created_at DESC LIMIT $2`, partnerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.ServiceRequest{}
	for rows.Next() {
		var r model.ServiceRequest
		if err := rows.Scan(&r.ID, &r.TenantID, &r.ClientID, &r.ConversationID, &r.State,
			&r.Version, &r.IdempotencyKey, &r.SourceKind, &r.SourcePartnerID,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

// SetSource reattributes a booking's demand channel (Phase 64). RLS
// scopes the row to the tenant; empty partner clears back to direct.
func (s pgBookings) SetSource(ctx context.Context, tenantID, id, kind, partnerID string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE bookings SET source_kind=$2, source_partner_id=nullif($3,'')::uuid WHERE id=$1`,
		id, kind, partnerID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}
