"""Meta Cloud API channel adapter (WhatsApp Booking Flow spec, Phase 3 path).

Direct Meta integration for flagship volume — bypasses BSP markup. Same
ChannelPort interface, so swapping is tenant-level config, not code.

Also owns webhook security: X-Hub-Signature-256 is HMAC-SHA256 of the raw
body with the app secret, compared in constant time. The GET verification
handshake (hub.challenge) is validated against the tenant's verify token.
"""

from __future__ import annotations

import hashlib
import hmac
from typing import Any, Optional

import httpx

from .channel_port import BSPConfig, ChannelPort

GRAPH_BASE = "https://graph.facebook.com/v21.0"


def verify_meta_signature(app_secret: str, body: bytes, signature_header: str) -> bool:
    """Constant-time X-Hub-Signature-256 check. Empty secret disables
    verification only by refusing everything — a missing secret must never
    silently pass."""
    if not app_secret or not signature_header:
        return False
    if not signature_header.startswith("sha256="):
        return False
    expected = hmac.new(app_secret.encode(), body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, signature_header[len("sha256="):])


def verify_challenge(mode: Optional[str], token: Optional[str], challenge: Optional[str],
                     verify_token: str) -> Optional[str]:
    """GET webhook handshake: echo hub.challenge only when mode+token match."""
    if mode == "subscribe" and token and verify_token and token == verify_token:
        return challenge
    return None


class MetaCloudAdapter(ChannelPort):
    """Direct Meta Cloud API send adapter."""

    def __init__(self, graph_base: str = GRAPH_BASE) -> None:
        self.graph_base = graph_base.rstrip("/")

    def _url(self, cfg: BSPConfig, path: str = "/messages") -> str:
        base = cfg.base_url or self.graph_base
        return f"{base.rstrip('/')}/{cfg.phone_id}{path}"

    async def send_freeform(self, cfg: BSPConfig, to: str, body: str) -> dict[str, Any]:
        payload = {
            "messaging_product": "whatsapp",
            "recipient_type": "individual",
            "to": to,
            "type": "text",
            "text": {"preview_url": False, "body": body},
        }
        return await self._post(cfg, payload)

    async def send_template(self, cfg: BSPConfig, to: str, template: str,
                            params: list[str]) -> dict[str, Any]:
        payload = {
            "messaging_product": "whatsapp",
            "to": to,
            "type": "template",
            "template": {
                "name": template,
                "language": {"code": "en"},
                "components": [{
                    "type": "body",
                    "parameters": [{"type": "text", "text": p} for p in params],
                }],
            },
        }
        return await self._post(cfg, payload)

    async def send_interactive_list(self, cfg: BSPConfig, to: str, header: str,
                                    options: list[str]) -> dict[str, Any]:
        rows = [{"id": f"opt-{i}", "title": o[:24]} for i, o in enumerate(options[:10])]
        payload = {
            "messaging_product": "whatsapp",
            "to": to,
            "type": "interactive",
            "interactive": {
                "type": "list",
                "body": {"text": header},
                "action": {"button": "Options", "sections": [{"title": "Options", "rows": rows}]},
            },
        }
        return await self._post(cfg, payload)

    async def _post(self, cfg: BSPConfig, payload: dict[str, Any]) -> dict[str, Any]:
        async with httpx.AsyncClient(timeout=10.0) as client:
            r = await client.post(self._url(cfg), json=payload,
                                  headers={"Authorization": f"Bearer {cfg.api_key}"})
            ok = r.status_code == 200
            body = r.json() if r.headers.get("content-type", "").startswith("application/json") else {"raw": r.text[:500]}
            return {"status": r.status_code, "ok": ok, "body": body}
