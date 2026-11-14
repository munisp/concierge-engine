-- Phase 17: tax/VAT readiness + per-tenant payment configuration.
-- quotes: tax component of the grand total with engine provenance.
-- tenants: tax posture (engine/rate/country) and PSP enablement.
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS tax_minor bigint NOT NULL DEFAULT 0;
ALTER TABLE quotes ADD COLUMN IF NOT EXISTS tax_engine text NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS tax_config jsonb NOT NULL DEFAULT '{}';
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS payment_config jsonb NOT NULL DEFAULT '{}';
