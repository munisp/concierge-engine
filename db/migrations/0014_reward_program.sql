-- Phase 19: reward sharing program — two-sided credit (referee welcome +
-- referrer reward), per-tenant program config, and optional credit expiry.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS reward_program jsonb NOT NULL DEFAULT '{}';
ALTER TABLE client_rewards ADD COLUMN IF NOT EXISTS expires_at timestamptz NULL;

-- State machine gains EXPIRED (credit lapsed unused; only when the program
-- sets expiry_days — credit never expires silently).
ALTER TABLE client_rewards DROP CONSTRAINT IF EXISTS client_rewards_state_check;
ALTER TABLE client_rewards ADD CONSTRAINT client_rewards_state_check
    CHECK (state IN ('ACCRUED','REDEEMED','EXPIRED'));

-- Expiry sweep lookups: accrued rewards past their expiry.
CREATE INDEX IF NOT EXISTS idx_client_rewards_expiry
    ON client_rewards (tenant_id, client_id, expires_at)
    WHERE state = 'ACCRUED' AND expires_at IS NOT NULL;
