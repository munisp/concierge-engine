//go:build pg

// Postgres Phase 3 stores. Tenant-scoped operations run inside tenantTx
// (app.tenant_id GUC + FORCE RLS policies from db/migrations/0003_phase3.sql).
//
// Two deliberate exceptions, mirroring how hosted Postgres platforms do auth:
//   - ApiKeys.GetByPrefix runs without tenantTx (the prefix lookup IS the
//     tenant resolution); the service role holds BYPASSRLS for this path.
//   - Webhooks.DueDeliveries scans all tenants for the dispatcher; rows are
//     only ever mutated back through their own tenant context.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (p *postgres) ApiKeys() ApiKeyStore     { return pgApiKeys{p} }
func (p *postgres) Webhooks() WebhookStore   { return pgWebhooks{p} }
func (p *postgres) Referrals() ReferralStore { return pgReferrals{p} }

// ------------------------------------------------------------- API keys
type pgApiKeys struct{ p *postgres }

func (s pgApiKeys) Create(ctx context.Context, k *model.ApiKey) error {
	scopes, _ := json.Marshal(k.Scopes)
	tx, err := s.p.tenantTx(ctx, k.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO api_keys (id, tenant_id, name, prefix, secret_hash, scopes, revoked, partner_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,nullif($8,'')::uuid)`,
		k.ID, k.TenantID, k.Name, k.Prefix, k.SecretHash, scopes, k.Revoked, k.PartnerID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func scanApiKey(row interface{ Scan(...any) error }) (*model.ApiKey, error) {
	var k model.ApiKey
	var scopes []byte
	var partnerID sql.NullString
	var lastUsed sql.NullTime
	err := row.Scan(&k.ID, &k.TenantID, &k.Name, &k.Prefix, &k.SecretHash,
		&scopes, &k.Revoked, &k.CreatedAt, &lastUsed, &partnerID)
	if err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal(scopes, &k.Scopes)
	k.PartnerID = partnerID.String
	k.LastUsedAt = lastUsed.Time
	return &k, nil
}

const apiKeyCols = `id, tenant_id, name, prefix, secret_hash, scopes, revoked, created_at, last_used_at, partner_id`

func (s pgApiKeys) GetByPrefix(ctx context.Context, prefix string) (*model.ApiKey, error) {
	// No tenantTx: the prefix lookup resolves the tenant (see file header).
	return scanApiKey(s.p.db.QueryRowContext(ctx,
		`SELECT `+apiKeyCols+` FROM api_keys WHERE prefix=$1`, prefix))
}

func (s pgApiKeys) List(ctx context.Context, tenantID string) ([]model.ApiKey, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+apiKeyCols+` FROM api_keys ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ApiKey
	for rows.Next() {
		k, err := scanApiKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, tx.Commit()
}

func (s pgApiKeys) Revoke(ctx context.Context, tenantID, id string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE api_keys SET revoked=true WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

func (s pgApiKeys) TouchUsed(ctx context.Context, id string, at time.Time) error {
	// Best-effort usage stamp; not tenant-scoped (the id is already resolved).
	_, err := s.p.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at=$2 WHERE id=$1`, id, at)
	return err
}

// ------------------------------------------------------------- webhooks
type pgWebhooks struct{ p *postgres }

func (s pgWebhooks) CreateSubscription(ctx context.Context, sub *model.WebhookSubscription) error {
	events, _ := json.Marshal(sub.Events)
	tx, err := s.p.tenantTx(ctx, sub.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO webhook_subscriptions (id, tenant_id, url, events, secret, active, prev_secret, secret_rotated_at, partner_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,nullif($9,'')::uuid)`,
		sub.ID, sub.TenantID, sub.URL, events, sub.Secret, sub.Active,
		sub.PrevSecret, nilIfZero(sub.SecretRotatedAt), sub.PartnerID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func scanSubscription(row interface{ Scan(...any) error }) (*model.WebhookSubscription, error) {
	var sub model.WebhookSubscription
	var events []byte
	var rotatedAt sql.NullTime
	var partnerID sql.NullString
	err := row.Scan(&sub.ID, &sub.TenantID, &sub.URL, &events, &sub.Secret, &sub.Active,
		&sub.CreatedAt, &sub.PrevSecret, &rotatedAt, &partnerID)
	if err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal(events, &sub.Events)
	sub.SecretRotatedAt = rotatedAt.Time
	sub.PartnerID = partnerID.String
	return &sub, nil
}

const webhookSubCols = `id, tenant_id, url, events, secret, active, created_at, prev_secret, secret_rotated_at, partner_id`

func (s pgWebhooks) GetSubscription(ctx context.Context, tenantID, id string) (*model.WebhookSubscription, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	sub, err := scanSubscription(tx.QueryRowContext(ctx,
		`SELECT `+webhookSubCols+` FROM webhook_subscriptions WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	return sub, tx.Commit()
}

func (s pgWebhooks) ListSubscriptions(ctx context.Context, tenantID string) ([]model.WebhookSubscription, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+webhookSubCols+` FROM webhook_subscriptions ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.WebhookSubscription
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sub)
	}
	return out, tx.Commit()
}

func (s pgWebhooks) UpdateSubscription(ctx context.Context, sub *model.WebhookSubscription) error {
	events, _ := json.Marshal(sub.Events)
	tx, err := s.p.tenantTx(ctx, sub.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE webhook_subscriptions SET url=$2, events=$3, active=$4, secret=$5, prev_secret=$6, secret_rotated_at=$7, partner_id=nullif($8,'')::uuid WHERE id=$1`,
		sub.ID, sub.URL, events, sub.Active, sub.Secret, sub.PrevSecret, nilIfZero(sub.SecretRotatedAt), sub.PartnerID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

func (s pgWebhooks) EnqueueDelivery(ctx context.Context, d *model.WebhookDelivery) error {
	tx, err := s.p.tenantTx(ctx, d.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO webhook_deliveries
		 (id, tenant_id, subscription_id, event_id, event_kind, payload, attempts, max_attempts, next_attempt_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		d.ID, d.TenantID, d.SubscriptionID, d.EventID, d.EventKind, d.Payload,
		d.Attempts, d.MaxAttempts, d.NextAttemptAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func scanDelivery(row interface{ Scan(...any) error }) (*model.WebhookDelivery, error) {
	var d model.WebhookDelivery
	// delivered_at and last_status are nullable until the first delivery
	// succeeds — scan them nullable or any pending delivery is unreadable.
	var deliveredAt sql.NullTime
	var lastStatus sql.NullInt64
	err := row.Scan(&d.ID, &d.TenantID, &d.SubscriptionID, &d.EventID, &d.EventKind,
		&d.Payload, &d.Attempts, &d.MaxAttempts, &d.NextAttemptAt, &deliveredAt,
		&lastStatus, &d.LastError, &d.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	d.DeliveredAt = deliveredAt.Time
	if lastStatus.Valid {
		d.LastStatus = int(lastStatus.Int64)
	}
	return &d, nil
}

const deliveryCols = `id, tenant_id, subscription_id, event_id, event_kind, payload, attempts, max_attempts, next_attempt_at, delivered_at, last_status, last_error, created_at`

func (s pgWebhooks) DueDeliveries(ctx context.Context, now time.Time, limit int) ([]model.WebhookDelivery, error) {
	// No tenantTx: the dispatcher scans across tenants (see file header).
	rows, err := s.p.db.QueryContext(ctx,
		`SELECT `+deliveryCols+` FROM webhook_deliveries
		 WHERE delivered_at IS NULL AND attempts < max_attempts AND next_attempt_at <= $1
		 ORDER BY next_attempt_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.WebhookDelivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, nil
}

func (s pgWebhooks) UpdateDelivery(ctx context.Context, d *model.WebhookDelivery) error {
	tx, err := s.p.tenantTx(ctx, d.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE webhook_deliveries SET attempts=$2, next_attempt_at=$3, delivered_at=$4,
		 last_status=$5, last_error=$6 WHERE id=$1`,
		d.ID, d.Attempts, d.NextAttemptAt, nilIfZero(d.DeliveredAt), nullZeroInt(d.LastStatus), d.LastError)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

func (s pgWebhooks) ListDeliveries(ctx context.Context, tenantID string, limit int) ([]model.WebhookDelivery, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+deliveryCols+` FROM webhook_deliveries ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.WebhookDelivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, tx.Commit()
}

// ------------------------------------------------------------- referrals
type pgReferrals struct{ p *postgres }

const referralCols = `id, referring_tenant_id, receiving_tenant_id, client_name, client_contact, request_summary, est_value_minor, currency, fee_pct, state, request_id, booking_id, commission_minor, idempotency_key, created_at, updated_at`

func (s pgReferrals) Create(ctx context.Context, r *model.Referral) error {
	// Idempotency is scoped to the referring tenant; write in their context.
	tx, err := s.p.tenantTx(ctx, r.ReferringTenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if r.IdempotencyKey != "" {
		var existingID string
		err := tx.QueryRowContext(ctx,
			`SELECT id FROM referrals WHERE referring_tenant_id=$1 AND idempotency_key=$2`,
			r.ReferringTenantID, r.IdempotencyKey).Scan(&existingID)
		if err == nil {
			// Replay: load the original into r.
			row := tx.QueryRowContext(ctx, `SELECT `+referralCols+` FROM referrals WHERE id=$1`, existingID)
			r2, err := scanReferral(row)
			if err != nil {
				return err
			}
			*r = *r2
			return tx.Commit()
		}
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO referrals (`+referralCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		r.ID, r.ReferringTenantID, r.ReceivingTenantID, r.ClientName, r.ClientContact,
		r.RequestSummary, r.EstValueMinor, r.Currency, r.FeePct, r.State,
		nilIfEmpty(r.RequestID), nilIfEmpty(r.BookingID), r.CommissionMinor, r.IdempotencyKey, r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func scanReferral(row interface{ Scan(...any) error }) (*model.Referral, error) {
	var r model.Referral
	var requestID, bookingID sql.NullString // nullable until the receiving tenant books
	err := row.Scan(&r.ID, &r.ReferringTenantID, &r.ReceivingTenantID, &r.ClientName,
		&r.ClientContact, &r.RequestSummary, &r.EstValueMinor, &r.Currency, &r.FeePct,
		&r.State, &requestID, &bookingID, &r.CommissionMinor, &r.IdempotencyKey,
		&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	r.RequestID = requestID.String
	r.BookingID = bookingID.String
	return &r, nil
}

func (s pgReferrals) Get(ctx context.Context, tenantID, id string) (*model.Referral, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	r, err := scanReferral(tx.QueryRowContext(ctx,
		`SELECT `+referralCols+` FROM referrals WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	// RLS policy already restricts to both sides; belt-and-braces check here.
	if r.ReferringTenantID != tenantID && r.ReceivingTenantID != tenantID {
		return nil, httpx.ErrNotFound
	}
	return r, tx.Commit()
}

func (s pgReferrals) List(ctx context.Context, tenantID string) ([]model.Referral, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+referralCols+` FROM referrals ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Referral
	for rows.Next() {
		r, err := scanReferral(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, tx.Commit()
}

func (s pgReferrals) Update(ctx context.Context, r *model.Referral) error {
	// Updates run in the context of whichever side is acting; the RLS policy
	// admits both referring and receiving tenant.
	tx, err := s.p.tenantTx(ctx, r.ReceivingTenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE referrals SET state=$2, request_id=$3, booking_id=$4, commission_minor=$5, updated_at=$6
		 WHERE id=$1`,
		r.ID, r.State, nilIfEmpty(r.RequestID), nilIfEmpty(r.BookingID), r.CommissionMinor, r.UpdatedAt)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

// nullZeroInt maps 0 to NULL for columns where 0 is not a real value
// (e.g. last_status before the first delivery attempt).
func nullZeroInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}
