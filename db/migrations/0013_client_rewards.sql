-- Phase 18: referrer ledger rewards + reward-aware payment intents.
-- client_rewards: referral credit accrued to a referrer when their referee's
-- first booking confirms; redeemed oldest-first against the referrer's next
-- captured payment. payment_intents.reward_applied_minor: portion of the
-- client obligation covered by rewards (PSP charges amount - reward).

CREATE TABLE IF NOT EXISTS client_rewards (
    id                uuid PRIMARY KEY,
    tenant_id         uuid NOT NULL REFERENCES tenants(id),
    client_id         uuid NOT NULL REFERENCES clients(id),   -- the referrer
    source_booking_id uuid NOT NULL REFERENCES bookings(id),  -- the referee's first booking
    kind              text NOT NULL DEFAULT 'referral_reward',
    amount_minor      bigint NOT NULL CHECK (amount_minor > 0),
    currency          char(3) NOT NULL,
    state             text NOT NULL DEFAULT 'ACCRUED' CHECK (state IN ('ACCRUED','REDEEMED')),
    redeemed_intent_id text,                                   -- text like payment_intents.psp_ref
    created_at        timestamptz NOT NULL DEFAULT now(),
    redeemed_at       timestamptz,
    -- Idempotent accrual: a replayed conversion never double-credits.
    UNIQUE (tenant_id, client_id, source_booking_id)
);

-- Balance + oldest-first redemption lookups.
CREATE INDEX IF NOT EXISTS idx_client_rewards_balance
    ON client_rewards (tenant_id, client_id, state, created_at);

ALTER TABLE client_rewards ENABLE ROW LEVEL SECURITY;
ALTER TABLE client_rewards FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON client_rewards
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE payment_intents ADD COLUMN IF NOT EXISTS reward_applied_minor bigint NOT NULL DEFAULT 0;
