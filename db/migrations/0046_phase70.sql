-- Phase 70: partial payments against issued folios. The payment ledger
-- lives on the folio row (same pattern as the Phase 67 reminders
-- ledger) — paid/outstanding are derived from it, never stored twice.
ALTER TABLE partner_folios ADD COLUMN IF NOT EXISTS payments jsonb NOT NULL DEFAULT '[]';
