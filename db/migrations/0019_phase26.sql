-- Phase 26: acquisition monetization config (fee on platform-attributed
-- new-client transactions, cohort goal for the 90-day pacing tracker).
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS acquisition_config jsonb NOT NULL DEFAULT '{}';
