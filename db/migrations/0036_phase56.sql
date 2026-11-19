-- Phase 56: partner integration layer. Partners gain a commission rate on
-- partner-routed confirmed bookings (accrued into the existing payout
-- saga); API keys and webhook subscriptions gain an optional partner scope
-- so operators (Mr Travel, The Little Orange, Younique Tailor Tours) can
-- integrate programmatically without tenant-wide credentials.
ALTER TABLE partners ADD COLUMN IF NOT EXISTS commission_pct double precision NOT NULL DEFAULT 0;
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS partner_id uuid;
ALTER TABLE webhook_subscriptions ADD COLUMN IF NOT EXISTS partner_id uuid;
