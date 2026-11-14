//go:build pg

// Postgres Phase 15 store: client search, duplicate merge, client-scoped
// listings (requests/bookings/cases/conversations), webhook delivery lookup.
package store

import (
	"context"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// ------------------------------------------------------------- clients

func (s pgClients) Search(ctx context.Context, tenantID, query string, limit int) ([]model.Client, error) {
	if limit <= 0 {
		limit = 25
	}
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+clientCols+` FROM clients
		 WHERE full_name ILIKE '%'||$1||'%' OR email ILIKE '%'||$1||'%' OR phone LIKE '%'||$1||'%'
		 ORDER BY created_at DESC LIMIT $2`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Client
	for rows.Next() {
		c, err := s.scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// Merge re-points every client-scoped record from loser to winner in one
// transaction, then deletes the loser. Bookings follow via their request.
// The loser's idempotency-bearing rows keep their keys — they belong to the
// same human now.
func (s pgClients) Merge(ctx context.Context, tenantID, winnerID, loserID string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Winner must exist in this tenant.
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM clients WHERE tenant_id = $1 AND id = $2`, tenantID, winnerID).Scan(&n); err != nil || n == 0 {
		return httpx.ErrNotFound
	}
	repoint := []string{
		`UPDATE service_requests SET client_id = $2 WHERE tenant_id = $1 AND client_id = $3`,
		`UPDATE cases SET client_id = $2 WHERE tenant_id = $1 AND client_id = $3`,
		`UPDATE conversations SET client_id = $2 WHERE tenant_id = $1 AND client_id = $3`,
		`UPDATE preferences SET client_id = $2 WHERE tenant_id = $1 AND client_id = $3`,
		`UPDATE attribution_events SET client_id = $2 WHERE tenant_id = $1 AND client_id = $3`,
		`UPDATE outreach_sends SET client_id = $2 WHERE tenant_id = $1 AND client_id = $3`,
	}
	for _, q := range repoint {
		if _, err := tx.ExecContext(ctx, q, tenantID, winnerID, loserID); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM clients WHERE tenant_id = $1 AND id = $2`, tenantID, loserID)
	if err != nil {
		return err
	}
	if gone, _ := res.RowsAffected(); gone == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

// ------------------------------------------------------------- client-scoped listings

func (s pgRequests) ForClient(ctx context.Context, tenantID, clientID string, limit int) ([]model.ServiceRequest, error) {
	if limit <= 0 {
		limit = 25
	}
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, client_id, coalesce(conversation_id::text,''), state, version,
		        idempotency_key, created_at, updated_at
		 FROM service_requests WHERE client_id = $1
		 ORDER BY created_at DESC LIMIT $2`, clientID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ServiceRequest
	for rows.Next() {
		var r model.ServiceRequest
		if err := rows.Scan(&r.ID, &r.TenantID, &r.ClientID, &r.ConversationID, &r.State,
			&r.Version, &r.IdempotencyKey, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s pgBookings) ForClient(ctx context.Context, tenantID, clientID string, limit int) ([]model.Booking, error) {
	if limit <= 0 {
		limit = 25
	}
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// Bookings carry no client_id — resolve through their request. Both
	// tables are RLS-scoped by the tenant GUC inside tenantTx.
	rows, err := tx.QueryContext(ctx,
		`SELECT b.id, b.tenant_id, b.request_id, coalesce(b.quote_id::text,''), b.state, b.created_at
		 FROM bookings b JOIN service_requests r ON r.id = b.request_id
		 WHERE r.client_id = $1
		 ORDER BY b.created_at DESC LIMIT $2`, clientID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Booking
	for rows.Next() {
		var b model.Booking
		if err := rows.Scan(&b.ID, &b.TenantID, &b.RequestID, &b.QuoteID, &b.State, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s pgCases) ForClient(ctx context.Context, tenantID, clientID string, limit int) ([]model.Case, error) {
	if limit <= 0 {
		limit = 25
	}
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+caseCols+` FROM cases WHERE client_id = $1
		 ORDER BY created_at DESC LIMIT $2`, clientID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Case
	for rows.Next() {
		c, err := scanCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s pgConversations) ForClient(ctx context.Context, tenantID, clientID string, limit int) ([]model.Conversation, error) {
	if limit <= 0 {
		limit = 25
	}
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, coalesce(client_id::text,''), channel, state, coalesce(external_ref,''), created_at
		 FROM conversations WHERE client_id = $1
		 ORDER BY created_at DESC LIMIT $2`, clientID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Conversation
	for rows.Next() {
		var c model.Conversation
		if err := rows.Scan(&c.ID, &c.TenantID, &c.ClientID, &c.Channel, &c.State,
			&c.ExternalRef, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ------------------------------------------------------------- webhook delivery lookup

func (s pgWebhooks) GetDelivery(ctx context.Context, tenantID, id string) (*model.WebhookDelivery, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	return scanDelivery(tx.QueryRowContext(ctx,
		`SELECT `+deliveryCols+` FROM webhook_deliveries WHERE id = $1`, id))
}
