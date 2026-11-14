-- Phase 15: client profile enrichment for CRM-360 — locale + timezone
-- (global clientele: localized checkout, outreach timing, display).

ALTER TABLE clients ADD COLUMN IF NOT EXISTS locale   text NOT NULL DEFAULT '';
ALTER TABLE clients ADD COLUMN IF NOT EXISTS timezone text NOT NULL DEFAULT '';
