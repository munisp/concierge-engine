//go:build pg

package store

// Phase 61: itinerary legs — pg implementation. RLS-scoped via tenantTx.
// booking_id / linked_leg_id are nullable uuid FKs — empty strings map to
// NULL (nilIfEmpty); zero times map to NULL (nilIfZero).

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (p *postgres) Itineraries() ItineraryStore { return pgItineraries{p} }

type pgItineraries struct{ p *postgres }

const itineraryCols = `id, tenant_id, client_id, booking_id, linked_leg_id,
	kind, title, ref, flight_number, origin, destination,
	sched_start, sched_end, actual_start, status, delay_minutes, source,
	created_at, updated_at`

func (s pgItineraries) Create(ctx context.Context, l *model.ItineraryLeg) error {
	tx, err := s.p.tenantTx(ctx, l.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO itinerary_legs (`+itineraryCols+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		l.ID, l.TenantID, l.ClientID, nilIfEmpty(l.BookingID), nilIfEmpty(l.LinkedLegID),
		l.Kind, l.Title, l.Ref, l.FlightNumber, l.Origin, l.Destination,
		l.SchedStart, nilIfZero(l.SchedEnd), nilIfZero(l.ActualStart),
		l.Status, l.DelayMinutes, l.Source, l.CreatedAt, l.UpdatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func scanItinerary(row interface{ Scan(...any) error }) (*model.ItineraryLeg, error) {
	var l model.ItineraryLeg
	var bookingID, linkedLegID sql.NullString
	var schedEnd, actualStart *time.Time
	err := row.Scan(&l.ID, &l.TenantID, &l.ClientID, &bookingID, &linkedLegID,
		&l.Kind, &l.Title, &l.Ref, &l.FlightNumber, &l.Origin, &l.Destination,
		&l.SchedStart, &schedEnd, &actualStart, &l.Status, &l.DelayMinutes, &l.Source,
		&l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return nil, err
	}
	l.BookingID = bookingID.String
	l.LinkedLegID = linkedLegID.String
	if schedEnd != nil {
		l.SchedEnd = *schedEnd
	}
	if actualStart != nil {
		l.ActualStart = *actualStart
	}
	return &l, nil
}

func (s pgItineraries) Get(ctx context.Context, tenantID, id string) (*model.ItineraryLeg, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	l, err := scanItinerary(tx.QueryRowContext(ctx,
		`SELECT `+itineraryCols+` FROM itinerary_legs WHERE tenant_id=$1 AND id=$2`, tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return l, tx.Commit()
}

func (s pgItineraries) Update(ctx context.Context, l *model.ItineraryLeg) error {
	tx, err := s.p.tenantTx(ctx, l.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE itinerary_legs SET booking_id=$3, linked_leg_id=$4, kind=$5, title=$6,
		 ref=$7, flight_number=$8, origin=$9, destination=$10, sched_start=$11,
		 sched_end=$12, actual_start=$13, status=$14, delay_minutes=$15, source=$16,
		 updated_at=$17
		 WHERE tenant_id=$1 AND id=$2`,
		l.TenantID, l.ID, nilIfEmpty(l.BookingID), nilIfEmpty(l.LinkedLegID), l.Kind, l.Title,
		l.Ref, l.FlightNumber, l.Origin, l.Destination, l.SchedStart,
		nilIfZero(l.SchedEnd), nilIfZero(l.ActualStart), l.Status, l.DelayMinutes, l.Source,
		l.UpdatedAt)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

func (s pgItineraries) list(ctx context.Context, tenantID, where string, args ...any) ([]model.ItineraryLeg, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT `+itineraryCols+` FROM itinerary_legs WHERE tenant_id=$1 AND `+where+
			` ORDER BY sched_start`, append([]any{tenantID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ItineraryLeg
	for rows.Next() {
		l, err := scanItinerary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (s pgItineraries) ListForClient(ctx context.Context, tenantID, clientID string, limit int) ([]model.ItineraryLeg, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.list(ctx, tenantID, `client_id=$2 LIMIT $3`, clientID, limit)
}

func (s pgItineraries) ForBooking(ctx context.Context, tenantID, bookingID string) ([]model.ItineraryLeg, error) {
	return s.list(ctx, tenantID, `booking_id=$2`, bookingID)
}

func (s pgItineraries) FlightLegsDue(ctx context.Context, tenantID string, from, to time.Time) ([]model.ItineraryLeg, error) {
	return s.list(ctx, tenantID,
		`kind='flight' AND status NOT IN ('ARRIVED','COMPLETED','CANCELLED')
		 AND sched_start >= $2 AND sched_start <= $3`, from, to)
}
