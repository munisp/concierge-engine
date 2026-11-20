-- Phase 66: partner bill-to-desk folios — monthly consolidated settlement
-- for partner-sourced demand. The partner opts into desk billing; every
-- sourced CONFIRMED booking accrues its adjusted gross as a folio line;
-- the platform issues one folio per (partner, currency, period).

ALTER TABLE partners ADD COLUMN IF NOT EXISTS bill_to_desk boolean NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS partner_folios (
    id          uuid PRIMARY KEY,
    tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    partner_id  uuid NOT NULL REFERENCES partners(id) ON DELETE CASCADE,
    period      text NOT NULL,                -- YYYY-MM (UTC)
    currency    text NOT NULL,
    state       text NOT NULL DEFAULT 'OPEN',
    total_minor bigint NOT NULL DEFAULT 0,
    lines       jsonb NOT NULL DEFAULT '[]',
    created_at  timestamptz NOT NULL DEFAULT now(),
    issued_at   timestamptz,
    settled_at  timestamptz,
    CONSTRAINT partner_folios_state_check
        CHECK (state IN ('OPEN', 'ISSUED', 'SETTLED')),
    CONSTRAINT partner_folios_period_check
        CHECK (period ~ '^\d{4}-\d{2}$'),
    CONSTRAINT partner_folios_total_check
        CHECK (total_minor >= 0),
    -- One folio per partner per period per currency — accrual upserts.
    CONSTRAINT partner_folios_unique_period
        UNIQUE (tenant_id, partner_id, period, currency)
);

ALTER TABLE partner_folios ENABLE ROW LEVEL SECURITY;
ALTER TABLE partner_folios FORCE ROW LEVEL SECURITY;

CREATE POLICY partner_folios_tenant_isolation ON partner_folios
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

-- Operator surface: folios for one partner, newest period first.
CREATE INDEX IF NOT EXISTS partner_folios_partner_idx
    ON partner_folios (tenant_id, partner_id, period DESC);
