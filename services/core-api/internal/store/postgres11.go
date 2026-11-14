//go:build pg

package store

// Phase 27: pg reviews — one review per (tenant, assignment), RLS-scoped,
// publishable subset for public SEO pages (migration 0020).

import (
	"context"
	"database/sql"
	"strconv"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (p *postgres) Reviews() ReviewStore { return pgReviews{p} }

type pgReviews struct{ p *postgres }

func (s pgReviews) Create(ctx context.Context, r *model.Review) error {
	tx, err := s.p.tenantTx(ctx, r.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx,
		`INSERT INTO reviews (id, tenant_id, client_id, assignment_id, rating, text, publish_consent)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (tenant_id, assignment_id) DO NOTHING RETURNING id`,
		r.ID, r.TenantID, r.ClientID, r.AssignmentID, r.Rating, r.Text, r.PublishConsent).Scan(&id)
	if err == sql.ErrNoRows {
		return httpx.ErrConflict
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgReviews) GetByAssignment(ctx context.Context, tenantID, assignmentID string) (*model.Review, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var r model.Review
	err = tx.QueryRowContext(ctx,
		`SELECT id, tenant_id, client_id, assignment_id, rating, text, publish_consent, created_at
		 FROM reviews WHERE assignment_id=$1`, assignmentID).
		Scan(&r.ID, &r.TenantID, &r.ClientID, &r.AssignmentID, &r.Rating, &r.Text, &r.PublishConsent, &r.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &r, tx.Commit()
}

func (s pgReviews) Consented(ctx context.Context, tenantID string) ([]model.Review, error) {
	return s.queryReviews(ctx, tenantID,
		`SELECT id, tenant_id, client_id, assignment_id, rating, text, publish_consent, created_at
		 FROM reviews WHERE publish_consent ORDER BY created_at DESC`, 0)
}

func (s pgReviews) ListAll(ctx context.Context, tenantID string, limit int) ([]model.Review, error) {
	return s.queryReviews(ctx, tenantID,
		`SELECT id, tenant_id, client_id, assignment_id, rating, text, publish_consent, created_at
		 FROM reviews ORDER BY created_at DESC`, limit)
}

func (s pgReviews) queryReviews(ctx context.Context, tenantID, sqlQ string, limit int) ([]model.Review, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if limit > 0 {
		sqlQ += ` LIMIT ` + strconv.Itoa(limit)
	}
	rows, err := tx.QueryContext(ctx, sqlQ)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Review
	for rows.Next() {
		var r model.Review
		if err := rows.Scan(&r.ID, &r.TenantID, &r.ClientID, &r.AssignmentID,
			&r.Rating, &r.Text, &r.PublishConsent, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
