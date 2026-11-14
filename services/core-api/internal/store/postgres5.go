//go:build pg

// Postgres Phase 6 store: service-area polygons via PostGIS geography.
// All operations run inside tenantTx (app.tenant_id GUC + FORCE RLS from
// db/migrations/0006_geo.sql).
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"concierge-engine/services/core-api/internal/model"
)

type pgGeo struct{ p *postgres }

func (s pgGeo) AddArea(ctx context.Context, a *model.ServiceArea) error {
	// [lat,lng] ring -> WKT POLYGON((lng lat, ...)), explicitly closed.
	var pts []string
	for _, p := range a.Polygon {
		pts = append(pts, fmt.Sprintf("%f %f", p[1], p[0]))
	}
	pts = append(pts, fmt.Sprintf("%f %f", a.Polygon[0][1], a.Polygon[0][0]))
	wkt := "POLYGON((" + strings.Join(pts, ",") + "))"

	tx, err := s.p.tenantTx(ctx, a.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO service_areas (id, tenant_id, name, geom, created_at)
		 VALUES ($1, $2, $3, ST_GeogFromText($4), $5)`,
		a.ID, a.TenantID, a.Name, wkt, a.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgGeo) Areas(ctx context.Context, tenantID string) ([]model.ServiceArea, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, name, ST_AsText(geom::geometry), created_at
		 FROM service_areas WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.ServiceArea{}
	for rows.Next() {
		var a model.ServiceArea
		var wkt string
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Name, &wkt, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Polygon = parseWKTPolygon(wkt)
		out = append(out, a)
	}
	return out, rows.Err()
}

// parseWKTPolygon reads "POLYGON((lng lat, ...))" back into [lat,lng] rings.
func parseWKTPolygon(wkt string) [][2]float64 {
	wkt = strings.TrimPrefix(wkt, "POLYGON((")
	wkt = strings.TrimSuffix(wkt, "))")
	var ring [][2]float64
	for _, pt := range strings.Split(wkt, ",") {
		var lng, lat float64
		_, _ = fmt.Sscanf(strings.TrimSpace(pt), "%f %f", &lng, &lat)
		ring = append(ring, [2]float64{lat, lng})
	}
	// drop the duplicated closing vertex
	if n := len(ring); n > 1 && ring[0] == ring[n-1] {
		ring = ring[:n-1]
	}
	return ring
}

func (p *postgres) Geo() GeoStore { return pgGeo{p} }

// ---------------------------------------------------------- Phase 11 receipts

func (p *postgres) Receipts() ReceiptStore { return pgReceipts{p} }

type pgReceipts struct{ p *postgres }

func (s pgReceipts) AddReceipt(ctx context.Context, rc *model.MessageReceipt) error {
	tx, err := s.p.tenantTx(ctx, rc.TenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO message_receipts (id, tenant_id, channel, external_ref, status, at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (tenant_id, external_ref, status) DO NOTHING`,
		rc.ID, rc.TenantID, rc.Channel, rc.ExternalRef, rc.Status, rc.At, rc.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgReceipts) Receipts(ctx context.Context, tenantID, externalRef string) ([]model.MessageReceipt, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, channel, external_ref, status, at, created_at
		 FROM message_receipts WHERE tenant_id = $1 AND external_ref = $2
		 ORDER BY at`, tenantID, externalRef)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.MessageReceipt{}
	for rows.Next() {
		var rc model.MessageReceipt
		if err := rows.Scan(&rc.ID, &rc.TenantID, &rc.Channel, &rc.ExternalRef,
			&rc.Status, &rc.At, &rc.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}

func (s pgReceipts) ReceiptCounts(ctx context.Context, tenantID string) (map[string]int, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM message_receipts WHERE tenant_id = $1 GROUP BY status`,
		tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	return out, rows.Err()
}

// ---------------------------------------------------- Phase 12 receipt links

// LinkReceipt: external_ref -> case. ON CONFLICT DO NOTHING on the unique
// (tenant_id, external_ref): first link wins, replays are free. The row id
// is gen_random_uuid() — the link's identity is the ref, not the row.
func (s pgReceipts) LinkReceipt(ctx context.Context, tenantID, externalRef, caseID string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO receipt_links (tenant_id, external_ref, case_id, created_at)
		 VALUES ($1, $2, $3, now())
		 ON CONFLICT (tenant_id, external_ref) DO NOTHING`,
		tenantID, externalRef, caseID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgReceipts) LinkedCase(ctx context.Context, tenantID, externalRef string) (string, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var caseID string
	err = tx.QueryRowContext(ctx,
		`SELECT case_id FROM receipt_links WHERE tenant_id = $1 AND external_ref = $2`,
		tenantID, externalRef).Scan(&caseID)
	if err == sql.ErrNoRows {
		return "", nil // unlinked ref is not an error (e.g. template sends)
	}
	if err != nil {
		return "", err
	}
	return caseID, tx.Commit()
}

func (s pgReceipts) ReceiptsByStatus(ctx context.Context, tenantID, status string, limit int) ([]model.MessageReceipt, error) {
	if limit <= 0 {
		limit = 50
	}
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, tenant_id, channel, external_ref, status, at, created_at
		 FROM message_receipts WHERE tenant_id = $1 AND status = $2
		 ORDER BY at DESC LIMIT $3`, tenantID, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.MessageReceipt{}
	for rows.Next() {
		var rc model.MessageReceipt
		if err := rows.Scan(&rc.ID, &rc.TenantID, &rc.Channel, &rc.ExternalRef,
			&rc.Status, &rc.At, &rc.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}

func (s pgReceipts) RefsForCase(ctx context.Context, tenantID, caseID string) ([]string, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT external_ref FROM receipt_links
		 WHERE tenant_id = $1 AND case_id = $2 ORDER BY created_at`, tenantID, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}
