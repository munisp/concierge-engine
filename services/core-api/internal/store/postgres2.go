//go:build pg

// Postgres Phase 2 stores. Same RLS discipline as postgres.go: every query
// runs inside tenantTx (app.tenant_id GUC + FORCE RLS policies from
// db/migrations/0002_phase2.sql).
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// nullZeroFloat maps 0 to NULL: the domain convention is that 0,0 means
// "no coordinates recorded" (the Gulf of Guinea is not a service area).
func nullZeroFloat(f float64) any {
	if f == 0 {
		return nil
	}
	return f
}

func (p *postgres) Partners() PartnerStore    { return pgPartners{p} }
func (p *postgres) Corporate() CorporateStore { return pgCorporate{p} }
func (p *postgres) Payouts() PayoutStore      { return pgPayouts{p} }
func (p *postgres) Audit() AuditStore         { return pgAudit{p} }

// ------------------------------------------------------------- partners
type pgPartners struct{ p *postgres }

func (s pgPartners) Create(ctx context.Context, pt *model.Partner) error {
	verticals, _ := json.Marshal(pt.Verticals)
	markets, _ := json.Marshal(pt.Markets)
	tx, err := s.p.tenantTx(ctx, pt.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO partners (id, tenant_id, name, verticals, markets, status, quality_score, payout_rail, payout_ref, lat, lng)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		pt.ID, pt.TenantID, pt.Name, verticals, markets, pt.Status, pt.QualityScore, pt.PayoutRail, pt.PayoutRef,
		nullZeroFloat(pt.Lat), nullZeroFloat(pt.Lng))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func scanPartner(row interface{ Scan(...any) error }) (*model.Partner, error) {
	var pt model.Partner
	var verticals, markets []byte
	var lat, lng sql.NullFloat64 // base-of-operations (Phase 6) is nullable
	err := row.Scan(&pt.ID, &pt.TenantID, &pt.Name, &verticals, &markets,
		&pt.Status, &pt.QualityScore, &pt.PayoutRail, &pt.PayoutRef, &lat, &lng, &pt.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	pt.Lat = lat.Float64
	pt.Lng = lng.Float64
	_ = json.Unmarshal(verticals, &pt.Verticals)
	_ = json.Unmarshal(markets, &pt.Markets)
	return &pt, nil
}

const partnerCols = `id, tenant_id, name, verticals, markets, status, quality_score, payout_rail, payout_ref, lat, lng, created_at`

func (s pgPartners) Get(ctx context.Context, tenantID, id string) (*model.Partner, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	pt, err := scanPartner(tx.QueryRowContext(ctx,
		`SELECT `+partnerCols+` FROM partners WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	return pt, tx.Commit()
}

func (s pgPartners) List(ctx context.Context, tenantID string) ([]model.Partner, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT `+partnerCols+` FROM partners ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Partner
	for rows.Next() {
		pt, err := scanPartner(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *pt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (s pgPartners) Update(ctx context.Context, pt *model.Partner) error {
	tx, err := s.p.tenantTx(ctx, pt.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE partners SET name=$2, status=$3, quality_score=$4, payout_rail=$5, payout_ref=$6, lat=$7, lng=$8 WHERE id=$1`,
		pt.ID, pt.Name, pt.Status, pt.QualityScore, pt.PayoutRail, pt.PayoutRef,
		nullZeroFloat(pt.Lat), nullZeroFloat(pt.Lng))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

// ------------------------------------------------------------- corporate
type pgCorporate struct{ p *postgres }

func (s pgCorporate) CreateAccount(ctx context.Context, a *model.CorporateAccount) error {
	tx, err := s.p.tenantTx(ctx, a.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO corporate_accounts (id, tenant_id, name, billing_email, payment_terms_days, credit_limit_minor, currency)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		a.ID, a.TenantID, a.Name, a.BillingEmail, a.PaymentTermsDays, a.CreditLimitMinor, a.Currency)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func scanAccount(row interface{ Scan(...any) error }) (*model.CorporateAccount, error) {
	var a model.CorporateAccount
	err := row.Scan(&a.ID, &a.TenantID, &a.Name, &a.BillingEmail,
		&a.PaymentTermsDays, &a.CreditLimitMinor, &a.Currency, &a.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &a, nil
}

const accountCols = `id, tenant_id, name, billing_email, payment_terms_days, credit_limit_minor, currency, created_at`

func (s pgCorporate) GetAccount(ctx context.Context, tenantID, id string) (*model.CorporateAccount, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := scanAccount(tx.QueryRowContext(ctx,
		`SELECT `+accountCols+` FROM corporate_accounts WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	return a, tx.Commit()
}

func (s pgCorporate) ListAccounts(ctx context.Context, tenantID string) ([]model.CorporateAccount, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT `+accountCols+` FROM corporate_accounts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CorporateAccount
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (s pgCorporate) CreateInvoice(ctx context.Context, inv *model.CorporateInvoice) error {
	tx, err := s.p.tenantTx(ctx, inv.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Idempotent on (tenant, idempotency_key): replay returns the original.
	var prev model.CorporateInvoice
	err = tx.QueryRowContext(ctx,
		`SELECT `+invoiceCols+` FROM corporate_invoices WHERE idempotency_key=$1`, inv.IdempotencyKey).
		Scan(&prev.ID, &prev.TenantID, &prev.AccountID, &prev.PeriodStart, &prev.PeriodEnd,
			&prev.TotalMinor, &prev.Currency, &prev.State, &prev.CreatedAt)
	if err == nil {
		*inv = prev
		return tx.Commit()
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO corporate_invoices (id, tenant_id, account_id, period_start, period_end, total_minor, currency, state, idempotency_key)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		inv.ID, inv.TenantID, inv.AccountID, inv.PeriodStart, inv.PeriodEnd,
		inv.TotalMinor, inv.Currency, inv.State, inv.IdempotencyKey)
	if err != nil {
		return err
	}
	return tx.Commit()
}

const invoiceCols = `id, tenant_id, account_id, period_start, period_end, total_minor, currency, state, created_at`

func scanInvoice(row interface{ Scan(...any) error }) (*model.CorporateInvoice, error) {
	var inv model.CorporateInvoice
	err := row.Scan(&inv.ID, &inv.TenantID, &inv.AccountID, &inv.PeriodStart, &inv.PeriodEnd,
		&inv.TotalMinor, &inv.Currency, &inv.State, &inv.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &inv, nil
}

func (s pgCorporate) GetInvoice(ctx context.Context, tenantID, id string) (*model.CorporateInvoice, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	inv, err := scanInvoice(tx.QueryRowContext(ctx,
		`SELECT `+invoiceCols+` FROM corporate_invoices WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	return inv, tx.Commit()
}

func (s pgCorporate) UpdateInvoiceState(ctx context.Context, tenantID, id, state string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE corporate_invoices SET state=$2 WHERE id=$1`, id, state)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

func (s pgCorporate) ListInvoices(ctx context.Context, tenantID, accountID string) ([]model.CorporateInvoice, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+invoiceCols+` FROM corporate_invoices WHERE ($1='' OR account_id=$1::uuid) ORDER BY created_at`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.CorporateInvoice
	for rows.Next() {
		inv, err := scanInvoice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *inv)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

// ------------------------------------------------------------- payouts
type pgPayouts struct{ p *postgres }

const payoutCols = `id, tenant_id, partner_id, booking_id, amount_minor, currency, rail, state,
	quote_id, transfer_id, fx_rate, receive_minor, fees_minor, idempotency_key, created_at, updated_at,
	quote_expires_at, batch_id, settled_at`

func scanPayout(row interface{ Scan(...any) error }) (*model.Payout, error) {
	var p model.Payout
	var quoteExpiresAt, settledAt *time.Time
	var batchID *string
	err := row.Scan(&p.ID, &p.TenantID, &p.PartnerID, &p.BookingID, &p.AmountMinor, &p.Currency,
		&p.Rail, &p.State, &p.QuoteID, &p.TransferID, &p.FXRate, &p.ReceiveMinor, &p.FeesMinor,
		&p.IdempotencyKey, &p.CreatedAt, &p.UpdatedAt, &quoteExpiresAt, &batchID, &settledAt)
	if err != nil {
		return nil, notFound(err)
	}
	if quoteExpiresAt != nil {
		p.QuoteExpiresAt = *quoteExpiresAt
	}
	if batchID != nil {
		p.BatchID = *batchID
	}
	if settledAt != nil {
		p.SettledAt = *settledAt
	}
	return &p, nil
}

func (s pgPayouts) Create(ctx context.Context, p *model.Payout) error {
	tx, err := s.p.tenantTx(ctx, p.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var prev model.Payout
	prevPtr, err := scanPayout(tx.QueryRowContext(ctx,
		`SELECT `+payoutCols+` FROM payouts WHERE idempotency_key=$1`, p.IdempotencyKey))
	if err == nil && prevPtr != nil {
		prev = *prevPtr
		*p = prev // idempotent replay
		return tx.Commit()
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO payouts (id, tenant_id, partner_id, booking_id, amount_minor, currency, rail, state,
			quote_id, transfer_id, fx_rate, receive_minor, fees_minor, idempotency_key, created_at, updated_at,
			quote_expires_at, batch_id, settled_at)
		 VALUES ($1,$2,$3,nullif($4,'')::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,nullif($18,'')::uuid,$19)`,
		p.ID, p.TenantID, p.PartnerID, p.BookingID, p.AmountMinor, p.Currency, p.Rail, p.State,
		p.QuoteID, p.TransferID, p.FXRate, p.ReceiveMinor, p.FeesMinor, p.IdempotencyKey,
		p.CreatedAt, p.UpdatedAt, nilIfZero(p.QuoteExpiresAt), p.BatchID, nilIfZero(p.SettledAt))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgPayouts) Get(ctx context.Context, tenantID, id string) (*model.Payout, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	p, err := scanPayout(tx.QueryRowContext(ctx, `SELECT `+payoutCols+` FROM payouts WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	return p, tx.Commit()
}

func (s pgPayouts) GetByTransfer(ctx context.Context, tenantID, transferID string) (*model.Payout, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	p, err := scanPayout(tx.QueryRowContext(ctx,
		`SELECT `+payoutCols+` FROM payouts WHERE transfer_id=$1`, transferID))
	if err != nil {
		return nil, err
	}
	return p, tx.Commit()
}

func (s pgPayouts) Update(ctx context.Context, p *model.Payout) error {
	tx, err := s.p.tenantTx(ctx, p.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE payouts SET state=$2, quote_id=$3, transfer_id=$4, fx_rate=$5, receive_minor=$6,
			fees_minor=$7, updated_at=$8, quote_expires_at=$9, batch_id=nullif($10,'')::uuid, settled_at=$11
		 WHERE id=$1`,
		p.ID, p.State, p.QuoteID, p.TransferID, p.FXRate, p.ReceiveMinor, p.FeesMinor, p.UpdatedAt,
		nilIfZero(p.QuoteExpiresAt), p.BatchID, nilIfZero(p.SettledAt))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

func (s pgPayouts) List(ctx context.Context, tenantID string) ([]model.Payout, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT `+payoutCols+` FROM payouts ORDER BY created_at`)
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

// List on pgBookings lives here with the other Phase 2 store extensions.
func (s pgBookings) List(ctx context.Context, tenantID string) ([]model.Booking, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, request_id, quote_id, state, created_at FROM bookings ORDER BY created_at`)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

// ------------------------------------------------------------- audit
type pgAudit struct{ p *postgres }

func (s pgAudit) Log(ctx context.Context, e *model.AuditEntry) error {
	detail, _ := json.Marshal(e.Detail)
	tx, err := s.p.tenantTx(ctx, e.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	err = tx.QueryRowContext(ctx,
		`INSERT INTO audit_log (tenant_id, actor, action, resource, resource_id, detail)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, at`,
		e.TenantID, e.Actor, e.Action, e.Resource, e.ResourceID, detail).Scan(&e.ID, &e.At)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgAudit) List(ctx context.Context, tenantID string, limit int) ([]model.AuditEntry, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, actor, action, resource, resource_id, detail, at
		 FROM audit_log ORDER BY at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AuditEntry
	for rows.Next() {
		var e model.AuditEntry
		var detail []byte
		if err := rows.Scan(&e.ID, &e.TenantID, &e.Actor, &e.Action, &e.Resource,
			&e.ResourceID, &detail, &e.At); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(detail, &e.Detail)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}
