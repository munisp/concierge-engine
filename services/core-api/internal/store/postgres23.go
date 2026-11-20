//go:build pg

package store

// Phase 62: Postgres chauffeur-link store (upsert/clear/list, RLS via
// tenantTx) plus the matcher's prior-trips count across the
// assignments → bookings → service_requests chain.

import (
	"context"

	"concierge-engine/services/core-api/internal/model"
)

func (p *postgres) ChauffeurLinks() ChauffeurLinkStore { return pgChauffeurLinks{p} }

type pgChauffeurLinks struct{ p *postgres }

func (s pgChauffeurLinks) Set(ctx context.Context, l *model.ChauffeurLink) error {
	tx, err := s.p.tenantTx(ctx, l.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO chauffeur_links (tenant_id, client_id, crew_member_id, kind, created_at)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (tenant_id, client_id, crew_member_id)
		 DO UPDATE SET kind = EXCLUDED.kind`,
		l.TenantID, l.ClientID, l.CrewMemberID, l.Kind, l.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgChauffeurLinks) Clear(ctx context.Context, tenantID, clientID, crewID string) error {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM chauffeur_links WHERE tenant_id=$1 AND client_id=$2 AND crew_member_id=$3`,
		tenantID, clientID, crewID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s pgChauffeurLinks) ListForClient(ctx context.Context, tenantID, clientID string) ([]model.ChauffeurLink, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT tenant_id, client_id, crew_member_id, kind, created_at
		 FROM chauffeur_links WHERE tenant_id=$1 AND client_id=$2
		 ORDER BY created_at`, tenantID, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.ChauffeurLink{}
	for rows.Next() {
		var l model.ChauffeurLink
		if err := rows.Scan(&l.TenantID, &l.ClientID, &l.CrewMemberID, &l.Kind, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

// CountClientTrips: completed trips this client took with this crew
// member (assignments → bookings → service_requests).
func (s pgDispatch) CountClientTrips(ctx context.Context, tenantID, clientID, crewMemberID string) (int, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM assignments a
		 JOIN bookings b ON b.id = a.booking_id
		 JOIN service_requests r ON r.id = b.request_id
		 WHERE a.tenant_id=$1 AND a.crew_member_id=$2
		   AND r.client_id=$3 AND a.status='COMPLETED_TRIP'`,
		tenantID, nilIfEmpty(crewMemberID), clientID).Scan(&n); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}
