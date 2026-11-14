"""Bridge from voice sessions to the platform's durable record.

Every spoken turn becomes exactly what a WhatsApp message already is:
a resolved client, a get-or-created conversation thread (Phase 22 — repeat
calls land on the SAME VOICE thread, not a new conversation per call),
CLIENT/AI provenance lines, an advisory case-intake call with a
deterministic idempotency key, and an agent turn answered by the real
concierge-agent (policy engine, tool tiers, budget caps, shadow mode — all
inherited, nothing re-implemented here).

Failure discipline mirrors the WhatsApp adapter: identity resolution and
conversation opening are hard requirements (a voice turn with no client
anchor is refuse-able); intake and reply-recording are advisory and never
kill a live call.
"""

from __future__ import annotations

from typing import Any, Optional

import httpx

from . import config

CHANNEL = "VOICE"


class BridgeError(Exception):
    """Hard failure anchoring a session or turn to the platform record."""


class CoreBridge:
    def __init__(self, core_url: Optional[str] = None, agent_url: Optional[str] = None,
                 transport: Optional[httpx.AsyncBaseTransport] = None):
        self.core = core_url or config.CORE_API_URL
        self.agent = agent_url or config.AGENT_URL
        self._transport = transport

    def _client(self, base_url: str, tenant_id: str, timeout: float = 10.0) -> httpx.AsyncClient:
        return httpx.AsyncClient(base_url=base_url, timeout=timeout,
                                 headers={"X-Tenant-ID": tenant_id},
                                 transport=self._transport)

    async def resolve_client(self, tenant_id: str, phone: str, name: str = "") -> str:
        """Phone is the identity anchor (same rule as WhatsApp spec 5.1).
        Hard failure: no client, no session."""
        try:
            async with self._client(self.core, tenant_id) as c:
                r = await c.post("/v1/clients/resolve",
                                 json={"phone": phone, "name": name})
                r.raise_for_status()
                return r.json()["client"]["id"]
        except Exception as e:
            raise BridgeError(f"client resolution failed: {e}") from e

    async def open_conversation(self, tenant_id: str, client_id: str,
                                session_id: str) -> dict:
        """Get-or-create the client's VOICE thread. Phase 22 threading means
        a returning caller continues their one VOICE conversation."""
        try:
            async with self._client(self.core, tenant_id) as c:
                r = await c.post("/v1/conversations", json={
                    "client_id": client_id, "channel": CHANNEL,
                    "external_ref": f"voice:{session_id}"})
                r.raise_for_status()
                return r.json()
        except Exception as e:
            raise BridgeError(f"conversation open failed: {e}") from e

    async def post_message(self, tenant_id: str, conversation_id: str,
                           author: str, body: str) -> bool:
        """Advisory — a lost transcript line is a provenance gap, logged by
        the caller, never a dropped call."""
        try:
            async with self._client(self.core, tenant_id) as c:
                r = await c.post(f"/v1/conversations/{conversation_id}/messages",
                                 json={"author": author, "body": body})
                r.raise_for_status()
                return True
        except Exception:
            return False

    async def intake(self, tenant_id: str, client_id: str, conversation_id: str,
                     text: str, idempotency_key: str) -> dict:
        """Advisory per-turn triage (same as WhatsApp per-message intake):
        triage policy decides if the spoken words open an accountable case.
        Idempotent per (session, turn) — webhook redelivery or a repeated
        final flush never double-opens."""
        try:
            async with self._client(self.core, tenant_id) as c:
                r = await c.post("/v1/cases/intake", json={
                    "client_id": client_id, "conversation_id": conversation_id,
                    "text": text, "idempotency_key": idempotency_key})
                r.raise_for_status()
                return r.json()
        except Exception:
            return {"opened": False, "reason": "intake unavailable"}

    async def agent_turn(self, tenant_id: str, conversation_id: str, text: str,
                         client_id: str, phone: str, state: str) -> str:
        """The agent service is the LLM stage of the voice pipeline. Returns
        the reply text to speak; empty string = stay silent (agent declined
        or errored — the pipeline says a holding phrase, never a hallucinated
        answer)."""
        try:
            async with self._client(self.agent, tenant_id, timeout=30.0) as c:
                r = await c.post("/v1/turns", json={
                    "tenant_id": tenant_id, "conversation_id": conversation_id,
                    "text": text, "client_id": client_id, "client_phone": phone,
                    "state": state or "CAPTURING"})
                r.raise_for_status()
                return r.json().get("reply", "")
        except Exception:
            return ""


async def handle_user_turn(bridge: CoreBridge, session, transcript: str) -> str:
    """One completed caller utterance → platform record → agent reply.
    Returns the text to synthesize. Shared by the Pipecat processor and by
    tests, so the turn contract is exercised without audio hardware."""
    session.turns += 1
    await bridge.post_message(session.tenant_id, session.conversation_id,
                              "CLIENT", transcript)
    await bridge.intake(session.tenant_id, session.client_id,
                        session.conversation_id, transcript,
                        f"voice:{session.id}:{session.turns}")
    reply = await bridge.agent_turn(session.tenant_id, session.conversation_id,
                                    transcript, session.client_id, session.phone,
                                    "CAPTURING")
    if reply:
        await bridge.post_message(session.tenant_id, session.conversation_id,
                                  "AI", reply)
    return reply or "One moment — let me get a human concierge to join us."
