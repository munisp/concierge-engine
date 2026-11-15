-- Phase 36: lead engine — first-class pre-client prospects.
--
-- leads: one row per prospect in four kinds (person | corporate | partner
-- | event). The lead is the CRM/pipeline object; client_id links the
-- messaging identity resolved at capture, so all nurture (consent gates,
-- caps, quiet hours, sequences, STOP/START) flows through the outreach
-- engine unchanged.
--
-- Idempotency: partial UNIQUE on (tenant_id, idempotency_key) — public
-- captures without a key never collide, keyed captures replay safely.
-- Dedup across unkeyed captures is a domain concern (phone/email match on
-- open leads), not a constraint — a repeat inquiry is signal, not an error.

CREATE TABLE IF NOT EXISTS leads (
    id                   uuid PRIMARY KEY,
    tenant_id            uuid NOT NULL REFERENCES tenants(id),
    kind                 text NOT NULL CHECK (kind IN ('person','corporate','partner','event')),
    name                 text NOT NULL DEFAULT '',
    phone                text NOT NULL DEFAULT '',
    email                text NOT NULL DEFAULT '',
    company              text NOT NULL DEFAULT '',
    event_type           text NOT NULL DEFAULT '',
    event_date           timestamptz,
    headcount            int NOT NULL DEFAULT 0,
    venue                text NOT NULL DEFAULT '',
    verticals            jsonb NOT NULL DEFAULT '[]',
    notes                text NOT NULL DEFAULT '',
    source               text NOT NULL DEFAULT '',
    channel              text NOT NULL DEFAULT '',
    campaign             text NOT NULL DEFAULT '',
    referral_code        text NOT NULL DEFAULT '',
    status               text NOT NULL DEFAULT 'NEW'
                             CHECK (status IN ('NEW','CONTACTED','QUALIFIED','CONVERTED','LOST')),
    score                int NOT NULL DEFAULT 0,
    score_reasons        jsonb NOT NULL DEFAULT '[]',
    lost_reason          text NOT NULL DEFAULT '',
    consent_marketing    boolean NOT NULL DEFAULT false,
    client_id            uuid,
    converted_client_id  uuid,
    converted_partner_id uuid,
    converted_account_id uuid,
    sla_alerted_at       timestamptz,
    idempotency_key      text NOT NULL DEFAULT '',
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE leads ENABLE ROW LEVEL SECURITY;
ALTER TABLE leads FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON leads
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE UNIQUE INDEX IF NOT EXISTS leads_tenant_idem_key
    ON leads (tenant_id, idempotency_key) WHERE idempotency_key <> '';
CREATE INDEX IF NOT EXISTS idx_leads_tenant_status ON leads (tenant_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_leads_tenant_kind ON leads (tenant_id, kind);
CREATE INDEX IF NOT EXISTS idx_leads_client ON leads (tenant_id, client_id) WHERE client_id IS NOT NULL;
-- Speed-to-lead sweep: untouched NEW leads, not yet alerted.
CREATE INDEX IF NOT EXISTS idx_leads_sla ON leads (tenant_id, created_at)
    WHERE status = 'NEW' AND sla_alerted_at IS NULL;
