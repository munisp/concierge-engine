-- Phase 57: partner PWA + WhatsApp offers. Partners gain a WhatsApp number
-- (E.164) so assignment offers and the portal magic link can reach the
-- operator's phone directly; empty = portal-link shared manually.
ALTER TABLE partners ADD COLUMN IF NOT EXISTS phone text NOT NULL DEFAULT '';
