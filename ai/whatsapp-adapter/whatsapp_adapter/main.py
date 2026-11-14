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
    media_id: str = ""      # channel media handle (Meta media id or BSP URL) — triggers extraction
    media_mime: str = ""
    latitude: Optional[float] = None   # dropped-pin / shared location
    longitude: Optional[float] = None
    location_name: str = ""
    location_address: str = ""
    reaction: bool = False  # emoji reaction event — conversation signal, never intake


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

    # Media -> text. Images go through OCR, audio through transcription;
    # both are provider chains with engine labels. Advisory at every step:
    # download or extraction failure degrades to caption-only (or an
    # explicit placeholder) and never breaks the message flow. The engine
    # is labeled in the text itself so the downstream case/audit trail
    # keeps provenance.
    media_result: dict[str, Any] = {}
    text = msg.text
    if msg.media_id:
        from .ocr import fetch_media, ocr_image
        from .transcribe import transcribe_audio
        from .video import video_to_text

        try:
            data, mime = await fetch_media(msg.media_id, msg.media_mime)
            mime = mime or msg.media_mime
            if mime.startswith("audio/"):
                extracted = transcribe_audio(data, mime)
                label = "voice note"
            elif mime.startswith("video/"):
                extracted = video_to_text(data, mime)
                label = "video note OCR"
            else:
                extracted = ocr_image(data, mime)
                label = "media OCR"
            media_result = {"engine": extracted["engine"], "detail": extracted["detail"],
                            "chars": len(extracted["text"]), "kind": label}
            if extracted["text"]:
                media_result["extracted"] = True
                text = (f"{text}\n" if text else "") + \
                    f"[{label} via {extracted['engine']}]: {extracted['text']}"
        except Exception as exc:
            media_result = {"engine": "none",
                            "detail": f"media unavailable: {exc.__class__.__name__}"}
    if not text:
        text = "[media received — no text extracted]" if msg.media_id else ""

    # Dropped pins: fold the location into the text so conversation, agent,
    # and intake all see it. The geo serviceability check runs advisory
    # against core-api after identity resolution (it needs the tenant
    # header), and its verdict joins the text before anything is recorded.
    has_location = msg.latitude is not None and msg.longitude is not None
    geo_result: dict[str, Any] = {}
    if has_location:
        label = ", ".join(p for p in (msg.location_name, msg.location_address) if p)
        loc_text = f"[location shared: {label + ' ' if label else ''}" \
                   f"({msg.latitude:.5f},{msg.longitude:.5f})]"
        text = f"{text}\n{loc_text}" if text else loc_text
    if not text:
        text = "[message received — no text]"

    # Identity resolution: phone is the anchor (WhatsApp spec 5.1).
    import httpx

    async with httpx.AsyncClient(base_url=CORE_API_URL, timeout=10.0,
                                 headers={"X-Tenant-ID": msg.tenant_id}) as client:
        resolved = await client.post("/v1/clients/resolve", json={
            "phone": msg.contact, "name": msg.display_name})
        resolved.raise_for_status()
        client_id = resolved.json()["client"]["id"]

        if has_location:
            try:
                chk = await client.get("/v1/geo/check", params={
                    "lat": msg.latitude, "lng": msg.longitude})
                chk.raise_for_status()
                geo = chk.json()
                # areas arrive as name strings from core-api; tolerate dicts
                raw_areas = geo.get("areas", [])
                area_names = ", ".join(
                    a.get("name", "") if isinstance(a, dict) else str(a) for a in raw_areas)
                geo_result = {"serviceable": geo.get("serviceable", False),
                              "areas": area_names.split(", ") if area_names else []}
                verdict = "yes" if geo.get("serviceable") else "no"
                text += f" → serviceable: {verdict}" + (f" ({area_names})" if area_names else "")
            except Exception:
                geo_result = {"serviceable": None, "detail": "geo check unavailable"}

        conv = await client.post("/v1/conversations", json={
            "client_id": client_id, "channel": "WHATSAPP",
            "external_ref": msg.external_ref})
        conv.raise_for_status()
        conversation = conv.json()

        await client.post(f"/v1/conversations/{conversation['id']}/messages",
                          json={"author": "CLIENT", "body": text})

        # Case intake loop: triage policy in core-api decides whether this
        # message warrants an accountable case (incidents, VIP, P1/P2).
        # Idempotent per WhatsApp message id, so webhook redeliveries never
        # double-open. Advisory — intake failure never breaks the message.
        # Reactions skip intake: an emoji acknowledgement is conversation
        # context, not a service request — it never reaches the triage model
        # and can never open a junk case.
        intake_result: dict[str, Any] = {}
        if msg.reaction:
            return {"status": "recorded", "conversation_id": conversation["id"],
                    "client_id": client_id, "reaction": True,
                    **({"media": media_result} if media_result else {})}
        try:
            idem = f"wa:{msg.external_ref}" if msg.external_ref else _stable_idem(
                msg.tenant_id, msg.contact, text)
            intake = await client.post("/v1/cases/intake", json={
                "client_id": client_id, "conversation_id": conversation["id"],
                "text": text, "idempotency_key": idem})
            intake.raise_for_status()
            intake_result = intake.json()
        except Exception:
            intake_result = {"opened": False, "reason": "intake unavailable"}

    # Forward the turn to the AI concierge.
    async with httpx.AsyncClient(timeout=30.0) as http:
        turn = await http.post(f"{AGENT_URL}/v1/turns", json={
            "tenant_id": msg.tenant_id,
            "conversation_id": conversation["id"],
            "text": text,
            "client_id": client_id,
            "client_phone": msg.contact,
            "state": conversation.get("state", "CAPTURING"),
        })
        turn.raise_for_status()
        result = turn.json()

    if result.get("reply"):
        await _deliver(msg.tenant_id, msg.contact, result["reply"], None)
    return {"conversation_id": conversation["id"], "client_id": client_id,
            "agent": result, "intake": intake_result, "media": media_result,
            "geo": geo_result}


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
            # Delivery/read receipts for OUR outbound messages — SLA evidence.
            # Advisory: receipt recording never blocks message processing.
            for status in value.get("statuses", []):
                ref = status.get("id", "")
                st = status.get("status", "")
                if ref and st in ("sent", "delivered", "read", "failed"):
                    await _record_receipt(tenant_id, "whatsapp", ref, st,
                                          status.get("timestamp", ""))
                    processed += 1
            contacts = {c.get("wa_id"): c.get("profile", {}).get("name", "")
                        for c in value.get("contacts", [])}
            for msg in value.get("messages", []):
                mtype = msg.get("type")
                contact = msg.get("from", "")
                if not contact:
                    continue
                if mtype == "text":
                    text = msg.get("text", {}).get("body", "")
                    if not text:
                        continue
                    await inbound(WebhookMessage(
                        tenant_id=tenant_id, contact=contact, text=text,
                        external_ref=msg.get("id", ""),
                        display_name=contacts.get(contact, "")))
                    processed += 1
                elif mtype in ("image", "document", "audio", "video"):
                    media = msg.get(mtype, {})
                    await inbound(WebhookMessage(
                        tenant_id=tenant_id, contact=contact,
                        text=media.get("caption", ""),
                        external_ref=msg.get("id", ""),
                        display_name=contacts.get(contact, ""),
                        media_id=media.get("id", ""),
                        media_mime=media.get("mime_type", "")))
                    processed += 1
                elif mtype == "interactive":
                    # Taps on our outbound list/button options come back as
                    # interactive replies — treat the selection as the
                    # message, keeping the option id for provenance.
                    inter = msg.get("interactive", {})
                    reply = inter.get("button_reply") or inter.get("list_reply") or {}
                    title, rid = reply.get("title", ""), reply.get("id", "")
                    if not title:
                        continue
                    text = f"{title} [option: {rid}]" if rid else title
                    await inbound(WebhookMessage(
                        tenant_id=tenant_id, contact=contact, text=text,
                        external_ref=msg.get("id", ""),
                        display_name=contacts.get(contact, "")))
                    processed += 1
                elif mtype == "button":
                    # Legacy quick-reply buttons.
                    btn = msg.get("button", {})
                    text, payload_id = btn.get("text", ""), btn.get("payload", "")
                    if not text:
                        continue
                    if payload_id:
                        text = f"{text} [option: {payload_id}]"
                    await inbound(WebhookMessage(
                        tenant_id=tenant_id, contact=contact, text=text,
                        external_ref=msg.get("id", ""),
                        display_name=contacts.get(contact, "")))
                    processed += 1
                elif mtype == "location":
                    loc = msg.get("location", {})
                    lat, lng = loc.get("latitude"), loc.get("longitude")
                    if lat is None or lng is None:
                        continue
                    await inbound(WebhookMessage(
                        tenant_id=tenant_id, contact=contact, text="",
                        external_ref=msg.get("id", ""),
                        display_name=contacts.get(contact, ""),
                        latitude=lat, longitude=lng,
                        location_name=loc.get("name", ""),
                        location_address=loc.get("address", "")))
                    processed += 1
                elif mtype == "contacts":
                    # Shared contact card(s): fold into text for intake —
                    # "my assistant will coordinate" is a classic concierge move.
                    cards = []
                    for c in msg.get("contacts", []):
                        name = (c.get("name", {}) or {}).get("formatted_name", "")
                        phones = c.get("phones", []) or []
                        phone = phones[0].get("phone", "") if phones else ""
                        cards.append(" — ".join(p for p in (name, phone) if p))
                    if not any(cards):
                        continue
                    await inbound(WebhookMessage(
                        tenant_id=tenant_id, contact=contact,
                        text=f"[contact shared: {'; '.join(cards)}]",
                        external_ref=msg.get("id", ""),
                        display_name=contacts.get(contact, "")))
                    processed += 1
                elif mtype == "reaction":
                    # Emoji reaction to a message — ours or the client's own.
                    # Empty emoji = reaction removed. Kept as a bracketed
                    # provenance line on the conversation timeline.
                    rxn = msg.get("reaction", {})
                    emoji = rxn.get("emoji", "")
                    text = f"[reaction: {emoji}]" if emoji else "[reaction removed]"
                    await inbound(WebhookMessage(
                        tenant_id=tenant_id, contact=contact, text=text,
                        external_ref=msg.get("id", ""),
                        display_name=contacts.get(contact, ""),
                        reaction=True))
                    processed += 1
                # remaining types (e2e nacks, ...): skipped honestly
    return {"processed": processed}


async def _record_receipt(tenant_id: str, channel: str, external_ref: str,
                          status: str, at: str) -> None:
    """Forward a delivery/read receipt to core-api. Advisory — receipts are
    SLA evidence, never on the message path's critical line."""
    import httpx

    if at and at.isdigit():
        from datetime import datetime, timezone
        at = datetime.fromtimestamp(int(at), tz=timezone.utc).isoformat()
    try:
        async with httpx.AsyncClient(base_url=CORE_API_URL, timeout=5.0,
                                     headers={"X-Tenant-ID": tenant_id}) as client:
            await client.post("/v1/channels/receipts", json={
                "channel": channel, "external_ref": external_ref,
                "status": status, "at": at})
    except Exception:
        pass


def tenant_of_phone(phone_id: str, tenant_map: dict[str, str]) -> str:
    """phone_number_id -> tenant. Explicit map first, then the single-tenant
    default for flagship deployments (CE_META_DEFAULT_TENANT)."""
    if phone_id in tenant_map:
        return tenant_map[phone_id]
    return os.getenv("CE_META_DEFAULT_TENANT", "")


# ---------------------------------------------------------------------------
# Infobip inbound webhook (BSP path). Infobip posts {"results": [...]} with
# message.type TEXT|IMAGE|VIDEO|AUDIO|DOCUMENT|LOCATION; media arrives as a
# direct content URL (downloaded with the App-key auth) rather than Meta's
# two-step id flow. Tenant resolution mirrors the Meta path: receiver number
# -> tenant map (CE_INFOBIP_TENANT_MAP), else CE_INFOBIP_DEFAULT_TENANT.
# ---------------------------------------------------------------------------

def _infobip_tenant(receiver: str) -> str:
    raw = os.getenv("CE_INFOBIP_TENANT_MAP", "")  # "receiver:tenant_id,..."
    for pair in raw.split(","):
        if ":" in pair:
            number, tenant_id = pair.split(":", 1)
            if number.strip() == receiver:
                return tenant_id.strip()
    return os.getenv("CE_INFOBIP_DEFAULT_TENANT", "")


@app.post("/webhooks/whatsapp/infobip")
async def infobip_inbound(request: Request) -> dict[str, Any]:
    payload = await request.json()
    processed = 0
    for result in payload.get("results", []):
        message = result.get("message", {})
        mtype = str(message.get("type", "")).upper()
        contact = result.get("from", "")
        tenant_id = _infobip_tenant(str(result.get("to", "")))
        if not contact or not tenant_id:
            continue
        ref = str(result.get("messageId", ""))
        if mtype == "TEXT":
            text = message.get("text", "")
            if not text:
                continue
            await inbound(WebhookMessage(tenant_id=tenant_id, contact=contact,
                                         text=text, external_ref=ref))
            processed += 1
        elif mtype in ("IMAGE", "VIDEO", "AUDIO", "DOCUMENT"):
            await inbound(WebhookMessage(
                tenant_id=tenant_id, contact=contact,
                text=message.get("caption", ""),
                external_ref=ref,
                media_id=message.get("url", ""),  # direct download URL
                media_mime=""))
            processed += 1
        elif mtype == "LOCATION":
            lat, lng = message.get("latitude"), message.get("longitude")
            if lat is None or lng is None:
                continue
            await inbound(WebhookMessage(
                tenant_id=tenant_id, contact=contact, text="",
                external_ref=ref, latitude=lat, longitude=lng,
                location_name=message.get("name", ""),
                location_address=message.get("address", "")))
            processed += 1
        elif mtype == "CONTACT":
            cards = []
            for c in message.get("contacts", []):
                name = (c.get("name", {}) or {}).get("formatted_name", "")
                phones = c.get("phones", []) or []
                phone = phones[0].get("phone", "") if phones else ""
                cards.append(" — ".join(p for p in (name, phone) if p))
            if not any(cards):
                continue
            await inbound(WebhookMessage(
                tenant_id=tenant_id, contact=contact,
                text=f"[contact shared: {'; '.join(cards)}]",
                external_ref=ref))
            processed += 1
        elif mtype == "REACTION":
            # Infobip: message.reaction = {action: REACT|UNREACT, emoji,
            # messageId} (older payloads nest it under content.reaction).
            rxn = message.get("reaction") or \
                (message.get("content", {}) or {}).get("reaction", {}) or {}
            action = str(rxn.get("action", "REACT")).upper()
            emoji = rxn.get("emoji", "")
            text = f"[reaction: {emoji}]" \
                if (emoji and action != "UNREACT") else "[reaction removed]"
            await inbound(WebhookMessage(
                tenant_id=tenant_id, contact=contact, text=text,
                external_ref=ref, reaction=True))
            processed += 1
        # other types: not normalized yet — skipped honestly
    return {"processed": processed}


# Infobip delivery reports (separate webhook URL in the BSP console):
# {"results": [{"messageId": ..., "status": {"groupName": ...}}]}
_INFOBIP_STATUS_MAP = {
    "ACCEPTED": "sent", "PENDING": "sent", "DELIVERED": "delivered",
    "SEEN": "read", "READ": "read",
    "REJECTED": "failed", "UNDELIVERABLE": "failed", "EXPIRED": "failed",
}


@app.post("/webhooks/whatsapp/infobip/status")
async def infobip_status(request: Request) -> dict[str, Any]:
    payload = await request.json()
    processed = 0
    for result in payload.get("results", []):
        tenant_id = _infobip_tenant(str(result.get("to", "")))
        ref = str(result.get("messageId", ""))
        group = str((result.get("status") or {}).get("groupName", "")).upper()
        status = _INFOBIP_STATUS_MAP.get(group)
        if not tenant_id or not ref or not status:
            continue
        await _record_receipt(tenant_id, "whatsapp", ref, status, "")
        processed += 1
    return {"processed": processed}
