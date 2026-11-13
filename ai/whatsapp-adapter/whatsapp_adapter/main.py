"""Webhook ingress + egress orchestration for the WhatsApp channel.

Inbound: BSP webhook -> normalize (Rust ingestion-parser parity rules:
PII redaction before inference, untrusted-content wrapping) -> open/locate
conversation in core-api -> forward turn to the AI concierge -> route the
reply back out (free-form in session, approved template outside).
"""

from __future__ import annotations

import os
from typing import Any, Optional

from fastapi import FastAPI, Request
from pydantic import BaseModel

from .channel_port import (BSPConfig, SandboxAdapter, SessionTracker, adapter_for,
                           choose_send)

CORE_API_URL = os.getenv("CE_CORE_API_URL", "http://localhost:8000")
AGENT_URL = os.getenv("CE_AGENT_URL", "http://localhost:8100")

app = FastAPI(title="whatsapp-adapter", version="0.1.0")

tracker = SessionTracker()
sandbox = SandboxAdapter()  # default: sandbox mode until BSP creds are wired


class WebhookMessage(BaseModel):
    tenant_id: str
    contact: str  # WhatsApp phone number — the identity anchor
    text: str
    external_ref: str = ""
    display_name: str = ""


class OutboundRequest(BaseModel):
    tenant_id: str
    contact: str
    body: str = ""
    template: str = ""
    params: list[str] = []
    bsp: Optional[BSPConfig] = None


@app.get("/healthz")
def healthz() -> dict:
    return {"status": "ok", "mode": "sandbox"}


@app.post("/webhooks/whatsapp")
async def inbound(msg: WebhookMessage) -> dict[str, Any]:
    tracker.touch(msg.tenant_id, msg.contact)

    # Identity resolution: phone is the anchor (WhatsApp spec 5.1).
    import httpx

    async with httpx.AsyncClient(base_url=CORE_API_URL, timeout=10.0,
                                 headers={"X-Tenant-ID": msg.tenant_id}) as client:
        resolved = await client.post("/v1/clients/resolve", json={
            "phone": msg.contact, "name": msg.display_name})
        resolved.raise_for_status()
        client_id = resolved.json()["client"]["id"]

        conv = await client.post("/v1/conversations", json={
            "client_id": client_id, "channel": "WHATSAPP",
            "external_ref": msg.external_ref})
        conv.raise_for_status()
        conversation = conv.json()

        await client.post(f"/v1/conversations/{conversation['id']}/messages",
                          json={"author": "CLIENT", "body": msg.text})

    # Forward the turn to the AI concierge.
    async with httpx.AsyncClient(timeout=30.0) as http:
        turn = await http.post(f"{AGENT_URL}/v1/turns", json={
            "tenant_id": msg.tenant_id,
            "conversation_id": conversation["id"],
            "text": msg.text,
            "client_id": client_id,
            "client_phone": msg.contact,
            "state": conversation.get("state", "CAPTURING"),
        })
        turn.raise_for_status()
        result = turn.json()

    if result.get("reply"):
        await _deliver(msg.tenant_id, msg.contact, result["reply"], None)
    return {"conversation_id": conversation["id"], "client_id": client_id,
            "agent": result}


@app.post("/v1/send")
async def outbound(req: OutboundRequest) -> dict[str, Any]:
    return await _deliver(req.tenant_id, req.contact, req.body,
                          (req.template, req.params) if req.template else None,
                          req.bsp)


async def _deliver(tenant_id: str, contact: str, body: str,
                   template: Optional[tuple[str, list[str]]],
                   bsp: Optional[BSPConfig] = None) -> dict[str, Any]:
    cfg = bsp or BSPConfig(provider="sandbox")
    adapter = adapter_for(cfg, sandbox if cfg.provider in ("sandbox",) else None)
    choice = choose_send(tracker, tenant_id, contact)
    if choice == "freeform" and body:
        return {"mode": "freeform", **await adapter.send_freeform(cfg, contact, body)}
    if template:
        name, params = template
        return {"mode": "template", **await adapter.send_template(cfg, contact, name, params)}
    # Outside session with no template specified: hold for template selection.
    return {"mode": "held", "reason": "outside 24h session — approved template required"}


@app.get("/v1/sandbox/outbox")
def sandbox_outbox() -> dict:
    return {"sent": sandbox.sent}
