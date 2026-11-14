"""Voice session state machine.

Sessions are ephemeral call state — the durable record is the conversation
thread in core-api, which outlives the call. The store is therefore
in-memory per replica (sessions never cross processes; a room lives on one
pipeline runner). If multi-replica voice ships, this becomes a store
interface backed by Postgres — same discipline as core-api stores.
"""

from __future__ import annotations

import time
import uuid
from dataclasses import dataclass, field
from typing import Optional

CREATED, LIVE, ENDED, FAILED = "CREATED", "LIVE", "ENDED", "FAILED"

_TRANSITIONS = {
    CREATED: {LIVE, ENDED, FAILED},   # ENDED from CREATED = caller never joined
    LIVE: {ENDED, FAILED},
    ENDED: set(),
    FAILED: set(),
}


class IllegalTransition(Exception):
    pass


class VoiceSession:
    def __init__(self, tenant_id: str, client_id: str, phone: str, room: str,
                 conversation_id: str):
        self.id = uuid.uuid4().hex
        self.tenant_id = tenant_id
        self.client_id = client_id
        self.phone = phone
        self.room = room
        self.conversation_id = conversation_id
        self.state = CREATED
        self.turns = 0
        self.created_at = time.time()
        self.live_at: Optional[float] = None
        self.ended_at: Optional[float] = None
        self.end_reason = ""

    def to_dict(self) -> dict:
        return {
            "id": self.id, "tenant_id": self.tenant_id, "client_id": self.client_id,
            "phone": self.phone, "room": self.room,
            "conversation_id": self.conversation_id, "state": self.state,
            "turns": self.turns, "created_at": self.created_at,
            "live_at": self.live_at, "ended_at": self.ended_at,
            "end_reason": self.end_reason,
        }


class SessionStore:
    def __init__(self):
        self._by_id: dict[str, VoiceSession] = {}

    def create(self, tenant_id: str, client_id: str, phone: str, room: str,
               conversation_id: str) -> VoiceSession:
        s = VoiceSession(tenant_id, client_id, phone, room, conversation_id)
        self._by_id[s.id] = s
        return s

    def get(self, session_id: str) -> Optional[VoiceSession]:
        return self._by_id.get(session_id)

    def get_for_tenant(self, tenant_id: str, session_id: str) -> Optional[VoiceSession]:
        s = self._by_id.get(session_id)
        if s is None or s.tenant_id != tenant_id:
            return None
        return s

    def transition(self, session: VoiceSession, to: str, reason: str = "") -> VoiceSession:
        if to not in _TRANSITIONS[session.state]:
            raise IllegalTransition(f"{session.state} → {to}")
        session.state = to
        if to == LIVE:
            session.live_at = time.time()
        if to in (ENDED, FAILED):
            session.ended_at = time.time()
            session.end_reason = reason
        return session

    def active(self, tenant_id: Optional[str] = None) -> list[VoiceSession]:
        return [s for s in self._by_id.values()
                if s.state in (CREATED, LIVE)
                and (tenant_id is None or s.tenant_id == tenant_id)]
