-- Phase 38: lead provenance — lawful contact basis for acquired lists.
--
-- GDPR-honest sourcing: list_source names the vendor/partner a row came
-- from; contact_basis states WHY the tenant may contact the prospect
-- (self_reported | partner_consent | b2b_legitimate_interest). The domain
-- refuses marketing consent on a B2B basis (Capture 422, Import strips it
-- with a notice) — a bought list earns consent at first reply, never at
-- import time. Pre-existing rows keep '' (unknown basis), which the
-- domain treats as organic on the next write.

ALTER TABLE leads ADD COLUMN IF NOT EXISTS list_source text NOT NULL DEFAULT '';
ALTER TABLE leads ADD COLUMN IF NOT EXISTS contact_basis text NOT NULL DEFAULT '';

ALTER TABLE leads DROP CONSTRAINT IF EXISTS leads_contact_basis_check;
ALTER TABLE leads ADD CONSTRAINT leads_contact_basis_check
    CHECK (contact_basis IN ('', 'self_reported', 'partner_consent', 'b2b_legitimate_interest'));

-- "Call list of B2B-sourced leads" / "exclude bought data" segments.
CREATE INDEX IF NOT EXISTS idx_leads_basis ON leads (tenant_id, contact_basis);
