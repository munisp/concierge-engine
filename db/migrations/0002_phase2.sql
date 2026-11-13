-- Phase 2 schema: partner network, corporate accounts, payouts (Mojaloop
-- saga state), corporate invoicing, and the sensitive-read audit log
-- (Master Spec sections 11.3, 13). Same RLS posture as 0001: FORCE +
-- tenant_isolation policy driven by the app.tenant_id GUC.

CREATE TABLE IF NOT EXISTS partners (
    id              uuid PRIMARY KEY,
    tenant_id       uuid NOT NULL REFERENCES tenants(id),
    name            text NOT NULL,
    verticals       jsonb NOT NULL DEFAULT '[]',
    markets         jsonb NOT NULL DEFAULT '[]',
    status          text NOT NULL DEFAULT 'PENDING',  -- PENDING | VETTED | SUSPENDED
    quality_score   double precision NOT NULL DEFAULT 1.0,
    payout_rail     text NOT NULL DEFAULT 'MANUAL',   -- MOJALOOP | MANUAL
    payout_ref      text NOT NULL DEFAULT '',         -- scheme FSPIOP id / bank ref
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS corporate_accounts (
    id                 uuid PRIMARY KEY,
    tenant_id          uuid NOT NULL REFERENCES tenants(id),
    name               text NOT NULL,
    billing_email      text NOT NULL DEFAULT '',
    payment_terms_days int NOT NULL DEFAULT 30,
    credit_limit_minor bigint NOT NULL DEFAULT 0,
    currency           text NOT NULL DEFAULT 'EUR',
    created_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS payouts (
    id              uuid PRIMARY KEY,
    tenant_id       uuid NOT NULL REFERENCES tenants(id),
    partner_id      uuid NOT NULL,
    booking_id      uuid,
    amount_minor    bigint NOT NULL,
    currency        text NOT NULL,
    rail            text NOT NULL DEFAULT 'MANUAL',
    state           text NOT NULL DEFAULT 'PENDING',  -- PENDING|QUOTED|IN_FLIGHT|SETTLED|COMPENSATED|FAILED
    quote_id        text NOT NULL DEFAULT '',
    transfer_id     text NOT NULL DEFAULT '',
    fx_rate         double precision NOT NULL DEFAULT 0,
    receive_minor   bigint NOT NULL DEFAULT 0,
    fees_minor      bigint NOT NULL DEFAULT 0,
    idempotency_key text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS payouts_transfer_idx ON payouts (tenant_id, transfer_id);

CREATE TABLE IF NOT EXISTS corporate_invoices (
    id              uuid PRIMARY KEY,
    tenant_id       uuid NOT NULL REFERENCES tenants(id),
    account_id      uuid NOT NULL REFERENCES corporate_accounts(id),
    period_start    timestamptz NOT NULL,
    period_end      timestamptz NOT NULL,
    total_minor     bigint NOT NULL,
    currency        text NOT NULL,
    state           text NOT NULL DEFAULT 'ISSUED',   -- ISSUED | PAID | VOID
    idempotency_key text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, idempotency_key)
);

-- Sensitive-read audit log (Master Spec section 13: access audit logs on
-- every sensitive read). Append-only by policy: no UPDATE/DELETE grants.
CREATE TABLE IF NOT EXISTS audit_log (
    id          bigserial PRIMARY KEY,
    tenant_id   uuid NOT NULL,
    actor       text NOT NULL,
    action      text NOT NULL,      -- client.read | preferences.read | ...
    resource    text NOT NULL,
    resource_id text NOT NULL,
    detail      jsonb NOT NULL DEFAULT '{}',
    at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS audit_log_tenant_idx ON audit_log (tenant_id, at DESC);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'partners','corporate_accounts','payouts','corporate_invoices','audit_log'
    ]
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I USING (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid)',
            t);
    END LOOP;
END $$;
