"""Bridge contract tests — the voice turn is a platform turn.

Runs against httpx.MockTransport: no core-api, no agent, no network. What
is asserted is the CONTRACT the voice channel has with the platform:
identity anchor, VOICE thread get-or-create shape, provenance lines,
per-turn idempotent intake, and advisory vs hard failure boundaries.
Sync wrappers mirror the whatsapp-adapter test convention (no
pytest-asyncio dependency).
"""

import asyncio
import json

import httpx
import pytest

from voice_adapter.bridge import (BridgeError, CoreBridge, handle_user_turn)
from voice_adapter.sessions import SessionStore


class Recorder:
    def __init__(self, fail: set[str] | None = None):
        self.calls: list[tuple[str, str, dict]] = []
        self.fail = fail or set()

    def handler(self, request: httpx.Request) -> httpx.Response:
        path = request.url.path
        body = json.loads(request.content or b"{}")
        self.calls.append((path, body))
        if path in self.fail:
            return httpx.Response(502, json={"error": "boom"})
        if path == "/v1/clients/resolve":
            return httpx.Response(200, json={"client": {"id": "client-1"}})
        if path == "/v1/conversations":
            return httpx.Response(201, json={"id": "conv-1", "state": "CAPTURING"})
        if path.endswith("/messages"):
            return httpx.Response(201, json={"id": "msg-1"})
        if path == "/v1/cases/intake":
            return httpx.Response(200, json={"opened": False})
        if path == "/v1/turns":
            return httpx.Response(200, json={"reply": "Your driver arrives at 7."})
        return httpx.Response(404, json={"error": path})


def bridge_for(rec: Recorder) -> CoreBridge:
    return CoreBridge(core_url="http://core", agent_url="http://agent",
                      transport=httpx.MockTransport(rec.handler))


def make_session():
    return SessionStore().create("tenant-1", "client-1", "+971500000000",
                                 "room-1", "conv-1")


def run(coro):
    return asyncio.run(coro)


def test_user_turn_full_contract():
    rec = Recorder()
    sess = make_session()
    reply = run(handle_user_turn(bridge_for(rec), sess,
                                 "I need a car at 7 tomorrow"))
    assert reply == "Your driver arrives at 7."
    assert sess.turns == 1
    paths = [p for p, _ in rec.calls]
    # CLIENT transcript recorded, advisory intake, agent turn, AI reply recorded.
    assert paths == ["/v1/conversations/conv-1/messages", "/v1/cases/intake",
                     "/v1/turns", "/v1/conversations/conv-1/messages"]
    assert rec.calls[0][1] == {"author": "CLIENT",
                               "body": "I need a car at 7 tomorrow"}
    intake = rec.calls[1][1]
    assert intake["idempotency_key"] == f"voice:{sess.id}:1"
    assert intake["text"] == "I need a car at 7 tomorrow"
    turn = rec.calls[2][1]
    assert turn["tenant_id"] == "tenant-1" and turn["conversation_id"] == "conv-1"
    assert turn["client_phone"] == "+971500000000"
    assert rec.calls[3][1] == {"author": "AI", "body": "Your driver arrives at 7."}


def test_second_turn_increments_idem_key():
    rec = Recorder()
    sess = make_session()
    b = bridge_for(rec)
    run(handle_user_turn(b, sess, "first"))
    run(handle_user_turn(b, sess, "second"))
    keys = [c["idempotency_key"] for p, c in rec.calls if p == "/v1/cases/intake"]
    assert keys == [f"voice:{sess.id}:1", f"voice:{sess.id}:2"]


def test_agent_failure_speaks_holding_phrase_and_records_nothing_fake():
    rec = Recorder(fail={"/v1/turns"})
    sess = make_session()
    reply = run(handle_user_turn(bridge_for(rec), sess, "hello"))
    assert "human concierge" in reply  # honest holding phrase, never hallucinated
    ai_msgs = [c for p, c in rec.calls
               if p.endswith("/messages") and c.get("author") == "AI"]
    assert ai_msgs == []  # no fake AI line recorded


def test_intake_failure_is_advisory():
    rec = Recorder(fail={"/v1/cases/intake"})
    sess = make_session()
    reply = run(handle_user_turn(bridge_for(rec), sess, "hello"))
    assert reply == "Your driver arrives at 7."  # turn unaffected


def test_resolve_failure_is_hard():
    rec = Recorder(fail={"/v1/clients/resolve"})
    with pytest.raises(BridgeError):
        run(bridge_for(rec).resolve_client("t1", "+971500000000"))


def test_open_conversation_shape():
    rec = Recorder()
    conv = run(bridge_for(rec).open_conversation("t1", "client-1", "session-9"))
    assert conv["id"] == "conv-1"
    path, body = rec.calls[0]
    assert path == "/v1/conversations"
    # VOICE channel + deterministic external_ref; Phase 22 server-side
    # threading get-or-creates on (client, channel), so repeat calls
    # continue one thread.
    assert body["channel"] == "VOICE"
    assert body["external_ref"] == "voice:session-9"


def test_open_conversation_failure_is_hard():
    rec = Recorder(fail={"/v1/conversations"})
    with pytest.raises(BridgeError):
        run(bridge_for(rec).open_conversation("t1", "client-1", "session-9"))
