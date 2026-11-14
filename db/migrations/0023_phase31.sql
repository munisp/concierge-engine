-- Phase 31: media pipeline — tenant-owned site imagery (hero + service
-- cards) stored inline, tenant-scoped, and the catalog link that points a
-- service at its card image.
CREATE TABLE IF NOT EXISTS tenant_media (
    id           uuid PRIMARY KEY,
    tenant_id    uuid NOT NULL REFERENCES tenants(id),
    kind         text NOT NULL DEFAULT 'misc'
                 CHECK (kind IN ('hero', 'service', 'misc')),
    content_type text NOT NULL
                 CHECK (content_type IN ('image/jpeg', 'image/png', 'image/webp')),
    data         bytea NOT NULL CHECK (octet_length(data) <= 8388608),
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS tenant_media_tenant ON tenant_media (tenant_id);
ALTER TABLE tenant_media ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_media FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_media
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- Catalog card imagery: points at a tenant_media row (no FK — media may be
-- deleted without touching the catalog; the site falls back to the
-- monogram tile for a dangling id).
ALTER TABLE catalog_services ADD COLUMN IF NOT EXISTS image_id text NOT NULL DEFAULT '';
