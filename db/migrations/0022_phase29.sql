-- Phase 29: multi-locale site engine — per-locale names for catalog
-- services and service areas. Tenant-level locales/brand i18n ride on the
-- existing tenants.site_config jsonb (Phase 28).
ALTER TABLE catalog_services ADD COLUMN IF NOT EXISTS name_i18n jsonb NOT NULL DEFAULT '{}';
ALTER TABLE catalog_services ADD COLUMN IF NOT EXISTS desc_i18n jsonb NOT NULL DEFAULT '{}';
ALTER TABLE service_areas ADD COLUMN IF NOT EXISTS name_i18n jsonb NOT NULL DEFAULT '{}';
