-- Phase 16: GDPR Art. 17 / CCPA erasure tombstone. The client row survives
-- anonymized so financial records (bookings, payment intents, ledger) keep a
-- pseudonymous anchor — erasure scrubs identity, not accounting truth.
ALTER TABLE clients ADD COLUMN IF NOT EXISTS erased_at timestamptz NULL;
CREATE INDEX IF NOT EXISTS clients_erased_idx ON clients (tenant_id) WHERE erased_at IS NOT NULL;
