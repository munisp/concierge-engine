//go:build pg

// Postgres Phase 14 store: outreach sends (idempotent, RLS-isolated),
// attribution list/funnel, client referral-code lookup.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type pgOutreach struct{ p *postgres }

func (s pgOutreach) AddSend(ctx context.Context, o *model.OutreachSend) error {
	params, _ := json.Marshal(o.Params)
	if o.Params == nil {
		params = []byte("[]")
	}
	tx, err := s.p.tenantTx(ctx, o.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// ON CONFLICT DO NOTHING + RETURNING: no row back means the idempotency
	// key was already used -> replay, surface ErrConflict (caller reloads).
	var id string
	err = tx.QueryRowContext(ctx,
		`INSERT INTO outreach_sends
		 (id, tenant_id, client_id, channel, purpose, body, template, params,
		  external_ref, status, detail, idempotency_key, created_at, scheduled_for)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		 ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
		 RETURNING id`,
		o.ID, o.TenantID, o.ClientID, o.Channel, o.Purpose, o.Body, o.Template,
		params, o.ExternalRef, o.Status, o.Detail, o.IdempotencyKey, o.CreatedAt,
		o.ScheduledFor).Scan(&id)
	if err == sql.ErrNoRows {
		return httpx.ErrConflict
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

const outreachCols = `id, tenant_id, client_id, channel, purpose, body, template,
	coalesce(to_jsonb(params), '[]'::jsonb), external_ref, status, detail,
	idempotency_key, created_at, scheduled_for`

func (s pgOutreach) scan(rows *sql.Rows) (*model.OutreachSend, error) {
	var o model.OutreachSend
	var params []byte
	var scheduledFor sql.NullTime
	if err := rows.Scan(&o.ID, &o.TenantID, &o.ClientID, &o.Channel, &o.Purpose,
		&o.Body, &o.Template, &params, &o.ExternalRef, &o.Status, &o.Detail,
		&o.IdempotencyKey, &o.CreatedAt, &scheduledFor); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(params, &o.Params)
	if scheduledFor.Valid {
		t := scheduledFor.Time
		o.ScheduledFor = &t
	}
	return &o, nil
}

// UpdateSendResult (Phase 35): the due-send sweep flips a "scheduled" row
// to its terminal delivery outcome. The WHERE status='scheduled' guard
// makes a double-fire between two sweepers a harmless no-op.
func (s pgOutreach) UpdateSendResult(ctx context.Context, tenantID, sendID, status, detail, externalRef string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE outreach_sends SET status=$3, detail=$4, external_ref=$5
		 WHERE tenant_id=$1 AND id=$2 AND status='scheduled'`,
		tenantID, sendID, status, detail, externalRef)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s pgOutreach) SendsForClient(ctx context.Context, tenantID, clientID string, since time.Time) ([]model.OutreachSend, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+outreachCols+` FROM outreach_sends
		 WHERE tenant_id = $1 AND client_id = $2 AND created_at >= $3
		 ORDER BY created_at ASC`, tenantID, clientID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.OutreachSend
	for rows.Next() {
		o, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

func (s pgOutreach) ListSends(ctx context.Context, tenantID string, limit int) ([]model.OutreachSend, error) {
	if limit <= 0 {
		limit = 100
	}
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+outreachCols+` FROM outreach_sends
		 WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.OutreachSend
	for rows.Next() {
		o, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// ------------------------------------------------------------- attribution

func (s pgAttribution) List(ctx context.Context, tenantID, clientID string, limit int) ([]model.AttributionEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT kind, coalesce(channel,''), coalesce(campaign,''),
		        coalesce(client_id::text,''), coalesce(request_id::text,''),
		        coalesce(value_minor,0)
		 FROM attribution_events
		 WHERE tenant_id = $1 AND client_id = $2
		 ORDER BY id ASC LIMIT $3`, tenantID, clientID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AttributionEvent
	for rows.Next() {
		var e model.AttributionEvent
		e.TenantID = tenantID
		if err := rows.Scan(&e.Kind, &e.Channel, &e.Campaign, &e.ClientID,
			&e.RequestID, &e.ValueMinor); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s pgAttribution) Funnel(ctx context.Context, tenantID string) ([]FunnelRow, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT kind, coalesce(channel,''), coalesce(campaign,''),
		        count(*), coalesce(sum(value_minor),0)
		 FROM attribution_events WHERE tenant_id = $1
		 GROUP BY kind, channel, campaign
		 ORDER BY kind, channel, campaign`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FunnelRow
	for rows.Next() {
		var r FunnelRow
		if err := rows.Scan(&r.Kind, &r.Channel, &r.Campaign, &r.Count, &r.ValueMinor); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ------------------------------------------------------------- client referral code

func (s pgClients) FindByReferralCode(ctx context.Context, tenantID, code string) (*model.Client, error) {
	prefix, ok := model.ReferralCodePrefix(code)
	if !ok {
		return nil, httpx.ErrNotFound
	}
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	row := tx.QueryRowContext(ctx,
		`SELECT `+clientCols+` FROM clients
		 WHERE tenant_id = $1
		   AND upper(replace(id::text,'-','')) LIKE $2 || '%'`, tenantID, prefix)
	return s.scanClient(row)
}
