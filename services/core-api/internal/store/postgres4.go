//go:build pg

// Postgres Phase 4 store: cases, tasks, notes. All operations run inside
// tenantTx (app.tenant_id GUC + FORCE RLS from db/migrations/0004_cases.sql)
// — cases carry client PII context, so there are no tenant-less exceptions
// here.
package store

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (p *postgres) Cases() CaseStore { return pgCases{p} }

type pgCases struct{ p *postgres }

const caseCols = `id, tenant_id, client_id, title, kind, priority, state, owner_id,
	summary, triage_engine, request_id, booking_id, conversation_id, tags,
	sla_due_at, escalated, created_at, updated_at, resolved_at, closed_at`

func scanCase(row interface{ Scan(...any) error }) (*model.Case, error) {
	var c model.Case
	var tags []byte
	err := row.Scan(&c.ID, &c.TenantID, &c.ClientID, &c.Title, &c.Kind, &c.Priority,
		&c.State, &c.OwnerID, &c.Summary, &c.TriageEngine, &c.RequestID, &c.BookingID,
		&c.ConversationID, &tags, &c.SLADueAt, &c.Escalated,
		&c.CreatedAt, &c.UpdatedAt, &c.ResolvedAt, &c.ClosedAt)
	if err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal(tags, &c.Tags)
	return &c, nil
}

func (s pgCases) Create(ctx context.Context, c *model.Case) error {
	tags, _ := json.Marshal(c.Tags)
	tx, err := s.p.tenantTx(ctx, c.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO cases (id, tenant_id, client_id, title, kind, priority, state, owner_id,
			summary, triage_engine, request_id, booking_id, conversation_id, tags,
			sla_due_at, escalated, idempotency_key)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		c.ID, c.TenantID, c.ClientID, c.Title, c.Kind, c.Priority, c.State, c.OwnerID,
		c.Summary, c.TriageEngine, c.RequestID, c.BookingID, c.ConversationID, tags,
		c.SLADueAt, c.Escalated, nilIfEmpty(c.IdempotencyKey))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s pgCases) Get(ctx context.Context, tenantID, id string) (*model.Case, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := scanCase(tx.QueryRowContext(ctx,
		`SELECT `+caseCols+` FROM cases WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	return c, tx.Commit()
}

func (s pgCases) Update(ctx context.Context, c *model.Case) error {
	tags, _ := json.Marshal(c.Tags)
	tx, err := s.p.tenantTx(ctx, c.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE cases SET client_id=$2, title=$3, kind=$4, priority=$5, state=$6, owner_id=$7,
			summary=$8, triage_engine=$9, request_id=$10, booking_id=$11, conversation_id=$12,
			tags=$13, sla_due_at=$14, escalated=$15, updated_at=$16, resolved_at=$17, closed_at=$18
		 WHERE id=$1`,
		c.ID, c.ClientID, c.Title, c.Kind, c.Priority, c.State, c.OwnerID,
		c.Summary, c.TriageEngine, c.RequestID, c.BookingID, c.ConversationID,
		tags, c.SLADueAt, c.Escalated, c.UpdatedAt, c.ResolvedAt, c.ClosedAt)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

func (s pgCases) List(ctx context.Context, tenantID, state, kind string) ([]model.Case, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := `SELECT ` + caseCols + ` FROM cases`
	var args []any
	clause := ""
	if state != "" {
		args = append(args, state)
		clause += ` AND state=$` + strconv.Itoa(len(args))
	}
	if kind != "" {
		args = append(args, kind)
		clause += ` AND kind=$` + strconv.Itoa(len(args))
	}
	if clause != "" {
		q += ` WHERE ` + clause[5:]
	}
	q += ` ORDER BY created_at DESC`
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Case{}
	for rows.Next() {
		c, err := scanCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, tx.Commit()
}

func (s pgCases) Overdue(ctx context.Context, tenantID string, now time.Time) ([]model.Case, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+caseCols+` FROM cases
		 WHERE state NOT IN ('RESOLVED','CLOSED') AND sla_due_at < $1
		 ORDER BY sla_due_at`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Case{}
	for rows.Next() {
		c, err := scanCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, tx.Commit()
}

func (s pgCases) AddTask(ctx context.Context, t *model.CaseTask) error {
	tx, err := s.p.tenantTx(ctx, t.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO case_tasks (id, tenant_id, case_id, title, done) VALUES ($1,$2,$3,$4,$5)`,
		t.ID, t.TenantID, t.CaseID, t.Title, t.Done)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgCases) Tasks(ctx context.Context, tenantID, caseID string) ([]model.CaseTask, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, case_id, title, done, done_at, created_at
		 FROM case_tasks WHERE case_id=$1 ORDER BY created_at`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.CaseTask{}
	for rows.Next() {
		var t model.CaseTask
		if err := rows.Scan(&t.ID, &t.TenantID, &t.CaseID, &t.Title, &t.Done, &t.DoneAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, tx.Commit()
}

func (s pgCases) UpdateTask(ctx context.Context, t *model.CaseTask) error {
	tx, err := s.p.tenantTx(ctx, t.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE case_tasks SET title=$2, done=$3, done_at=$4 WHERE id=$1`,
		t.ID, t.Title, t.Done, t.DoneAt)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

func (s pgCases) AddNote(ctx context.Context, n *model.CaseNote) error {
	tx, err := s.p.tenantTx(ctx, n.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO case_notes (id, tenant_id, case_id, author, body) VALUES ($1,$2,$3,$4,$5)`,
		n.ID, n.TenantID, n.CaseID, n.Author, n.Body)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgCases) Notes(ctx context.Context, tenantID, caseID string) ([]model.CaseNote, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, case_id, author, body, created_at
		 FROM case_notes WHERE case_id=$1 ORDER BY created_at`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.CaseNote{}
	for rows.Next() {
		var n model.CaseNote
		if err := rows.Scan(&n.ID, &n.TenantID, &n.CaseID, &n.Author, &n.Body, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, tx.Commit()
}
