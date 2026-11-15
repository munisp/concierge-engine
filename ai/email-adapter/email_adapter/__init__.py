"""Email channel adapter (Phase 39).

The email channel-port implementation: outbound SMTP delivery with the
compliance envelope core-api mandates (unsubscribe footer already in the
body, List-Unsubscribe headers here), open/click tracking on the adapter's
own public endpoints, ESP event webhooks (deliveries/bounces/complaints),
and inbound email -> conversation/intake/agent bridging.

Honest degrade: without CE_SMTP_HOST the adapter runs in sandbox mode —
sends are recorded in the sandbox outbox and reported as mode "sandbox",
never silently dropped.
"""
