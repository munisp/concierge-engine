-- Phase 28: SEO optimization wave — tenant site_config jsonb.
-- Holds custom_domains (host-based tenant resolution), google_verification
-- (Search Console meta), locale (hreflang/html lang), social_links (sameAs).
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS site_config jsonb NOT NULL DEFAULT '{}';
