-- Phase 62: chauffeur matching — crew capabilities, recorded match
-- decisions, and rider↔chauffeur links (favorite / blocked).

ALTER TABLE crew_members
    ADD COLUMN IF NOT EXISTS languages jsonb NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS styles jsonb NOT NULL DEFAULT '[]';

ALTER TABLE assignments
    ADD COLUMN IF NOT EXISTS match_score integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS match_reasons jsonb NOT NULL DEFAULT '[]';

CREATE TABLE IF NOT EXISTS chauffeur_links (
    tenant_id      uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    client_id      uuid NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    crew_member_id uuid NOT NULL REFERENCES crew_members(id) ON DELETE CASCADE,
    kind           text NOT NULL CHECK (kind IN ('favorite','blocked')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, client_id, crew_member_id)
);

ALTER TABLE chauffeur_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE chauffeur_links FORCE ROW LEVEL SECURITY;

CREATE POLICY chauffeur_links_tenant_isolation ON chauffeur_links
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

-- Matching scans: links for one rider, prior trips per crew.
CREATE INDEX IF NOT EXISTS chauffeur_links_client_idx
    ON chauffeur_links (tenant_id, client_id);
CREATE INDEX IF NOT EXISTS assignments_crew_completed_idx
    ON assignments (tenant_id, crew_member_id)
    WHERE status = 'COMPLETED_TRIP';
