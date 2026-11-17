//go:build pg

package store

// Phase 31: tenant media. Tenant-scoped via tenantTx (FORCE RLS from
// db/migrations/0023_phase31.sql). Images are bytea — the upload cap
// (8 MB, enforced at the handler and again by the column CHECK) keeps rows
// small enough that inline storage beats an object-store hop for a site
// that serves a handful of images per tenant.

import (
	"context"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (p *postgres) Media() MediaStore { return pgMedia{p} }

type pgMedia struct{ p *postgres }

func (s pgMedia) Put(ctx context.Context, md *model.Media) error {
	tx, err := s.p.tenantTx(ctx, md.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	data := md.Data
	if data == nil {
		data = []byte{} // object-stored rows carry empty bytea (NOT NULL column)
	}
	size := md.SizeBytes
	if size == 0 {
		size = len(md.Data)
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO tenant_media (id, tenant_id, kind, content_type, data, size_bytes, storage_key, storage_engine)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		md.ID, md.TenantID, md.Kind, md.ContentType, data, size, md.StorageKey, md.StorageEngine)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgMedia) Get(ctx context.Context, tenantID, id string) (*model.Media, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var md model.Media
	err = tx.QueryRowContext(ctx,
		`SELECT id, tenant_id, kind, content_type, data,
		        COALESCE(NULLIF(size_bytes, 0), octet_length(data)),
		        created_at, storage_key, storage_engine
		 FROM tenant_media WHERE id=$1`, id).
		Scan(&md.ID, &md.TenantID, &md.Kind, &md.ContentType, &md.Data, &md.SizeBytes, &md.CreatedAt,
			&md.StorageKey, &md.StorageEngine)
	if err != nil {
		return nil, notFound(err)
	}
	return &md, tx.Commit()
}

func (s pgMedia) List(ctx context.Context, tenantID string) ([]model.Media, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, kind, content_type,
		        COALESCE(NULLIF(size_bytes, 0), octet_length(data)),
		        created_at, storage_key, storage_engine
		 FROM tenant_media ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Media{}
	for rows.Next() {
		var md model.Media
		if err := rows.Scan(&md.ID, &md.TenantID, &md.Kind, &md.ContentType, &md.SizeBytes, &md.CreatedAt,
			&md.StorageKey, &md.StorageEngine); err != nil {
			return nil, err
		}
		out = append(out, md)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (s pgMedia) Delete(ctx context.Context, tenantID, id string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM tenant_media WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

// MarkExternal (Phase 49): flip a legacy inline row to object storage in
// one statement — record key/engine/size and clear the bytea. Only rows
// still carrying inline bytes are touched, so a repeated backfill is a
// no-op.
func (s pgMedia) MarkExternal(ctx context.Context, tenantID, id, storageKey, engine string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE tenant_media
		 SET storage_key=$2, storage_engine=$3, size_bytes=octet_length(data), data=''
		 WHERE id=$1 AND storage_key=''`, id, storageKey, engine)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Either missing or already external — distinguish for honesty.
		var exists bool
		if err := tx.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM tenant_media WHERE id=$1)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return httpx.ErrNotFound
		}
	}
	return tx.Commit()
}
