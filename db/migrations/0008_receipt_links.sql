-- Phase 12: channel message id -> case correlation (receipt links).
-- Set by SendReply when the channel adapter returns a message id, so
-- delivery/read/failed receipts can alert on the right case.
-- First link wins: UNIQUE (tenant_id, external_ref), insert DO NOTHING.

CREATE TABLE IF NOT EXISTS receipt_links (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid NOT NULL REFERENCES tenants(id),
    external_ref TEXT NOT NULL,
    case_id      uuid NOT NULL REFERENCES cases(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, external_ref)
);

CREATE INDEX IF NOT EXISTS idx_receipt_links_case
    ON receipt_links (tenant_id, case_id);

ALTER TABLE receipt_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE receipt_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON receipt_links
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- Failure lookups for the SLA board: recent receipts by status.
CREATE INDEX IF NOT EXISTS idx_message_receipts_status
    ON message_receipts (tenant_id, status, at DESC);
