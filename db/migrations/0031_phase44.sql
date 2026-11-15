-- Phase 44: consent evidence ledger. GDPR Art. 7 puts the burden of
-- PROOF on the controller: "the controller shall be able to demonstrate
-- that the data subject has consented". Until now consent was a mutable
-- map on the client row — the current state, with no history of when,
-- where, or via what mechanism each scope was granted or revoked.
--
-- consent_events: append-only evidence rows. One row per ACTUAL change
-- (the domain records only when the value flips or appears — a replayed
-- PUT with the same value adds nothing). source names the surface:
-- registration_default | console | api | keyword | unsub | reopt |
-- lead_capture | lead_repeat_touch | esp_complaint | dsar_erasure.
-- detail carries surface-specific context (the keyword text, the note an
-- operator typed). Rows survive erasure: they reference only the
-- pseudonymous client_id, like the financial ledger.

CREATE TABLE IF NOT EXISTS consent_events (
    id          uuid PRIMARY KEY,
    tenant_id   uuid NOT NULL REFERENCES tenants(id),
    client_id   uuid NOT NULL REFERENCES clients(id),
    scope       text NOT NULL,
    granted     boolean NOT NULL,
    source      text NOT NULL,
    detail      text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_consent_events_client
    ON consent_events (tenant_id, client_id, created_at DESC);

ALTER TABLE consent_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE consent_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON consent_events
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
