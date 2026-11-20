-- Phase 65: partner rate cards — negotiated per-channel pricing applied at
-- quoting time to demand the partner sourced. One card per
-- (tenant, partner, vertical).

CREATE TABLE IF NOT EXISTS partner_rate_cards (
    tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    partner_id uuid NOT NULL REFERENCES partners(id) ON DELETE CASCADE,
    vertical   text NOT NULL,
    kind       text NOT NULL,
    value      bigint NOT NULL,
    currency   text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, partner_id, vertical),
    CONSTRAINT partner_rate_cards_kind_check
        CHECK (kind IN ('pct_discount', 'pct_markup', 'fixed_total')),
    CONSTRAINT partner_rate_cards_value_check
        CHECK ((kind IN ('pct_discount', 'pct_markup') AND value BETWEEN 1 AND 50)
            OR (kind = 'fixed_total' AND value > 0)),
    CONSTRAINT partner_rate_cards_fixed_currency_check
        CHECK (kind <> 'fixed_total' OR currency IS NOT NULL)
);

ALTER TABLE partner_rate_cards ENABLE ROW LEVEL SECURITY;
ALTER TABLE partner_rate_cards FORCE ROW LEVEL SECURITY;

CREATE POLICY partner_rate_cards_tenant_isolation ON partner_rate_cards
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

-- Quote-time lookup: cards for the sourcing partner of a request.
CREATE INDEX IF NOT EXISTS partner_rate_cards_partner_idx
    ON partner_rate_cards (tenant_id, partner_id);
