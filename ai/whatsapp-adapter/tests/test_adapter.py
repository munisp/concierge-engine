"""Tests for session mechanics, template library and send choice."""

import time

from whatsapp_adapter.channel_port import (BSPConfig, SandboxAdapter,
                                           SessionTracker, choose_send)


def test_session_window_24h():
    t = SessionTracker()
    t.touch("t1", "+31612345678")
    assert t.in_session("t1", "+31612345678")
    # simulate expiry
    t.sessions["t1:+31612345678"] = time.time() - 25 * 3600
    assert not t.in_session("t1", "+31612345678")


def test_send_choice():
    t = SessionTracker()
    assert choose_send(t, "t1", "+31612345678") == "template"  # no session yet
    t.touch("t1", "+31612345678")
    assert choose_send(t, "t1", "+31612345678") == "freeform"


def test_sandbox_adapter_records():
    import asyncio

    s = SandboxAdapter()
    cfg = BSPConfig(provider="sandbox")
    asyncio.run(s.send_template(cfg, "+31612345678", "booking_confirmed", ["BK-1"]))
    assert s.sent[0]["template"] == "booking_confirmed"


def test_session_isolation_per_tenant():
    t = SessionTracker()
    t.touch("t1", "+31612345678")
    assert not t.in_session("t2", "+31612345678")
