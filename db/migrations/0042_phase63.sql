-- Phase 63: demand channels. Partners gain a channel kind; requests and
-- bookings carry the source of the demand (direct by default) plus the
-- sourcing partner, so commission accrual and margin analytics attribute
-- every booking to the channel that brought it.

ALTER TABLE partners
    ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'tour_operator';
ALTER TABLE partners
    DROP CONSTRAINT IF EXISTS partners_kind_check;
ALTER TABLE partners
    ADD CONSTRAINT partners_kind_check
    CHECK (kind IN ('hotel', 'travel_agency', 'transport_company', 'tour_operator', 'other'));

ALTER TABLE service_requests
    ADD COLUMN IF NOT EXISTS source_kind text NOT NULL DEFAULT 'direct',
    ADD COLUMN IF NOT EXISTS source_partner_id uuid REFERENCES partners(id) ON DELETE SET NULL;
ALTER TABLE service_requests
    DROP CONSTRAINT IF EXISTS service_requests_source_kind_check;
ALTER TABLE service_requests
    ADD CONSTRAINT service_requests_source_kind_check
    CHECK (source_kind IN ('direct', 'hotel', 'travel_agency', 'transport_company', 'tour_operator', 'other'));

ALTER TABLE bookings
    ADD COLUMN IF NOT EXISTS source_kind text NOT NULL DEFAULT 'direct',
    ADD COLUMN IF NOT EXISTS source_partner_id uuid REFERENCES partners(id) ON DELETE SET NULL;
ALTER TABLE bookings
    DROP CONSTRAINT IF EXISTS bookings_source_kind_check;
ALTER TABLE bookings
    ADD CONSTRAINT bookings_source_kind_check
    CHECK (source_kind IN ('direct', 'hotel', 'travel_agency', 'transport_company', 'tour_operator', 'other'));

-- Partner portal "my sourced demand" listing.
CREATE INDEX IF NOT EXISTS service_requests_source_partner_idx
    ON service_requests (tenant_id, source_partner_id, created_at DESC)
    WHERE source_partner_id IS NOT NULL;

-- Margin report: per-channel aggregation over the booking window.
CREATE INDEX IF NOT EXISTS bookings_source_kind_idx
    ON bookings (tenant_id, source_kind, created_at);
