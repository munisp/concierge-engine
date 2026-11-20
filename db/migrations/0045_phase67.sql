-- Phase 67: folio aging & dunning. The reminders ledger lives ON the
-- folio row as jsonb — every payment reminder (or honestly recorded
-- skip) is durable evidence, and the escalation cadence is derived from
-- it, never stored twice. Payment terms ride in tenants.pricing_rules
-- (folio_terms_days, 0 = 14-day platform default) — no tenants change.

ALTER TABLE partner_folios ADD COLUMN IF NOT EXISTS reminders jsonb NOT NULL DEFAULT '[]';
