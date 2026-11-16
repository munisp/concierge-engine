-- Phase 48: settlement & payout depth.
--
-- Three gaps closed in the Mojaloop payout lane (Phase 2 built the saga):
--   1. Quotes carried expires_at from the adapter but the core never
--      enforced it — an execution could ride a stale FX lock.
--   2. Every payout was its own scheme transfer — a partner with twelve
--      completed trips paid twelve scheme fees. Settlement batches net a
--      partner's pending payouts into ONE transfer.
--   3. A lost fulfil callback (adapter notify is fire-and-forget) left a
--      payout IN_FLIGHT forever; nothing in the core ever reclaimed it.
--      quote_expires_at / settled_at / batch_id on payouts support 1, 2
--      and the timeout sweep respectively.

CREATE TABLE IF NOT EXISTS settlement_batches (
    id               uuid PRIMARY KEY,
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    partner_id       uuid NOT NULL REFERENCES partners(id) ON DELETE CASCADE,
    currency         text NOT NULL,
    amount_minor     bigint NOT NULL,           -- sum of line payout amounts
    receive_minor    bigint NOT NULL DEFAULT 0, -- scheme-quoted receive
    fees_minor       bigint NOT NULL DEFAULT 0, -- scheme-quoted fees
    state            text NOT NULL DEFAULT 'OPEN'
                     CHECK (state IN ('OPEN', 'QUOTED', 'IN_FLIGHT', 'SETTLED', 'COMPENSATED', 'FAILED')),
    quote_id         text NOT NULL DEFAULT '',
    transfer_id      text NOT NULL DEFAULT '',
    quote_expires_at timestamptz,
    idempotency_key  text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS settlement_batches_idem_unique
    ON settlement_batches (tenant_id, idempotency_key)
    WHERE idempotency_key <> '';
CREATE INDEX IF NOT EXISTS idx_settlement_batches_tenant ON settlement_batches (tenant_id, state);
CREATE INDEX IF NOT EXISTS idx_settlement_batches_transfer ON settlement_batches (tenant_id, transfer_id);

ALTER TABLE settlement_batches ENABLE ROW LEVEL SECURITY;
ALTER TABLE settlement_batches FORCE ROW LEVEL SECURITY;
CREATE POLICY settlement_batches_tenant_isolation ON settlement_batches
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);

ALTER TABLE payouts ADD COLUMN IF NOT EXISTS quote_expires_at timestamptz;
ALTER TABLE payouts ADD COLUMN IF NOT EXISTS batch_id uuid REFERENCES settlement_batches(id);
ALTER TABLE payouts ADD COLUMN IF NOT EXISTS settled_at timestamptz;
CREATE INDEX IF NOT EXISTS idx_payouts_batch ON payouts (tenant_id, batch_id) WHERE batch_id IS NOT NULL;
