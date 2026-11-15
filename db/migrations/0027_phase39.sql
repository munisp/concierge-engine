-- Phase 39: email outreach channel. The send's subject persists so the
-- hourly sweep can deliver scheduled email sends without losing it.
ALTER TABLE outreach_sends ADD COLUMN IF NOT EXISTS subject text NOT NULL DEFAULT '';
