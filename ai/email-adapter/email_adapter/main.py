"""Email channel adapter — FastAPI surface (Phase 39).

Outbound: core-api POSTs /v1/send; we build the compliance MIME (footer
already in body, List-Unsubscribe headers here) and deliver via SMTP — or
record into the sandbox outbox when no SMTP host is configured.

Tracking: GET /t/o/{token} (open pixel) and /t/c/{token}?u= (click redirect)
are the adapter's own public endpoints; each verified hit is forwarded to
core-api's attribution spine.

Inbound: /webhooks/email/inbound takes a normalized message (from the
tenant's inbound-parse provider — SES, Mailgun, Postmark) and drives the
same resolve -> conversation -> intake -> agent loop as the WhatsApp
adapter, with the agent's reply delivered back by email.

ESP events: /webhooks/email/events maps provider delivery/bounce/complaint
webhooks onto the receipt spine; complaints also flip marketing consent off
— a complaint is an opt-out, not a statistic.
"""

from __future__ import annotations

import json
import os
import uuid
from typing import Any, Optional

import httpx
from fastapi import FastAPI, Request
from fastapi.responses import RedirectResponse, Response
from pydantic import BaseModel

from . import tokens
from .mailer import SMTPConfig, build_message, sandbox, send

CORE_API_URL = os.getenv("CE_CORE_API_URL", "http://localhost:8000")
AGENT_URL = os.getenv("CE_AGENT_URL", "http://localhost:8100")
PUBLIC_BASE = os.getenv("CE_EMAIL_PUBLIC_BASE", "").rstrip("/")
TRACKING_SECRET = os.getenv("CE_EMAIL_TRACKING_SECRET", "")
DEFAULT_TENANT = os.getenv("CE_EMAIL_DEFAULT_TENANT", "")

SMTP_CFG = SMTPConfig(
    host=os.getenv("CE_SMTP_HOST", ""),
    port=int(os.getenv("CE_SMTP_PORT", "587")),
    user=os.getenv("CE_SMTP_USER", ""),
    password=os.getenv("CE_SMTP_PASS", ""),
    sender=os.getenv("CE_SMTP_FROM", ""),
    sender_name=os.getenv("CE_SMTP_FROM_NAME", ""),
    tls=os.getenv("CE_SMTP_TLS", "starttls"),
)

# Inbound tenant resolution: {"mail.taylor.example": "<tenant-uuid>"} — the
# receiving domain identifies the tenant, never caller-supplied input.
TENANT_MAP: dict[str, str] = json.loads(os.getenv("CE_EMAIL_TENANT_MAP", "{}"))

PIXEL_GIF = bytes.fromhex(
    "47494638396101000100800000000000ffffff21f90401000000002c00000000"
    "010001000002024401003b")

app = FastAPI(title="email-adapter", version="0.1.0")


class SendRequest(BaseModel):
    tenant_id: str
    to: str
    subject: str
    body: str
    unsubscribe_url: str = ""
    client_id: str = ""
    campaign: str = ""


@app.get("/healthz")
def healthz() -> dict:
    return {"status": "ok", "smtp_configured": SMTP_CFG.configured,
            "tracking": bool(PUBLIC_BASE and TRACKING_SECRET),
            "sandbox": not SMTP_CFG.configured}


@app.post("/v1/send")
def outbound(req: SendRequest) -> dict[str, Any]:
    send_ref = uuid.uuid4().hex[:12]
    msg, message_id = build_message(
        SMTP_CFG, to=req.to, subject=req.subject, body=req.body,
        unsubscribe_url=req.unsubscribe_url,
        tracking_base=PUBLIC_BASE, tracking_secret=TRACKING_SECRET,
        tenant_id=req.tenant_id, client_id=req.client_id, send_ref=send_ref)
    if not SMTP_CFG.configured:
        sandbox.sent.append({"tenant_id": req.tenant_id, "to": req.to,
                             "subject": req.subject, "message_id": message_id})
        return {"mode": "sandbox", "message_id": message_id}
    try:
        send(SMTP_CFG, msg, req.to)
    except Exception as exc:  # noqa: BLE001 — core-api records the failure
        return {"mode": "held", "reason": f"smtp: {exc.__class__.__name__}: {exc}"}
    return {"mode": "smtp", "message_id": message_id, "send_ref": send_ref}


@app.get("/v1/sandbox/outbox")
def sandbox_outbox() -> dict:
    return {"sent": sandbox.sent}


# ---------------------------------------------------------------- tracking

async def _track(tenant_id: str, kind: str, client_id: str, send_ref: str) -> None:
    """Forward one engagement event to the attribution spine. Advisory."""
    try:
        async with httpx.AsyncClient(base_url=CORE_API_URL, timeout=5.0,
                                     headers={"X-Tenant-ID": tenant_id}) as c:
            await c.post("/v1/attribution", json={
                "kind": kind, "channel": "email",
                "client_id": client_id, "campaign": send_ref})
    except Exception:
        pass


@app.get("/t/o/{token}")
async def track_open(token: str) -> Response:
    try:
        t = tokens.verify(TRACKING_SECRET, token, "o")
    except ValueError:
        return Response(status_code=403)
    await _track(t["tenant_id"], "email_opened", t["client_id"], t["send_ref"])
    return Response(content=PIXEL_GIF, media_type="image/gif",
                    headers={"Cache-Control": "no-store"})


@app.get("/t/c/{token}")
async def track_click(token: str, u: str = "") -> Response:
    try:
        t = tokens.verify(TRACKING_SECRET, token, "c")
    except ValueError:
        return Response(status_code=403)
    if not u.startswith(("http://", "https://")):
        return Response(status_code=400)
    await _track(t["tenant_id"], "email_clicked", t["client_id"], t["send_ref"])
    return RedirectResponse(u, status_code=302)


# ------------------------------------------------------------- ESP events

class ESPEvent(BaseModel):
    type: str            # delivered | bounced | complained | opened | clicked
    message_ref: str = ""
    recipient: str = ""
    tenant_id: str = ""


@app.post("/webhooks/email/events")
async def esp_events(ev: ESPEvent, request: Request) -> dict[str, Any]:
    tenant_id = ev.tenant_id or request.headers.get("X-Tenant-ID", "") or DEFAULT_TENANT
    if not tenant_id:
        return {"status": "refused", "reason": "tenant unresolved"}
    out: dict[str, Any] = {"status": "recorded", "type": ev.type}
    async with httpx.AsyncClient(base_url=CORE_API_URL, timeout=10.0,
                                 headers={"X-Tenant-ID": tenant_id}) as c:
        if ev.type in ("delivered", "bounced"):
            try:
                await c.post("/v1/channels/receipts", json={
                    "channel": "email", "external_ref": ev.message_ref,
                    "status": "delivered" if ev.type == "delivered" else "failed",
                    "detail": ev.type})
            except Exception:
                out["receipt"] = "unavailable"
            if ev.type == "bounced":
                # Phase 40: feed the deliverability guard — bounces count
                # against the tenant's 7-day sender-reputation window.
                try:
                    await c.post("/v1/attribution", json={
                        "kind": "email_bounced", "channel": "email",
                        "campaign": ev.message_ref})
                    out["attribution"] = "email_bounced"
                except Exception:
                    out["attribution"] = "unavailable"
                # Phase 41: a hard bounce suppresses the address permanently —
                # re-mailing known-dead addresses is the single biggest
                # bounce driver. Idempotent on the core side (replay = 200).
                if ev.recipient:
                    try:
                        await c.post("/v1/email/suppressions", json={
                            "email": ev.recipient,
                            "reason": "hard_bounce",
                            "source_ref": ev.message_ref})
                        out["suppression"] = "hard_bounce"
                    except Exception:
                        out["suppression"] = "unavailable"
        elif ev.type == "complained":
            # A spam complaint is an opt-out: find the client by email and
            # flip marketing consent off on the shared consent spine.
            client_id = ""
            try:
                rr = await c.post("/v1/clients/resolve",
                                  json={"email": ev.recipient})
                rr.raise_for_status()
                client_id = rr.json()["client"]["id"]
                await c.put(f"/v1/clients/{client_id}/consent",
                            json={"scope": "marketing", "granted": False})
                out["consent"] = "marketing=false"
            except Exception:
                out["consent"] = "unavailable"
            # Phase 40: complaints weigh on the deliverability guard even
            # when the recipient was never a known client.
            try:
                await c.post("/v1/attribution", json={
                    "kind": "email_complained", "channel": "email",
                    "client_id": client_id, "campaign": ev.message_ref})
                out["attribution"] = "email_complained"
            except Exception:
                out["attribution"] = "unavailable"
        elif ev.type in ("opened", "clicked"):
            await _track(tenant_id, f"email_{ev.type}", "", ev.message_ref)
        else:
            return {"status": "ignored", "type": ev.type}
    return out


# ---------------------------------------------------------------- inbound

class InboundMessage(BaseModel):
    from_email: str
    from_name: str = ""
    subject: str = ""
    text: str = ""
    message_id: str = ""
    to_address: str = ""  # receiving address — resolves the tenant


def _tenant_of_inbound(msg: InboundMessage) -> str:
    domain = msg.to_address.split("@")[-1].lower() if "@" in msg.to_address else ""
    return TENANT_MAP.get(msg.to_address.lower()) or TENANT_MAP.get(domain) or DEFAULT_TENANT


@app.post("/webhooks/email/inbound")
async def inbound(msg: InboundMessage) -> dict[str, Any]:
    tenant_id = _tenant_of_inbound(msg)
    if not tenant_id:
        return {"status": "refused", "reason": "no tenant for receiving address"}

    text = msg.text or ""
    if msg.subject:
        text = f"[subject: {msg.subject}]\n{text}" if text else f"[subject: {msg.subject}]"
    if not text:
        text = "[email received — no text]"

    async with httpx.AsyncClient(base_url=CORE_API_URL, timeout=10.0,
                                 headers={"X-Tenant-ID": tenant_id}) as c:
        resolved = await c.post("/v1/clients/resolve", json={
            "email": msg.from_email, "name": msg.from_name})
        resolved.raise_for_status()
        client_id = resolved.json()["client"]["id"]

        conv = await c.post("/v1/conversations", json={
            "client_id": client_id, "channel": "EMAIL",
            "external_ref": msg.message_id})
        conv.raise_for_status()
        conversation = conv.json()

        await c.post(f"/v1/conversations/{conversation['id']}/messages",
                     json={"author": "CLIENT", "body": text})

        intake_result: dict[str, Any] = {}
        try:
            idem = f"email:{msg.message_id}" if msg.message_id else \
                f"email:auto:{uuid.uuid5(uuid.NAMESPACE_URL, tenant_id + msg.from_email + text).hex[:24]}"
            intake = await c.post("/v1/cases/intake", json={
                "client_id": client_id, "conversation_id": conversation["id"],
                "text": text, "idempotency_key": idem})
            intake.raise_for_status()
            intake_result = intake.json()
        except Exception:
            intake_result = {"opened": False, "reason": "intake unavailable"}

    async with httpx.AsyncClient(timeout=30.0) as http:
        turn = await http.post(f"{AGENT_URL}/v1/turns", json={
            "tenant_id": tenant_id,
            "conversation_id": conversation["id"],
            "text": text,
            "client_id": client_id,
            "client_email": msg.from_email,
            "state": conversation.get("state", "CAPTURING"),
        })
        turn.raise_for_status()
        result = turn.json()

    replied = False
    if result.get("reply"):
        subject = msg.subject if msg.subject.lower().startswith("re:") else f"Re: {msg.subject}"
        resp = outbound(SendRequest(
            tenant_id=tenant_id, to=msg.from_email,
            subject=subject or "Re: your concierge request",
            body=result["reply"], client_id=client_id))
        replied = resp.get("mode") in ("smtp", "sandbox")

    return {"conversation_id": conversation["id"], "client_id": client_id,
            "agent": result, "intake": intake_result, "replied": replied}
