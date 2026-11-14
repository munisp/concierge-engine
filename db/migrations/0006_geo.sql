-- Phase 6 geo: PostGIS service-area polygons + partner coordinates.
-- Mirrors the in-memory store semantics (ring containment) with exact
-- spherical geography in production.

CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS service_areas (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    name        TEXT NOT NULL,
    geom        geography(Polygon, 4326) NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS service_areas_tenant ON service_areas (tenant_id);
CREATE INDEX IF NOT EXISTS service_areas_geom ON service_areas USING GIST (geom);

ALTER TABLE service_areas ENABLE ROW LEVEL SECURITY;
ALTER TABLE service_areas FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON service_areas
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

-- Partner base-of-operations coordinates for proximity ranking.
ALTER TABLE partners ADD COLUMN IF NOT EXISTS lat DOUBLE PRECISION;
ALTER TABLE partners ADD COLUMN IF NOT EXISTS lng DOUBLE PRECISION;
