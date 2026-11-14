-- Phase 22: scheduling v1 (assignment time windows + conflict detection)
-- and conversation threading.

-- Assignments gain the service window so dispatch can refuse double-booking.
-- NULL window = unknown schedule (pre-Phase-22 rows); conflict checks only
-- gate windowed assignments.
ALTER TABLE assignments ADD COLUMN IF NOT EXISTS start_at timestamptz;
ALTER TABLE assignments ADD COLUMN IF NOT EXISTS end_at   timestamptz;
CREATE INDEX IF NOT EXISTS assignments_resource_window_idx
    ON assignments (tenant_id, fleet_resource_id, start_at, end_at)
    WHERE status NOT IN ('COMPLETED_TRIP', 'CANCELLED_TRIP', 'NO_SHOW');

-- Tenant dispatch tuning: turnaround buffer between trips on the same
-- resource, default service duration when a request item has no hours.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS dispatch_config jsonb NOT NULL DEFAULT '{}';

-- Conversation threading: get-or-create the active thread per
-- (tenant, client, channel) instead of one conversation per message.
CREATE INDEX IF NOT EXISTS conversations_active_thread_idx
    ON conversations (tenant_id, client_id, channel, created_at DESC)
    WHERE state <> 'CLOSED';
