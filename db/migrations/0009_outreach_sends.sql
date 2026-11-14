-- Phase 14: proactive outreach sends (consent-gated, frequency-capped,
-- idempotent) — the platform's only non-reactive channel acts.

CREATE TABLE IF NOT EXISTS outreach_sends (
    id              uuid PRIMARY KEY,
    tenant_id       uuid NOT NULL REFERENCES tenants(id),
    client_id       uuid NOT NULL REFERENCES clients(id),
    channel         text NOT NULL DEFAULT 'whatsapp',
    purpose         text NOT NULL CHECK (purpose IN ('service','marketing')),
    body            text NOT NULL DEFAULT '',
    template        text NOT NULL DEFAULT '',
    params          jsonb NOT NULL DEFAULT '[]',
    external_ref    text NOT NULL DEFAULT '',
    status          text NOT NULL CHECK (status IN ('sent','held','failed')),
    detail          text NOT NULL DEFAULT '',
    idempotency_key text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, idempotency_key)
);

-- Frequency-cap lookups: marketing sends per client in a rolling window.
CREATE INDEX IF NOT EXISTS idx_outreach_client
    ON outreach_sends (tenant_id, client_id, purpose, created_at);

ALTER TABLE outreach_sends ENABLE ROW LEVEL SECURITY;
ALTER TABLE outreach_sends FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON outreach_sends
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
