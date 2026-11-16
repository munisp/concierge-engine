-- Phase 46: data retention & minimization (GDPR Art. 5(1)(e) — storage
-- limitation). Per-tenant retention windows for operational data with
-- personal content; financial records, the consent evidence ledger and the
-- audit log are exempt (legal/accounting retention + accountability).
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS retention_config jsonb NOT NULL DEFAULT '{}';
