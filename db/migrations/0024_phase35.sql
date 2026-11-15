-- Phase 35: scheduled outreach sends — a send recorded with a future
-- scheduled_for is held as status 'scheduled' and delivered by the hourly
-- sweep once due (send-time optimization, quiet hours, drip sequences).
-- The status CHECK gains 'scheduled'.

ALTER TABLE outreach_sends DROP CONSTRAINT IF EXISTS outreach_sends_status_check;
ALTER TABLE outreach_sends
    ADD CONSTRAINT outreach_sends_status_check
    CHECK (status IN ('scheduled','sent','held','failed'));

ALTER TABLE outreach_sends
    ADD COLUMN IF NOT EXISTS scheduled_for timestamptz;

-- Due-send sweep: scheduled rows whose time has come, per tenant.
CREATE INDEX IF NOT EXISTS idx_outreach_due
    ON outreach_sends (tenant_id, scheduled_for)
    WHERE scheduled_for IS NOT NULL;
