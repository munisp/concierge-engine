-- Phase 47: KYC / identity verification lane.
--
-- Until now Client.KYCStatus was a write-only field: set to 'none' at
-- registration, never checked, never advanced. This migration adds the
-- case workflow that makes identity verification real — one case per
-- verification attempt, operator decision, and the per-tenant thresholds
-- that gate high-value bookings behind it.
--
-- Data minimization by design: the platform stores document METADATA
-- (type + operator-side reference), never the document itself. Erasure
-- (Art. 17) scrubs the document references immediately; the case row
-- (level, decision, timestamps) survives as the AML accountability record,
-- same class as financial records.

ALTER TABLE tenants ADD COLUMN IF NOT EXISTS kyc_config jsonb NOT NULL DEFAULT '{}';

CREATE TABLE IF NOT EXISTS kyc_cases (
    id               uuid PRIMARY KEY,
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    client_id        uuid NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    level            text NOT NULL CHECK (level IN ('STANDARD', 'ENHANCED')),
    status           text NOT NULL DEFAULT 'PENDING'
                     CHECK (status IN ('PENDING', 'IN_REVIEW', 'APPROVED', 'REJECTED', 'EXPIRED')),
    provider         text NOT NULL DEFAULT 'manual',
    document_type    text NOT NULL DEFAULT '',
    document_ref     text NOT NULL DEFAULT '',
    decision_reason  text NOT NULL DEFAULT '',
    decided_by       text NOT NULL DEFAULT '',
    submitted_at     timestamptz,
    decided_at       timestamptz,
    idempotency_key  text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- One open case per (tenant, client, level): a second attempt at the same
-- level while one is in flight is a conflict, not a duplicate row.
CREATE UNIQUE INDEX IF NOT EXISTS kyc_cases_open_unique
    ON kyc_cases (tenant_id, client_id, level)
    WHERE status IN ('PENDING', 'IN_REVIEW');

-- Idempotent open: replayed create with the same key surfaces the original.
CREATE UNIQUE INDEX IF NOT EXISTS kyc_cases_idem_unique
    ON kyc_cases (tenant_id, idempotency_key)
    WHERE idempotency_key <> '';

CREATE INDEX IF NOT EXISTS idx_kyc_cases_tenant_status ON kyc_cases (tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_kyc_cases_client ON kyc_cases (tenant_id, client_id);

ALTER TABLE kyc_cases ENABLE ROW LEVEL SECURITY;
ALTER TABLE kyc_cases FORCE ROW LEVEL SECURITY;
CREATE POLICY kyc_cases_tenant_isolation ON kyc_cases
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
