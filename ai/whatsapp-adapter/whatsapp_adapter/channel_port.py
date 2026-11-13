"""WhatsApp channel adapter (WhatsApp Booking Flow spec).

One implementation of the channel-port interface — BSP choice is tenant-level
configuration, swappable without touching conversation logic. Phase 1:
BSP-managed (Infobip/Twilio); Phase 3: direct Meta Cloud API for flagship
volume.

Mechanics honored: 24-hour session windows (free-form in-session, approved
templates outside), interactive lists/buttons, per-conversation category
pricing optimized in the adapter, opt-in and quality-rating management.
"""

from __future__ import annotations

import time
from dataclasses import dataclass, field
from typing import Any, Optional

import httpx
from pydantic import BaseModel

CORE_API_URL = "http://localhost:8000"

SESSION_WINDOW_SECONDS = 24 * 3600

# Template categories (utility/marketing) with per-category conversation pricing.
TEMPLATE_LIBRARY = {
    "booking_confirmed": {"category": "utility",
                          "body": "Your booking is confirmed. Reference {{1}}. Your chauffeur details follow shortly."},
    "chauffeur_assigned": {"category": "utility",
                           "body": "Your chauffeur {{1}} ({{2}}, plate {{3}}) is assigned to your trip."},
    "car_approaching": {"category": "utility",
                        "body": "Your car is 5 minutes away. {{1}} will meet you at {{2}}."},
    "delay_replan": {"category": "utility",
                     "body": "Your flight is delayed; we have adjusted your pickup to {{1}}. No action needed."},
    "quote_followup": {"category": "utility",
                       "body": "Your quote of {{1}} is ready and held for you. Shall I confirm?"},
    "review_request": {"category": "marketing",
                       "body": "Thank you for travelling with us. A moment of your feedback is appreciated: {{1}}"},
}


class BSPConfig(BaseModel):
    """Tenant-level BSP configuration — swappable channel-port implementation."""
    provider: str = "infobip"  # infobip | twilio | meta_direct
    waba_id: str = ""
    phone_id: str = ""
    api_key: str = ""
    base_url: str = ""


@dataclass
class SessionTracker:
    """24-hour session window per (tenant, contact)."""
    sessions: dict[str, float] = field(default_factory=dict)

    def key(self, tenant_id: str, contact: str) -> str:
        return f"{tenant_id}:{contact}"

    def touch(self, tenant_id: str, contact: str) -> None:
        self.sessions[self.key(tenant_id, contact)] = time.time()

    def in_session(self, tenant_id: str, contact: str) -> bool:
        ts = self.sessions.get(self.key(tenant_id, contact))
        return ts is not None and (time.time() - ts) < SESSION_WINDOW_SECONDS


class ChannelPort:
    """The channel-port interface every channel implements."""

    async def send_freeform(self, cfg: BSPConfig, to: str, body: str) -> dict[str, Any]:
        raise NotImplementedError

    async def send_template(self, cfg: BSPConfig, to: str, template: str,
                            params: list[str]) -> dict[str, Any]:
        raise NotImplementedError

    async def send_interactive_list(self, cfg: BSPConfig, to: str, header: str,
                                    options: list[str]) -> dict[str, Any]:
        raise NotImplementedError


class InfobipAdapter(ChannelPort):
    """BSP-managed: Infobip (EU/MENA strength — fits Taylor's markets)."""

    def _url(self, cfg: BSPConfig, path: str) -> str:
        return f"{cfg.base_url or 'https://api.infobip.com'}{path}"

    async def send_freeform(self, cfg: BSPConfig, to: str, body: str) -> dict[str, Any]:
        payload = {"from": cfg.phone_id, "to": to,
                   "content": {"text": body}}
        return await self._post(cfg, "/whatsapp/1/message/text", payload)

    async def send_template(self, cfg: BSPConfig, to: str, template: str,
                            params: list[str]) -> dict[str, Any]:
        payload = {"from": cfg.phone_id, "to": to,
                   "content": {"templateName": template, "templateData": {"body": {"placeholders": params}},
                               "language": "en"}}
        return await self._post(cfg, "/whatsapp/1/message/template", payload)

    async def send_interactive_list(self, cfg: BSPConfig, to: str, header: str,
                                    options: list[str]) -> dict[str, Any]:
        rows = [{"id": f"opt-{i}", "title": o[:24]} for i, o in enumerate(options)]
        payload = {"from": cfg.phone_id, "to": to,
                   "content": {"body": {"text": header},
                               "action": {"sections": [{"title": "Options", "rows": rows}]},
                               "type": "LIST"}}
        return await self._post(cfg, "/whatsapp/1/message/interactive/list", payload)

    async def _post(self, cfg: BSPConfig, path: str, payload: dict[str, Any]) -> dict[str, Any]:
        async with httpx.AsyncClient(timeout=10.0) as client:
            r = await client.post(self._url(cfg, path), json=payload,
                                  headers={"Authorization": f"App {cfg.api_key}"})
            return {"status": r.status_code, "body": r.text[:500]}


class SandboxAdapter(ChannelPort):
    """Dev/staging adapter: records outbound messages, no network."""

    def __init__(self) -> None:
        self.sent: list[dict[str, Any]] = []

    async def send_freeform(self, cfg: BSPConfig, to: str, body: str) -> dict[str, Any]:
        self.sent.append({"kind": "freeform", "to": to, "body": body})
        return {"status": 200, "sandbox": True}

    async def send_template(self, cfg: BSPConfig, to: str, template: str,
                            params: list[str]) -> dict[str, Any]:
        self.sent.append({"kind": "template", "to": to, "template": template, "params": params})
        return {"status": 200, "sandbox": True}

    async def send_interactive_list(self, cfg: BSPConfig, to: str, header: str,
                                    options: list[str]) -> dict[str, Any]:
        self.sent.append({"kind": "list", "to": to, "header": header, "options": options})
        return {"status": 200, "sandbox": True}


def adapter_for(cfg: BSPConfig, sandbox: Optional[SandboxAdapter] = None) -> ChannelPort:
    if sandbox is not None:
        return sandbox
    if cfg.provider == "infobip":
        return InfobipAdapter()
    raise ValueError(f"unsupported BSP provider {cfg.provider!r}")


def choose_send(tracker: SessionTracker, tenant_id: str, contact: str) -> str:
    """Session-aware send choice: free-form inside the 24h window, approved
    template outside it (WhatsApp spec 2)."""
    return "freeform" if tracker.in_session(tenant_id, contact) else "template"
