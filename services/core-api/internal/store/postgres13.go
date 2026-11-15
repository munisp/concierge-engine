//go:build pg

package store

// Phase 36: pg leads — RLS-scoped via tenantTx (FORCE RLS from
// db/migrations/0025_phase36.sql). Idempotency rides the partial UNIQUE
// (tenant_id, idempotency_key) WHERE key <> '': ON CONFLICT cannot name a
// partial index by columns, so we catch the unique-violation instead.

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (p *postgres) Leads() LeadStore { return pgLeads{p} }

type pgLeads struct{ p *postgres }

const leadCols = `id, tenant_id, kind, name, phone, email, company,
	event_type, event_date, headcount, venue, verticals, notes,
	source, channel, campaign, referral_code, list_source, contact_basis,
	status, score, score_reasons, lost_reason, consent_marketing,
	client_id, converted_client_id, converted_partner_id, converted_account_id,
	sla_alerted_at, idempotency_key, created_at, updated_at`

func (s pgLeads) Create(ctx context.Context, l *model.Lead) error {
	verticals, _ := json.Marshal(l.Verticals)
	if l.Verticals == nil {
		verticals = []byte("[]")
	}
	reasons, _ := json.Marshal(l.ScoreReasons)
	if l.ScoreReasons == nil {
		reasons = []byte("[]")
	}
	tx, err := s.p.tenantTx(ctx, l.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// ON CONFLICT with the partial-index predicate: keyed replays no-op
	// (ErrConflict via missing RETURNING row); '' keys never match the
	// index and always insert.
	var id string
	err = tx.QueryRowContext(ctx,
		`INSERT INTO leads (`+leadCols+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,
		         $18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32)
		 ON CONFLICT (tenant_id, idempotency_key) WHERE idempotency_key <> ''
		 DO NOTHING RETURNING id`,
		l.ID, l.TenantID, l.Kind, l.Name, l.Phone, l.Email, l.Company,
		l.EventType, l.EventDate, l.Headcount, l.Venue, verticals, l.Notes,
		l.Source, l.Channel, l.Campaign, l.ReferralCode, l.ListSource, l.ContactBasis,
		l.Status, l.Score, reasons, l.LostReason, l.ConsentMarketing,
		nilIfEmpty(l.ClientID), nilIfEmpty(l.ConvertedClientID),
		nilIfEmpty(l.ConvertedPartnerID), nilIfEmpty(l.ConvertedAccountID),
		l.SLAAlertedAt, l.IdempotencyKey, l.CreatedAt, l.UpdatedAt).Scan(&id)
	if err == sql.ErrNoRows {
		return httpx.ErrConflict
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgLeads) Get(ctx context.Context, tenantID, id string) (*model.Lead, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx,
		`SELECT `+leadCols+` FROM leads WHERE tenant_id=$1 AND id=$2`, tenantID, id)
	l, err := scanLead(row)
	if err != nil {
		return nil, notFound(err)
	}
	return l, tx.Commit()
}

func (s pgLeads) Update(ctx context.Context, l *model.Lead) error {
	verticals, _ := json.Marshal(l.Verticals)
	if l.Verticals == nil {
		verticals = []byte("[]")
	}
	reasons, _ := json.Marshal(l.ScoreReasons)
	if l.ScoreReasons == nil {
		reasons = []byte("[]")
	}
	tx, err := s.p.tenantTx(ctx, l.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE leads SET kind=$3, name=$4, phone=$5, email=$6, company=$7,
		 event_type=$8, event_date=$9, headcount=$10, venue=$11, verticals=$12,
		 notes=$13, source=$14, channel=$15, campaign=$16, referral_code=$17,
		 list_source=$18, contact_basis=$19,
		 status=$20, score=$21, score_reasons=$22, lost_reason=$23,
		 consent_marketing=$24, client_id=$25, converted_client_id=$26,
		 converted_partner_id=$27, converted_account_id=$28,
		 sla_alerted_at=$29, updated_at=$30
		 WHERE tenant_id=$1 AND id=$2`,
		l.TenantID, l.ID, l.Kind, l.Name, l.Phone, l.Email, l.Company,
		l.EventType, l.EventDate, l.Headcount, l.Venue, verticals,
		l.Notes, l.Source, l.Channel, l.Campaign, l.ReferralCode,
		l.ListSource, l.ContactBasis,
		l.Status, l.Score, reasons, l.LostReason,
		l.ConsentMarketing, nilIfEmpty(l.ClientID), nilIfEmpty(l.ConvertedClientID),
		nilIfEmpty(l.ConvertedPartnerID), nilIfEmpty(l.ConvertedAccountID),
		l.SLAAlertedAt, l.UpdatedAt)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

func (s pgLeads) List(ctx context.Context, tenantID string, limit int) ([]model.Lead, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	q := `SELECT ` + leadCols + ` FROM leads WHERE tenant_id=$1 ORDER BY created_at DESC`
	if limit > 0 {
		q += ` LIMIT ` + strconv.Itoa(limit)
	}
	rows, err := tx.QueryContext(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Lead
	for rows.Next() {
		l, err := scanLead(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

type leadRow interface {
	Scan(dest ...any) error
}

func scanLead(row leadRow) (*model.Lead, error) {
	var l model.Lead
	var verticals, reasons []byte
	var eventDate, slaAlerted sql.NullTime
	var clientID, convClient, convPartner, convAccount sql.NullString
	err := row.Scan(&l.ID, &l.TenantID, &l.Kind, &l.Name, &l.Phone, &l.Email, &l.Company,
		&l.EventType, &eventDate, &l.Headcount, &l.Venue, &verticals, &l.Notes,
		&l.Source, &l.Channel, &l.Campaign, &l.ReferralCode, &l.ListSource, &l.ContactBasis,
		&l.Status, &l.Score, &reasons, &l.LostReason, &l.ConsentMarketing,
		&clientID, &convClient, &convPartner, &convAccount,
		&slaAlerted, &l.IdempotencyKey, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if len(verticals) > 0 {
		_ = json.Unmarshal(verticals, &l.Verticals)
	}
	if len(reasons) > 0 {
		_ = json.Unmarshal(reasons, &l.ScoreReasons)
	}
	if eventDate.Valid {
		t := eventDate.Time
		l.EventDate = &t
	}
	if slaAlerted.Valid {
		t := slaAlerted.Time
		l.SLAAlertedAt = &t
	}
	l.ClientID = clientID.String
	l.ConvertedClientID = convClient.String
	l.ConvertedPartnerID = convPartner.String
	l.ConvertedAccountID = convAccount.String
	return &l, nil
}
