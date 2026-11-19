-- Phase 59: signature ride soundscapes — one AI-composed track per
-- booking, composed from the rider's preference profile and rendered by a
-- music provider. Provenance is stored, never implied: provider,
-- ai_composed, and the verbatim detail/fallback reason are columns, so
-- nothing is ever presented as composed when it was degraded.

CREATE TABLE IF NOT EXISTS soundscapes (
    id               uuid PRIMARY KEY,
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    booking_id       uuid NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    client_id        uuid NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    status           text NOT NULL DEFAULT 'RENDERING'
                     CHECK (status IN ('RENDERING', 'RENDERED', 'DEGRADED', 'FAILED')),
    genre            text NOT NULL DEFAULT '',
    language         text NOT NULL DEFAULT '',
    mood             text NOT NULL DEFAULT '', -- ambient | chill | upbeat
    tempo_bpm        integer NOT NULL DEFAULT 0,
    title            text NOT NULL DEFAULT '',
    duration_seconds integer NOT NULL DEFAULT 0,
    provider         text NOT NULL DEFAULT '', -- mubert | beatoven | musicgen | ''
    ai_composed      boolean NOT NULL DEFAULT false,
    detail           text NOT NULL DEFAULT '',
    media_id         text NOT NULL DEFAULT '',
    fallback_id      text NOT NULL DEFAULT '',
    idempotency_key  text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- One render per booking: replayed booking.confirmed events and double
-- clicks conflict instead of double-rendering.
CREATE UNIQUE INDEX IF NOT EXISTS soundscapes_idem
    ON soundscapes (tenant_id, idempotency_key)
    WHERE idempotency_key <> '';
CREATE INDEX IF NOT EXISTS idx_soundscapes_booking ON soundscapes (tenant_id, booking_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_soundscapes_client  ON soundscapes (tenant_id, client_id, created_at DESC);

ALTER TABLE soundscapes ENABLE ROW LEVEL SECURITY;
ALTER TABLE soundscapes FORCE ROW LEVEL SECURITY;
CREATE POLICY soundscapes_tenant_isolation ON soundscapes
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
