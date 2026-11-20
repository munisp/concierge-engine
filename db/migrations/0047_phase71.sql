-- Phase 71: statement delivery. The delivery ledger lives on the folio
-- row (same pattern as the Phase 67 reminders / Phase 70 payments
-- ledgers) — every statement send is verbatim evidence, derived state
-- never stored twice. partners.email is the billing contact used when
-- the partner has no WhatsApp number on file (email lane, Phase 72
-- reuses it for dunning).
ALTER TABLE partner_folios ADD COLUMN IF NOT EXISTS deliveries jsonb NOT NULL DEFAULT '[]';
ALTER TABLE partners ADD COLUMN IF NOT EXISTS email text NOT NULL DEFAULT '';
