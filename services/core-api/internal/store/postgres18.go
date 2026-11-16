//go:build pg

package store

// Phase 46: data retention & minimization (GDPR Art. 5(1)(e)) — pg
// purge/scrub primitives. All RLS-scoped via tenantTx; the explicit
// tenant_id predicates stay anyway so the intent is obvious at the SQL
// level and the query plan can use the tenant indexes.

import (
	"context"
	"time"
)

// PurgeGeoStampsBefore permanently removes the tenant's geo stamps recorded
// before `before`. Location history is the most sensitive data we hold.
func (s pgDispatch) PurgeGeoStampsBefore(ctx context.Context, tenantID string, before time.Time) (int, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`DELETE FROM geostamps WHERE tenant_id = $1 AND at < $2`, tenantID, before)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

// ScrubMessageBodiesBefore overwrites message bodies with `marker` in the
// tenant's CLOSED conversations for messages sent before `before`. Message
// rows survive (threading/counts are metadata); only content goes. The
// body <> marker predicate keeps it idempotent.
func (s pgConversations) ScrubMessageBodiesBefore(ctx context.Context, tenantID string, before time.Time, marker string) (int, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE messages SET body = $3
		 FROM conversations
		 WHERE messages.conversation_id = conversations.id
		   AND messages.tenant_id = $1
		   AND conversations.tenant_id = $1
		   AND conversations.state = 'CLOSED'
		   AND messages.sent_at < $2
		   AND messages.body <> '' AND messages.body <> $3`,
		tenantID, before, marker)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

// ScrubMessageBodiesForClient overwrites every message body in the client's
// conversations regardless of conversation state — the GDPR Art. 17 erasure
// companion invoked from EraseWithEvidence.
func (s pgConversations) ScrubMessageBodiesForClient(ctx context.Context, tenantID, clientID, marker string) (int, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE messages SET body = $3
		 FROM conversations
		 WHERE messages.conversation_id = conversations.id
		   AND messages.tenant_id = $1
		   AND conversations.tenant_id = $1
		   AND conversations.client_id = $2
		   AND messages.body <> '' AND messages.body <> $3`,
		tenantID, clientID, marker)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

// PurgeBefore permanently deletes the tenant's attribution events recorded
// before `before`.
func (s pgAttribution) PurgeBefore(ctx context.Context, tenantID string, before time.Time) (int, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`DELETE FROM attribution_events WHERE tenant_id = $1 AND at < $2`, tenantID, before)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}
