"""LiveKit webhook receiver: room/participant lifecycle → session state.

LiveKit signs webhooks with a bearer JWT whose sha256 claim binds the raw
body (see tokens.verify_webhook). Events handled:

    participant_joined (client-*)  → session LIVE
    participant_left  (client-*)   → session ENDED("client left")
    room_finished                  → all sessions in the room ENDED("room finished")

Session end triggers one final advisory intake flush with the idempotency
key voice:{session}:final — redelivered webhooks never double-open a case.
"""

from __future__ import annotations

from .bridge import CoreBridge
from .sessions import ENDED, LIVE, SessionStore, VoiceSession


async def end_session(store: SessionStore, bridge: CoreBridge,
                      session: VoiceSession, reason: str) -> None:
    if session.state == ENDED:
        return  # idempotent: duplicate webhooks converge
    store.transition(session, ENDED, reason)
    if session.turns > 0:
        await bridge.intake(session.tenant_id, session.client_id,
                            session.conversation_id,
                            f"[voice session ended: {reason}; {session.turns} turns]",
                            f"voice:{session.id}:final")


async def handle_event(store: SessionStore, bridge: CoreBridge, event: dict) -> dict:
    kind = event.get("event", "")
    room_name = (event.get("room") or {}).get("name", "")
    participant = event.get("participant") or {}
    identity = participant.get("identity", "")

    if kind == "participant_joined" and identity.startswith("client-"):
        for s in store.active():
            if s.room == room_name:
                store.transition(s, LIVE)
                return {"handled": kind, "session": s.id, "state": LIVE}
        return {"handled": kind, "session": None}

    if kind == "participant_left" and identity.startswith("client-"):
        for s in store.active():
            if s.room == room_name:
                await end_session(store, bridge, s, "client left")
                return {"handled": kind, "session": s.id, "state": s.state}
        return {"handled": kind, "session": None}

    if kind in ("participant_joined", "participant_left"):
        # The bot (concierge-ai) and staff joins/leaves are room events but
        # not session transitions.
        return {"handled": kind, "session": None}

    if kind == "room_finished":
        ended = []
        for s in store.active():
            if s.room == room_name:
                await end_session(store, bridge, s, "room finished")
                ended.append(s.id)
        return {"handled": kind, "ended": ended}

    return {"handled": None, "ignored": kind}
