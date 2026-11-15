"""HMAC tracking tokens for email open/click instrumentation.

Same shape as core-api capability tokens (track/, quote/, unsub/ families):
base64url(payload) + "." + hex(hmac_sha256(secret, payload)). The payload
carries what the attribution event needs — kind, tenant, client, send ref —
so the tracking endpoints are stateless.
"""

from __future__ import annotations

import base64
import hashlib
import hmac


def mint(secret: str, kind: str, tenant_id: str, client_id: str, send_ref: str) -> str:
    payload = f"track/{kind}/{tenant_id}/{client_id}/{send_ref}"
    mac = hmac.new(secret.encode(), payload.encode(), hashlib.sha256).hexdigest()
    return base64.urlsafe_b64encode(payload.encode()).decode().rstrip("=") + "." + mac


def verify(secret: str, token: str, kind: str) -> dict[str, str]:
    """Verify and unpack. Raises ValueError on any malformation — callers
    turn that into a 403, never a guess."""
    try:
        raw, mac = token.split(".", 1)
        pad = "=" * (-len(raw) % 4)
        payload = base64.urlsafe_b64decode(raw + pad).decode()
    except Exception as exc:  # noqa: BLE001 — every decode failure is a 403
        raise ValueError("malformed token") from exc
    want = hmac.new(secret.encode(), payload.encode(), hashlib.sha256).hexdigest()
    if not hmac.compare_digest(want, mac):
        raise ValueError("bad signature")
    parts = payload.split("/", 4)
    if len(parts) != 5 or parts[0] != "track" or parts[1] != kind:
        raise ValueError("wrong token family")
    return {"kind": parts[1], "tenant_id": parts[2], "client_id": parts[3],
            "send_ref": parts[4]}
