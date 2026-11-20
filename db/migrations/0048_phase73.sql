-- Phase 73: credit notes & refunds on folios. The credit ledger lives on
-- the folio row next to reminders/payments/deliveries — the adjusted
-- total is derived from it, never stored twice.
ALTER TABLE partner_folios ADD COLUMN IF NOT EXISTS credits jsonb NOT NULL DEFAULT '[]';
