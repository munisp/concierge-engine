-- Phase 11: channel delivery/read receipts (SLA evidence).
-- Idempotent per (tenant, external_ref, status) — channel redelivery is free.

CREATE TABLE IF NOT EXISTS message_receipts (
    id           uuid PRIMARY KEY,
    tenant_id    uuid NOT NULL REFERENCES tenants(id),
    channel      TEXT NOT NULL,
    external_ref TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('sent','delivered','read','failed')),
    at           TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, external_ref, status)
);

CREATE INDEX IF NOT EXISTS idx_message_receipts_ref
    ON message_receipts (tenant_id, external_ref);

ALTER TABLE message_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE message_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON message_receipts
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
