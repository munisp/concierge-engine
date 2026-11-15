-- Phase 42: WhatsApp hygiene. Receipts arrive keyed by the channel message
-- id (wamid) — correlating a failed delivery back to the outreach send (and
-- therefore the client) needs a lookup by external_ref.
CREATE INDEX IF NOT EXISTS idx_outreach_sends_ref
    ON outreach_sends (tenant_id, external_ref)
    WHERE external_ref <> '';
