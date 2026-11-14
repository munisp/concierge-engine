-- Phase 21: webhook dual-secret rotation window, per-tenant operator
-- rosters, tenant-managed outreach message templates.

-- Dual-secret rotation: during the rotation window the dispatcher signs each
-- delivery with BOTH the current and previous secret (comma-separated
-- X-Concierge-Signature), so receivers can cut over without missing events.
ALTER TABLE webhook_subscriptions
    ADD COLUMN IF NOT EXISTS prev_secret text NOT NULL DEFAULT '';
ALTER TABLE webhook_subscriptions
    ADD COLUMN IF NOT EXISTS secret_rotated_at timestamptz;

-- Per-tenant operator roster: who gets SLA/failure WhatsApp alerts.
-- Empty = platform fallback (CE_OPERATOR_PHONE).
ALTER TABLE tenants
    ADD COLUMN IF NOT EXISTS operator_config jsonb NOT NULL DEFAULT '{}';

-- Tenant-managed outreach templates. Body supports {{client_name}} and
-- {{tenant_name}} substitutions rendered server-side at send time.
CREATE TABLE IF NOT EXISTS message_templates (
    id         uuid PRIMARY KEY,
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    name       text NOT NULL,
    purpose    text NOT NULL DEFAULT 'any'
               CHECK (purpose IN ('service', 'marketing', 'any')),
    body       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);
ALTER TABLE message_templates ENABLE ROW LEVEL SECURITY;
ALTER TABLE message_templates FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON message_templates
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
