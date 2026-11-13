-- Phase 3 schema (Master Spec §14): public API keys, webhook
-- subscriptions/deliveries (webhooks marketplace), cross-tenant referrals
-- (network effect), tenant residency + referral opt-in columns.
--
-- RLS posture: same FORCE + tenant_isolation pattern as 0001/0002 for
-- tenant-scoped tables. Two documented exceptions:
--   - api_keys: the prefix lookup IS tenant resolution, so the authenticating
--     path must read without a tenant GUC. The policy therefore also admits
--     connections with no tenant set (current_setting is NULL) — the table
--     holds only hashes, never usable secrets.
--   - webhook_deliveries: the dispatcher scans across tenants; read-without-
--     tenant is allowed, writes always carry tenant_id.
--   - referrals: visible to BOTH sides (referring + receiving tenant).

ALTER TABLE tenants ADD COLUMN IF NOT EXISTS region text NOT NULL DEFAULT 'EU';
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS accepts_referrals boolean NOT NULL DEFAULT false;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS referral_fee_pct double precision NOT NULL DEFAULT 0.10;

CREATE TABLE IF NOT EXISTS api_keys (
    id           uuid PRIMARY KEY,
    tenant_id    uuid NOT NULL REFERENCES tenants(id),
    name         text NOT NULL,
    prefix       text NOT NULL UNIQUE,           -- "cek_" + 8; index for auth lookup
    secret_hash  text NOT NULL,                  -- sha256 hex; the secret is never stored
    scopes       jsonb NOT NULL DEFAULT '[]',
    revoked      boolean NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz
);

CREATE TABLE IF NOT EXISTS webhook_subscriptions (
    id         uuid PRIMARY KEY,
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    url        text NOT NULL,
    events     jsonb NOT NULL DEFAULT '[]',      -- ["booking.*", "quote.created"]; ["*"] = all
    secret     text NOT NULL,                    -- delivery HMAC secret, returned once
    active     boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id              uuid PRIMARY KEY,
    tenant_id       uuid NOT NULL REFERENCES tenants(id),
    subscription_id uuid NOT NULL REFERENCES webhook_subscriptions(id),
    event_id        text NOT NULL,
    event_kind      text NOT NULL,
    payload         text NOT NULL,
    attempts        int NOT NULL DEFAULT 0,
    max_attempts    int NOT NULL DEFAULT 8,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    delivered_at    timestamptz,
    last_status     int,
    last_error      text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS webhook_deliveries_due_idx
    ON webhook_deliveries (next_attempt_at) WHERE delivered_at IS NULL;

CREATE TABLE IF NOT EXISTS referrals (
    id                   uuid PRIMARY KEY,
    referring_tenant_id  uuid NOT NULL REFERENCES tenants(id),
    receiving_tenant_id  uuid NOT NULL REFERENCES tenants(id),
    client_name          text NOT NULL DEFAULT '',
    client_contact       text NOT NULL DEFAULT '',
    request_summary      text NOT NULL DEFAULT '',
    est_value_minor      bigint NOT NULL DEFAULT 0,
    currency             text NOT NULL DEFAULT 'EUR',
    fee_pct              double precision NOT NULL DEFAULT 0.10,
    state                text NOT NULL DEFAULT 'OFFERED',  -- OFFERED|ACCEPTED|DECLINED|FULFILLED|SETTLED
    request_id           uuid,
    booking_id           uuid,
    commission_minor     bigint NOT NULL DEFAULT 0,
    idempotency_key      text NOT NULL DEFAULT '',
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (referring_tenant_id, idempotency_key)
);

-- Standard tenant isolation for keys, subscriptions, deliveries.
DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['api_keys','webhook_subscriptions','webhook_deliveries']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I USING (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid)',
            t);
    END LOOP;
END $$;

-- Auth-path exceptions (documented in the header): prefix lookup and the
-- dispatcher scan run without a tenant GUC.
CREATE POLICY apikey_auth_lookup ON api_keys
    FOR SELECT USING (current_setting('app.tenant_id', true) IS NULL
                      OR current_setting('app.tenant_id', true) = '');
CREATE POLICY deliveries_dispatch_scan ON webhook_deliveries
    FOR SELECT USING (current_setting('app.tenant_id', true) IS NULL
                      OR current_setting('app.tenant_id', true) = '');

-- Referrals: both sides can read; only the involved tenants can write.
ALTER TABLE referrals ENABLE ROW LEVEL SECURITY;
ALTER TABLE referrals FORCE ROW LEVEL SECURITY;
CREATE POLICY referral_bilateral ON referrals USING (
    referring_tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid
    OR receiving_tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid
);
