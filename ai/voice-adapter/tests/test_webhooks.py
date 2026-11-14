"""LiveKit webhook → session lifecycle, incl. idempotent end."""

import asyncio

import httpx

from voice_adapter.bridge import CoreBridge
from voice_adapter.sessions import CREATED, ENDED, LIVE, SessionStore
from voice_adapter.webhooks import end_session, handle_event


class Rec:
    def __init__(self):
        self.calls = []

    def handler(self, request: httpx.Request) -> httpx.Response:
        self.calls.append(request.url.path)
        return httpx.Response(200, json={"opened": False})


def setup_session(turns=0):
    store = SessionStore()
    s = store.create("t1", "c1", "+1", "room-x", "conv-1")
    s.turns = turns
    return store, s


def bridge(rec: Rec) -> CoreBridge:
    return CoreBridge("http://core", "http://agent",
                      transport=httpx.MockTransport(rec.handler))


def run(coro):
    return asyncio.run(coro)


def test_participant_joined_flips_live():
    store, s = setup_session()
    out = run(handle_event(store, bridge(Rec()), {
        "event": "participant_joined", "room": {"name": "room-x"},
        "participant": {"identity": "client-c1"}}))
    assert out["session"] == s.id
    assert s.state == LIVE


def test_bot_join_does_not_flip_live():
    store, s = setup_session()
    out = run(handle_event(store, bridge(Rec()), {
        "event": "participant_joined", "room": {"name": "room-x"},
        "participant": {"identity": "concierge-ai"}}))
    assert out["session"] is None
    assert s.state == CREATED


def test_room_finished_ends_and_flushes_once():
    rec = Rec()
    store, s = setup_session(turns=3)
    store.transition(s, LIVE)
    out = run(handle_event(store, bridge(rec), {
        "event": "room_finished", "room": {"name": "room-x"}}))
    assert out["ended"] == [s.id]
    assert s.state == ENDED and s.end_reason == "room finished"
    assert rec.calls == ["/v1/cases/intake"]  # final flush, exactly once

    # Redelivered webhook: no second flush, no error.
    out2 = run(handle_event(store, bridge(rec), {
        "event": "room_finished", "room": {"name": "room-x"}}))
    assert out2["ended"] == []
    assert rec.calls == ["/v1/cases/intake"]


def test_zero_turn_session_end_skips_intake():
    rec = Rec()
    store, s = setup_session(turns=0)
    store.transition(s, LIVE)
    run(handle_event(store, bridge(rec), {
        "event": "room_finished", "room": {"name": "room-x"}}))
    assert s.state == ENDED
    assert rec.calls == []  # nothing said → nothing to triage


def test_unknown_event_ignored():
    store, s = setup_session()
    out = run(handle_event(store, bridge(Rec()), {"event": "track_published"}))
    assert out["handled"] is None
    assert s.state == CREATED


def test_end_session_idempotent_direct():
    rec = Rec()
    store, s = setup_session(turns=2)
    store.transition(s, LIVE)
    b = bridge(rec)
    run(end_session(store, b, s, "hangup"))
    run(end_session(store, b, s, "hangup"))
    assert rec.calls == ["/v1/cases/intake"]
