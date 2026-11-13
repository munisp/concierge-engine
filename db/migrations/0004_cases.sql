-- Phase 4 schema: next-generation case management layered on the CRM.
-- cases carry client context + SLA clocks; case_tasks and case_notes are the
-- operational checklist and timeline. Standard FORCE + tenant_isolation RLS
-- (cases hold client PII context — no tenant-less exceptions).

CREATE TABLE IF NOT EXISTS cases (
    id              uuid PRIMARY KEY,
    tenant_id       uuid NOT NULL REFERENCES tenants(id),
    client_id       uuid,
    title           text NOT NULL,
    kind            text NOT NULL DEFAULT 'inquiry',   -- booking|incident|inquiry|vip_request
    priority        text NOT NULL DEFAULT 'P3',        -- P1..P4
    state           text NOT NULL DEFAULT 'OPEN',      -- OPEN|TRIAGED|IN_PROGRESS|WAITING_CLIENT|WAITING_PARTNER|RESOLVED|CLOSED
    owner_id        text NOT NULL DEFAULT '',
    summary         text NOT NULL DEFAULT '',
    triage_engine   text NOT NULL DEFAULT '',          -- ollama/qwen3:8b | rules | core-rules
    request_id      uuid,
    booking_id      uuid,
    conversation_id uuid,
    tags            jsonb NOT NULL DEFAULT '[]',
    sla_due_at      timestamptz,
    escalated       boolean NOT NULL DEFAULT false,
    idempotency_key text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    resolved_at     timestamptz,
    closed_at       timestamptz,
    UNIQUE (tenant_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS cases_state_idx ON cases (tenant_id, state);
CREATE INDEX IF NOT EXISTS cases_sla_idx ON cases (sla_due_at)
    WHERE state NOT IN ('RESOLVED', 'CLOSED');

CREATE TABLE IF NOT EXISTS case_tasks (
    id         uuid PRIMARY KEY,
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    case_id    uuid NOT NULL REFERENCES cases(id),
    title      text NOT NULL,
    done       boolean NOT NULL DEFAULT false,
    done_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS case_tasks_case_idx ON case_tasks (case_id);

CREATE TABLE IF NOT EXISTS case_notes (
    id         uuid PRIMARY KEY,
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    case_id    uuid NOT NULL REFERENCES cases(id),
    author     text NOT NULL DEFAULT 'system',   -- operator id | ai | system
    body       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS case_notes_case_idx ON case_notes (case_id);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['cases','case_tasks','case_notes']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I USING (tenant_id = nullif(current_setting(''app.tenant_id'', true), '''')::uuid)',
            t);
    END LOOP;
END $$;
