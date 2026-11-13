-- Phase 4b schema: per-case semantic embeddings for similar-case recall.
-- Vectors stored as jsonb arrays; pgvector is the scale-out path once a
-- tenant's case count makes in-app cosine ranking hot (deferred-register).
-- Standard FORCE + tenant_isolation RLS.

CREATE TABLE IF NOT EXISTS case_embeddings (
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    case_id    uuid NOT NULL REFERENCES cases(id) ON DELETE CASCADE,
    embedding  jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, case_id)
);

ALTER TABLE case_embeddings ENABLE ROW LEVEL SECURITY;
ALTER TABLE case_embeddings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON case_embeddings
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
