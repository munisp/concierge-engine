-- Phase 40: campaign economics. The funnel already tracks outcomes (sends,
-- conversions, attributed value); the spend ledger records what a campaign
-- COST, so cost-per-conversion and ROAS close the loop — "fund the next
-- event from evidence".
--
-- campaign_spend: one row per spend entry (ad buy, list purchase, event
-- sponsorship, influencer fee...). Idempotent by (tenant_id,
-- idempotency_key) — a retried POST replays to the original row and never
-- double-books cost. amount_minor is minor units (kobo/cents) with the
-- ISO-4217 currency beside it; mixed-currency campaigns are flagged by the
-- domain, never silently summed into one number.

CREATE TABLE IF NOT EXISTS campaign_spend (
    id              uuid PRIMARY KEY,
    tenant_id       uuid NOT NULL REFERENCES tenants(id),
    campaign        text NOT NULL,
    channel         text NOT NULL DEFAULT '',   -- paid_search | paid_social | ... (free text, advisory)
    amount_minor    bigint NOT NULL CHECK (amount_minor > 0),
    currency        char(3) NOT NULL,
    spent_on        date NOT NULL,
    note            text NOT NULL DEFAULT '',
    idempotency_key text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_campaign_spend_idem
    ON campaign_spend (tenant_id, idempotency_key) WHERE idempotency_key <> '';

-- Economics board: all spend for one campaign.
CREATE INDEX IF NOT EXISTS idx_campaign_spend_campaign
    ON campaign_spend (tenant_id, campaign);

ALTER TABLE campaign_spend ENABLE ROW LEVEL SECURITY;
ALTER TABLE campaign_spend FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON campaign_spend
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
