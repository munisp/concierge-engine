-- Phase 60: ride modes & cabin pre-conditioning. Tenants may override the
-- CONTENT of house mode presets (focus/relax/family/celebrate/custom);
-- the mode names stay the house enum so preference validation never forks
-- per tenant.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS mode_presets jsonb NOT NULL DEFAULT '{}';
