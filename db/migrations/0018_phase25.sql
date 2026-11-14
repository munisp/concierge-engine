-- Phase 25: lifecycle engagement config (post-trip follow-up, win-back).
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS lifecycle_config jsonb NOT NULL DEFAULT '{}';
