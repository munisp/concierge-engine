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

        # Case intake loop: triage policy in core-api decides whether this
        # message warrants an accountable case (incidents, VIP, P1/P2).
        # Idempotent per WhatsApp message id, so webhook redeliveries never
        # double-open. Advisory — intake failure never breaks the message.
        intake_result: dict[str, Any] = {}
        try:
            idem = f"wa:{msg.external_ref}" if msg.external_ref else _stable_idem(
                msg.tenant_id, msg.contact, msg.text)
            intake = await client.post("/v1/cases/intake", json={
                "client_id": client_id, "conversation_id": conversation["id"],
                "text": msg.text, "idempotency_key": idem})
            intake.raise_for_status()
            intake_result = intake.json()
        except Exception:
            intake_result = {"opened": False, "reason": "intake unavailable"}

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
            "agent": result, "intake": intake_result}


def _stable_idem(tenant_id: str, contact: str, text: str) -> str:
    """Deterministic idempotency key when the channel gave no message id:
    same (tenant, contact, text) redelivered maps to the same key."""
    import hashlib

    digest = hashlib.sha256(f"{tenant_id}|{contact}|{text}".encode()).hexdigest()
    return f"wa:auto:{digest[:24]}"


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


# ---------------------------------------------------------------------------
# Meta Cloud API webhook (direct integration path).
#
# GET  — verification handshake: Meta probes with hub.mode/hub.verify_token/
#        hub.challenge; we echo the challenge only on a token match.
# POST — signed ingress: X-Hub-Signature-256 over the RAW body, verified
#        before parsing. The tenant is resolved from the receiving
#        phone_number_id (CE_META_TENANT_MAP), never from caller input.
# ---------------------------------------------------------------------------

META_APP_SECRET = os.getenv("CE_META_APP_SECRET", "")
META_VERIFY_TOKEN = os.getenv("CE_META_VERIFY_TOKEN", "")


def _meta_tenant_map() -> dict[str, str]:
    raw = os.getenv("CE_META_TENANT_MAP", "")  # "phone_id:tenant_id,..."
    out = {}
    for pair in raw.split(","):
        if ":" in pair:
            phone_id, tenant_id = pair.split(":", 1)
            out[phone_id.strip()] = tenant_id.strip()
    return out


@app.get("/webhooks/whatsapp/meta")
async def meta_verify(request: Request):
    from .meta import verify_challenge

    params = request.query_params
    challenge = verify_challenge(params.get("hub.mode"), params.get("hub.verify_token"),
                                 params.get("hub.challenge"), META_VERIFY_TOKEN)
    if challenge is None:
        from fastapi.responses import PlainTextResponse
        return PlainTextResponse("forbidden", status_code=403)
    from fastapi.responses import PlainTextResponse
    return PlainTextResponse(challenge)


@app.post("/webhooks/whatsapp/meta")
async def meta_inbound(request: Request) -> dict[str, Any]:
    from .meta import verify_meta_signature

    body = await request.body()
    if not verify_meta_signature(META_APP_SECRET, body,
                                 request.headers.get("x-hub-signature-256", "")):
        from fastapi.responses import JSONResponse
        return JSONResponse({"error": "invalid signature"}, status_code=403)

    import json as _json

    payload = _json.loads(body)
    tenant_map = _meta_tenant_map()
    processed = 0
    for entry in payload.get("entry", []):
        for change in entry.get("changes", []):
            value = change.get("value", {})
            phone_id = value.get("metadata", {}).get("phone_number_id", "")
            tenant_id = tenant_of_phone(phone_id, tenant_map)
            if not tenant_id:
                continue
            contacts = {c.get("wa_id"): c.get("profile", {}).get("name", "")
                        for c in value.get("contacts", [])}
            for msg in value.get("messages", []):
                if msg.get("type") != "text":
                    continue  # media/interactive replies: next iteration
                contact = msg.get("from", "")
                text = msg.get("text", {}).get("body", "")
                if not contact or not text:
                    continue
                await inbound(WebhookMessage(
                    tenant_id=tenant_id, contact=contact, text=text,
                    external_ref=msg.get("id", ""),
                    display_name=contacts.get(contact, "")))
                processed += 1
    return {"processed": processed}


def tenant_of_phone(phone_id: str, tenant_map: dict[str, str]) -> str:
    """phone_number_id -> tenant. Explicit map first, then the single-tenant
    default for flagship deployments (CE_META_DEFAULT_TENANT)."""
    if phone_id in tenant_map:
        return tenant_map[phone_id]
    return os.getenv("CE_META_DEFAULT_TENANT", "")
