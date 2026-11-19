//go:build pg

package store

// Phase 59: signature ride soundscapes — pg implementation. RLS-scoped
// via tenantTx with explicit tenant_id predicates. The
// one-render-per-booking idempotency key lands on the unique index from
// migration 0038; a unique violation surfaces as ErrConflict so the
// domain treats a concurrent generate as a replay, not a second render.

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func (p *postgres) Soundscapes() SoundscapeStore { return pgSoundscapes{p} }

type pgSoundscapes struct{ p *postgres }

const soundscapeCols = `id, tenant_id, booking_id, client_id, status,
	genre, language, mood, tempo_bpm, title, duration_seconds,
	provider, ai_composed, detail, media_id, fallback_id,
	idempotency_key, created_at, updated_at`

func (s pgSoundscapes) Create(ctx context.Context, sc *model.Soundscape) error {
	tx, err := s.p.tenantTx(ctx, sc.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO soundscapes (`+soundscapeCols+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		sc.ID, sc.TenantID, sc.BookingID, sc.ClientID, sc.Status,
		sc.Genre, sc.Language, sc.Mood, sc.TempoBPM, sc.Title, sc.Duration,
		sc.Provider, sc.AiComposed, sc.Detail, sc.MediaID, sc.FallbackID,
		sc.IdempotencyKey, sc.CreatedAt, sc.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return httpx.ErrConflict
		}
		return err
	}
	return tx.Commit()
}

func scanSoundscape(row interface{ Scan(...any) error }) (*model.Soundscape, error) {
	var sc model.Soundscape
	err := row.Scan(&sc.ID, &sc.TenantID, &sc.BookingID, &sc.ClientID, &sc.Status,
		&sc.Genre, &sc.Language, &sc.Mood, &sc.TempoBPM, &sc.Title, &sc.Duration,
		&sc.Provider, &sc.AiComposed, &sc.Detail, &sc.MediaID, &sc.FallbackID,
		&sc.IdempotencyKey, &sc.CreatedAt, &sc.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &sc, nil
}

func (s pgSoundscapes) Get(ctx context.Context, tenantID, id string) (*model.Soundscape, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	sc, err := scanSoundscape(tx.QueryRowContext(ctx,
		`SELECT `+soundscapeCols+` FROM soundscapes WHERE tenant_id=$1 AND id=$2`, tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return sc, tx.Commit()
}

func (s pgSoundscapes) ForBooking(ctx context.Context, tenantID, bookingID string) (*model.Soundscape, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	sc, err := scanSoundscape(tx.QueryRowContext(ctx,
		`SELECT `+soundscapeCols+` FROM soundscapes
		 WHERE tenant_id=$1 AND booking_id=$2 ORDER BY created_at DESC LIMIT 1`, tenantID, bookingID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return sc, tx.Commit()
}

func (s pgSoundscapes) Update(ctx context.Context, sc *model.Soundscape) error {
	tx, err := s.p.tenantTx(ctx, sc.TenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE soundscapes SET status=$3, genre=$4, language=$5, mood=$6,
		 tempo_bpm=$7, title=$8, duration_seconds=$9, provider=$10,
		 ai_composed=$11, detail=$12, media_id=$13, fallback_id=$14, updated_at=$15
		 WHERE tenant_id=$1 AND id=$2`,
		sc.TenantID, sc.ID, sc.Status, sc.Genre, sc.Language, sc.Mood,
		sc.TempoBPM, sc.Title, sc.Duration, sc.Provider,
		sc.AiComposed, sc.Detail, sc.MediaID, sc.FallbackID, sc.UpdatedAt)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.ErrNotFound
	}
	return tx.Commit()
}

func (s pgSoundscapes) ListForClient(ctx context.Context, tenantID, clientID string, limit int) ([]model.Soundscape, error) {
	tx, err := s.p.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if limit <= 0 {
		limit = 50
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT `+soundscapeCols+` FROM soundscapes
		 WHERE tenant_id=$1 AND client_id=$2 ORDER BY created_at DESC LIMIT $3`,
		tenantID, clientID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Soundscape
	for rows.Next() {
		sc, err := scanSoundscape(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}
