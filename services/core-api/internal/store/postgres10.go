//go:build pg

package store

// Phase 21: message templates. Tenant-scoped via tenantTx (FORCE RLS from
// db/migrations/0016_phase21.sql). Upsert keyed on the (tenant_id, name)
// UNIQUE — re-saving a name updates in place, mirroring the memory engine.

import (
	"context"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (p *postgres) Templates() TemplateStore { return pgTemplates{p} }

type pgTemplates struct{ p *postgres }

const templateCols = `id, tenant_id, name, purpose, body, created_at, updated_at`

func scanTemplate(row interface{ Scan(...any) error }) (*model.MessageTemplate, error) {
	var t model.MessageTemplate
	err := row.Scan(&t.ID, &t.TenantID, &t.Name, &t.Purpose, &t.Body, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &t, nil
}

func (s pgTemplates) Upsert(ctx context.Context, t *model.MessageTemplate) error {
	tx, err := s.p.tenantTx(ctx, t.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	err = tx.QueryRowContext(ctx,
		`INSERT INTO message_templates (id, tenant_id, name, purpose, body, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (tenant_id, name) DO UPDATE
		   SET purpose=EXCLUDED.purpose, body=EXCLUDED.body, updated_at=EXCLUDED.updated_at
		 RETURNING id, created_at`,
		t.ID, t.TenantID, t.Name, t.Purpose, t.Body, t.CreatedAt, t.UpdatedAt).
		Scan(&t.ID, &t.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgTemplates) Get(ctx context.Context, tenantID, id string) (*model.MessageTemplate, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	t, err := scanTemplate(tx.QueryRowContext(ctx,
		`SELECT `+templateCols+` FROM message_templates WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	return t, tx.Commit()
}

func (s pgTemplates) GetByName(ctx context.Context, tenantID, name string) (*model.MessageTemplate, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	t, err := scanTemplate(tx.QueryRowContext(ctx,
		`SELECT `+templateCols+` FROM message_templates WHERE name=$1`, name))
	if err != nil {
		return nil, err
	}
	return t, tx.Commit()
}

func (s pgTemplates) List(ctx context.Context, tenantID string) ([]model.MessageTemplate, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+templateCols+` FROM message_templates ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.MessageTemplate
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, tx.Commit()
}

func (s pgTemplates) Delete(ctx context.Context, tenantID, id string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM message_templates WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}
