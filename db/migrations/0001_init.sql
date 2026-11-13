-- Concierge Engine · Phase 1 schema
-- Multi-tenant: shared database, row-level security on every tenant table.
-- Tenant context is set by the service layer: SELECT set_config('app.tenant_id', ...)
-- and enforced by RLS policies below (verified by the isolation test suite).

BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ---------------------------------------------------------------- tenants
CREATE TABLE tenants (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug          text NOT NULL UNIQUE,
    name          text NOT NULL,
    archetype     text NOT NULL CHECK (archetype IN ('flagship','smb','new_entrant')),
    brand         jsonb NOT NULL DEFAULT '{}',      -- domains, colors, logos, doc templates
    pricing_rules jsonb NOT NULL DEFAULT '{}',      -- tenant pricing rules (quote engine input)
    sla_config    jsonb NOT NULL DEFAULT '{}',      -- e.g. quote SLA seconds (Taylor: 3600)
    autonomy_dial smallint NOT NULL DEFAULT 0 CHECK (autonomy_dial BETWEEN 0 AND 3),
    tier2_limit_minor bigint NOT NULL DEFAULT 50000, -- auto-approve ceiling for TIER-2 actions
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tenant_users (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    subject    text NOT NULL,        -- Keycloak sub
    role       text NOT NULL CHECK (role IN ('owner','admin','concierge','dispatcher','chauffeur','viewer')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, subject)
);

-- ------------------------------------------------------- identity & CRM (M6)
CREATE TABLE clients (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id),
    full_name   text,
    email       text,
    phone       text,                -- identity anchor for WhatsApp flows
    org_id      uuid,                -- household / corporate tree
    vip_flags   text[] NOT NULL DEFAULT '{}',  -- executive | vip | government
    loyalty_tier text,
    kyc_status  text NOT NULL DEFAULT 'none',
    consent     jsonb NOT NULL DEFAULT '{}',   -- purpose-bound consent records
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX clients_tenant_phone ON clients (tenant_id, phone);
CREATE INDEX clients_tenant_email ON clients (tenant_id, lower(email));

CREATE TABLE preferences (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenants(id),
    client_id     uuid NOT NULL REFERENCES clients(id),
    key           text NOT NULL,      -- vehicle_class | route | amenities | privacy ...
    value         text NOT NULL,
    consent_scope text NOT NULL DEFAULT 'service',
    source_quote  text,               -- provenance: the conversation line it came from
    recorded_by   text NOT NULL,      -- ai | staff:<id>
    recorded_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, client_id, key)
);

-- ----------------------------------------------------- catalog & booking (M3)
CREATE TABLE catalog_services (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id),
    vertical    text NOT NULL,
    name        text NOT NULL,
    base_minor  bigint NOT NULL,
    currency    char(3) NOT NULL,
    per_km_minor bigint NOT NULL DEFAULT 0,
    per_hour_minor bigint NOT NULL DEFAULT 0,
    auto_quotable boolean NOT NULL DEFAULT true,
    active      boolean NOT NULL DEFAULT true
);

CREATE TABLE service_requests (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES tenants(id),
    client_id       uuid NOT NULL REFERENCES clients(id),
    conversation_id uuid,
    state           text NOT NULL DEFAULT 'NEW',
    version         int NOT NULL DEFAULT 1,
    idempotency_key text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, idempotency_key)
);

CREATE TABLE service_items (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid NOT NULL REFERENCES tenants(id),
    request_id   uuid NOT NULL REFERENCES service_requests(id),
    vertical     text NOT NULL,
    payload      jsonb NOT NULL       -- addresses, times, pax, vehicle class, attrs
);

CREATE TABLE quotes (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid NOT NULL REFERENCES tenants(id),
    request_id   uuid NOT NULL REFERENCES service_requests(id),
    version      int NOT NULL DEFAULT 1,
    origin       text NOT NULL CHECK (origin IN ('INSTANT','HUMAN')),
    total_minor  bigint NOT NULL,
    deposit_minor bigint NOT NULL DEFAULT 0,
    currency     char(3) NOT NULL,
    lines        jsonb NOT NULL,
    expires_at   timestamptz,
    policy_verdict_id text,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE bookings (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id),
    request_id  uuid NOT NULL REFERENCES service_requests(id),
    quote_id    uuid NOT NULL REFERENCES quotes(id),
    state       text NOT NULL DEFAULT 'CONFIRMED',
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- ------------------------------------------------------------- dispatch (M4)
CREATE TABLE fleet_resources (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    kind       text NOT NULL,          -- vehicle | helicopter | vessel
    label      text NOT NULL,          -- "S-Class · AM-123-X"
    partner_id uuid,                   -- NULL = own fleet
    quality_score numeric(4,3) NOT NULL DEFAULT 1.0,
    active     boolean NOT NULL DEFAULT true
);

CREATE TABLE crew_members (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id),
    full_name   text NOT NULL,
    certificates jsonb NOT NULL DEFAULT '[]',
    quality_score numeric(4,3) NOT NULL DEFAULT 1.0,
    active      boolean NOT NULL DEFAULT true
);

CREATE TABLE assignments (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id),
    booking_id  uuid NOT NULL REFERENCES bookings(id),
    fleet_resource_id uuid REFERENCES fleet_resources(id),
    crew_member_id    uuid REFERENCES crew_members(id),
    partner_id  uuid,
    status      text NOT NULL DEFAULT 'ASSIGNED',
    sla_deadline timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE geostamps (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenants(id),
    assignment_id uuid NOT NULL REFERENCES assignments(id),
    kind          text NOT NULL,       -- pickup_arrival | pob | dropoff | pod
    lat           double precision NOT NULL,
    lng           double precision NOT NULL,
    accuracy_m    double precision,
    at            timestamptz NOT NULL DEFAULT now()
);

-- ------------------------------------------------------------- payments (M5)
CREATE TABLE payment_intents (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES tenants(id),
    booking_id      uuid REFERENCES bookings(id),
    quote_id        uuid REFERENCES quotes(id),
    mode            text NOT NULL,     -- FULL | DEPOSIT_BALANCE | PREAUTH_CAPTURE | MILESTONE | INVOICE
    amount_minor    bigint NOT NULL,
    currency        char(3) NOT NULL,
    psp             text NOT NULL DEFAULT 'stripe',
    psp_ref         text,
    hosted_url      text,
    state           text NOT NULL DEFAULT 'CREATED',  -- CREATED|LINK_SENT|PAID|CAPTURED|FAILED|REFUNDED
    idempotency_key text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, idempotency_key)
);

-- Business-state mirror of TigerBeetle postings (money truth lives in TB;
-- this table is the reconciled projection for queries and dashboards).
CREATE TABLE ledger_postings (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES tenants(id),
    booking_id      uuid,
    debit_code      text NOT NULL,
    credit_code     text NOT NULL,
    amount_minor    bigint NOT NULL,
    currency        char(3) NOT NULL,
    tb_transfer_id  text,              -- TigerBeetle transfer id (128-bit, hex)
    idempotency_key text NOT NULL UNIQUE,
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- -------------------------------------------------------- conversations (M2)
CREATE TABLE conversations (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenants(id),
    client_id     uuid REFERENCES clients(id),
    channel       text NOT NULL,
    state         text NOT NULL DEFAULT 'NEW',
    external_ref  text,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE messages (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         uuid NOT NULL REFERENCES tenants(id),
    conversation_id   uuid NOT NULL REFERENCES conversations(id),
    author            text NOT NULL,   -- CLIENT | AI | HUMAN_STAFF | SYSTEM
    body              text NOT NULL,   -- PII-redacted copy for inference/audit
    raw_ref           text,            -- encrypted raw store pointer
    policy_verdict_id text,
    sent_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX messages_conversation ON messages (conversation_id, sent_at);

-- HITL queues (AI spec §5.3): TIER-2 samples and TIER-3 approvals.
CREATE TABLE handoff_queue (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES tenants(id),
    conversation_id uuid NOT NULL REFERENCES conversations(id),
    trigger_kind    text NOT NULL,     -- VALUE | CONFIDENCE | SENTIMENT | POLICY | SLA
    reason          text NOT NULL,
    brief           jsonb NOT NULL,    -- structured handoff brief (<5s render)
    pending_action  jsonb,             -- drafted action awaiting approve/edit/reject
    status          text NOT NULL DEFAULT 'OPEN',  -- OPEN | APPROVED | EDITED | REJECTED | RESOLVED
    owner           text,
    sla_deadline    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    resolved_at     timestamptz
);

-- ----------------------------------------------------------- outbox (events)
-- Outbox pattern (Master Spec §9): domain events committed with the business
-- transaction, forwarded to Kafka by the outbox forwarder.
CREATE TABLE outbox (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    uuid NOT NULL,
    aggregate    text NOT NULL,        -- service_request | quote | booking | payment | message ...
    aggregate_id text NOT NULL,
    event_type   text NOT NULL,        -- quote.sent, payment.captured, chauffeur.assigned ...
    payload      jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);
CREATE INDEX outbox_unpublished ON outbox (id) WHERE published_at IS NULL;

-- ------------------------------------------------------ attribution spine (M1)
CREATE TABLE attribution_events (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id  uuid NOT NULL,
    kind       text NOT NULL,          -- ad_click | page_view | conversation_started | booking | revenue
    channel    text,                   -- seo | whatsapp | referral | social
    campaign   text,
    client_id  uuid,
    request_id uuid,
    value_minor bigint,
    at         timestamptz NOT NULL DEFAULT now()
);

-- =============================================================== RLS policies
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY[
    'tenant_users','clients','preferences','catalog_services','service_requests',
    'service_items','quotes','bookings','fleet_resources','crew_members',
    'assignments','geostamps','payment_intents','ledger_postings',
    'conversations','messages','handoff_queue','outbox','attribution_events']
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY;', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY;', t);
    EXECUTE format(
      'CREATE POLICY tenant_isolation ON %I USING (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid)
       WITH CHECK (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid);', t);
  END LOOP;
END $$;

-- Platform services (outbox forwarder, analytics) read cross-tenant through
-- an explicit opt-in GUC; tenant connections never set it.
CREATE POLICY platform_reader ON outbox
    USING (current_setting('app.platform_reader', true) = 'on');
CREATE POLICY platform_reader ON attribution_events
    USING (current_setting('app.platform_reader', true) = 'on');

COMMIT;
