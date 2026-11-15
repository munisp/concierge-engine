-- Phase 41: email list hygiene. A hard bounce means the address is dead —
-- re-mailing it is the single biggest driver of the bounce rate the
-- Phase 40 deliverability guard measures. Suppression is permanent until
-- an operator removes it, and it holds BOTH lanes: marketing obviously,
-- and service too — a booking confirmation sent to a dead address delivers
-- nothing but reputation damage.
--
-- email_suppressions: one row per suppressed address per tenant. Idempotent
-- by (tenant_id, email) — re-suppressing is a no-op replay. reason:
-- hard_bounce (ESP webhook) | complaint | manual (operator).

CREATE TABLE IF NOT EXISTS email_suppressions (
    id          uuid PRIMARY KEY,
    tenant_id   uuid NOT NULL REFERENCES tenants(id),
    email       text NOT NULL,
    reason      text NOT NULL CHECK (reason IN ('hard_bounce', 'complaint', 'manual')),
    source_ref  text NOT NULL DEFAULT '',   -- ESP message ref / operator note
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, email)
);

ALTER TABLE email_suppressions ENABLE ROW LEVEL SECURITY;
ALTER TABLE email_suppressions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON email_suppressions
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
