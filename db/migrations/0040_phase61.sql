-- Phase 61: journey orchestration — itinerary legs with scheduled AND
-- actual times. booking_id / linked_leg_id are nullable FKs: flight and
-- hotel legs have no platform booking, and only arrival-transfer ground
-- legs chain to a flight leg.
CREATE TABLE IF NOT EXISTS itinerary_legs (
    id              uuid PRIMARY KEY,
    tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    client_id       uuid NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    booking_id      uuid REFERENCES bookings(id) ON DELETE SET NULL,
    linked_leg_id   uuid REFERENCES itinerary_legs(id) ON DELETE SET NULL,
    kind            text NOT NULL CHECK (kind IN ('flight','hotel','ground','partner')),
    title           text NOT NULL DEFAULT '',
    ref             text NOT NULL DEFAULT '',
    flight_number   text NOT NULL DEFAULT '',
    origin          text NOT NULL DEFAULT '',
    destination     text NOT NULL DEFAULT '',
    sched_start     timestamptz NOT NULL,
    sched_end       timestamptz,
    actual_start    timestamptz,
    status          text NOT NULL DEFAULT 'SCHEDULED'
                    CHECK (status IN ('SCHEDULED','EN_ROUTE','DELAYED','ARRIVED','COMPLETED','CANCELLED')),
    delay_minutes   integer NOT NULL DEFAULT 0,
    source          text NOT NULL DEFAULT 'manual' CHECK (source IN ('live','manual','scheduled')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_itinerary_client ON itinerary_legs (tenant_id, client_id, sched_start);
CREATE INDEX IF NOT EXISTS idx_itinerary_booking ON itinerary_legs (tenant_id, booking_id);
CREATE INDEX IF NOT EXISTS idx_itinerary_watch ON itinerary_legs (tenant_id, kind, sched_start)
    WHERE kind = 'flight' AND status NOT IN ('ARRIVED','COMPLETED','CANCELLED');

ALTER TABLE itinerary_legs ENABLE ROW LEVEL SECURITY;
ALTER TABLE itinerary_legs FORCE ROW LEVEL SECURITY;
CREATE POLICY itinerary_legs_tenant_isolation ON itinerary_legs
    USING (tenant_id = current_setting('app.tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id')::uuid);
