-- Phase 27: tenant onboarding journey + brownfield import + acquisition
-- innovation pack.
--
-- tenants.onboarding_config: journey state (track new|existing, skipped
-- step IDs). Step STATUSES are derived from real resources — only the
-- tenant's dismissals are stored.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS onboarding_config jsonb NOT NULL DEFAULT '{}';

-- promo_campaigns: gift vouchers (one-redemption codes a client hands to a
-- non-client; redemption stamps gift:<code> attribution) and competitor
-- switch offers (high-intent redemption alerts the operator roster).
ALTER TABLE promo_campaigns ADD COLUMN IF NOT EXISTS gift boolean NOT NULL DEFAULT false;
ALTER TABLE promo_campaigns ADD COLUMN IF NOT EXISTS switch_offer boolean NOT NULL DEFAULT false;

-- reviews: post-trip client reviews submitted through the track-token page.
-- publish_consent gates use on public SEO pages (aggregateRating) — feedback
-- by default, marketing material only by explicit consent. One review per
-- assignment.
CREATE TABLE IF NOT EXISTS reviews (
    id              uuid PRIMARY KEY,
    tenant_id       uuid NOT NULL REFERENCES tenants(id),
    client_id       uuid NOT NULL REFERENCES clients(id),
    assignment_id   uuid NOT NULL,
    rating          int NOT NULL CHECK (rating BETWEEN 1 AND 5),
    text            text NOT NULL DEFAULT '',
    publish_consent boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, assignment_id)
);

ALTER TABLE reviews ENABLE ROW LEVEL SECURITY;
ALTER TABLE reviews FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON reviews
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE INDEX IF NOT EXISTS idx_reviews_tenant_created ON reviews (tenant_id, created_at DESC);
