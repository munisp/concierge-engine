//go:build pg

package store

// Phase 42: pg send-by-external-ref — receipt correlation for the WhatsApp
// hygiene rules. Backed by idx_outreach_sends_ref (migration 0030).

import (
	"context"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (s pgOutreach) SendByExternalRef(ctx context.Context, tenantID, externalRef string) (*model.OutreachSend, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+outreachCols+` FROM outreach_sends
		 WHERE tenant_id = $1 AND external_ref = $2
		 ORDER BY created_at DESC LIMIT 1`, tenantID, externalRef)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, httpx.ErrNotFound
	}
	o, err := s.scan(rows)
	if err != nil {
		return nil, err
	}
	return o, tx.Commit()
}
