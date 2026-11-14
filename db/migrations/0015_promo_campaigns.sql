-- Phase 20: promo-code campaigns + per-tenant outreach config.
-- promo_campaigns: fixed-credit codes clients redeem for reward credit
-- (the Phase 19 program was referral-only; promos extend it to campaigns).
-- client_rewards: promo credit has no source booking (NULL) and carries the
-- redeemed promo code; one redemption per client per code.

CREATE TABLE IF NOT EXISTS promo_campaigns (
    id              uuid PRIMARY KEY,
    tenant_id       uuid NOT NULL REFERENCES tenants(id),
    code            text NOT NULL,
    credit_minor    bigint NOT NULL CHECK (credit_minor > 0),
    currency        char(3) NOT NULL,
    max_redemptions int NOT NULL DEFAULT 0,   -- 0 = unlimited
    expires_at      timestamptz,              -- campaign (and credit) validity
    active          boolean NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, code)
);

ALTER TABLE promo_campaigns ENABLE ROW LEVEL SECURITY;
ALTER TABLE promo_campaigns FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON promo_campaigns
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE client_rewards ALTER COLUMN source_booking_id DROP NOT NULL;
ALTER TABLE client_rewards ADD COLUMN IF NOT EXISTS promo_code text NOT NULL DEFAULT '';
-- One redemption per client per promo code (referral accruals keep their
-- own source-booking uniqueness).
CREATE UNIQUE INDEX IF NOT EXISTS idx_client_rewards_promo_once
    ON client_rewards (tenant_id, client_id, promo_code) WHERE promo_code <> '';

-- Per-tenant outreach configuration (marketing frequency-cap override).
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS outreach_config jsonb NOT NULL DEFAULT '{}';
