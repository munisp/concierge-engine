"""Inbound intake-loop tests: every WhatsApp message is offered to core-api's
case-intake policy, idempotency keys are stable, and intake failure never
breaks the message flow."""

import asyncio
from unittest.mock import patch

from whatsapp_adapter.main import WebhookMessage, _stable_idem, inbound


class _Resp:
    def __init__(self, payload):
        self._payload = payload

    def raise_for_status(self):
        pass

    def json(self):
        return self._payload


class _FakeCore:
    """Records calls; serves resolve/conversation/message/intake."""

    def __init__(self, fail_intake=False):
        self.calls = []
        self.fail_intake = fail_intake

    async def post(self, path, json=None):
        self.calls.append((path, json))
        if path.endswith("/cases/intake") and self.fail_intake:
            raise RuntimeError("core-api down")
        if path.endswith("/clients/resolve"):
            return _Resp({"client": {"id": "cl-1"}})
        if path.endswith("/conversations"):
            return _Resp({"id": "conv-1", "state": "CAPTURING"})
        if path.endswith("/cases/intake"):
            return _Resp({"opened": True, "kind": "incident", "priority": "P2",
                          "engine": "core-rules", "reason": "warrants accountable follow-up"})
        return _Resp({})


class _FakeAgent:
    async def post(self, url, json=None):
        return _Resp({"reply": "On it.", "state": "CAPTURING"})


class _Ctx:
    def __init__(self, impl):
        self._impl = impl

    async def __aenter__(self):
        return self._impl

    async def __aexit__(self, *a):
        return False


def _run(msg, core):
    """Run inbound() with httpx.AsyncClient faked: first client is core-api,
    second is the concierge-agent."""
    agent = _FakeAgent()
    calls = {"n": 0}

    def pick(**kw):
        calls["n"] += 1
        return _Ctx(core if calls["n"] == 1 else agent)

    with patch("httpx.AsyncClient", side_effect=pick):
        return asyncio.run(inbound(msg))


def test_intake_called_with_stable_idem():
    core = _FakeCore()
    msg = WebhookMessage(tenant_id="t1", contact="+31612345678",
                         text="my driver never showed up, refund please",
                         external_ref="wamid.123")
    out = _run(msg, core)
    intake_calls = [c for c in core.calls if c[0].endswith("/cases/intake")]
    assert len(intake_calls) == 1
    body = intake_calls[0][1]
    assert body["idempotency_key"] == "wa:wamid.123"
    assert body["client_id"] == "cl-1" and body["conversation_id"] == "conv-1"
    assert out["intake"]["opened"] is True


def test_intake_fallback_idem_when_no_external_ref():
    core = _FakeCore()
    msg = WebhookMessage(tenant_id="t1", contact="+31612345678", text="hello")
    _run(msg, core)
    body = [c for c in core.calls if c[0].endswith("/cases/intake")][0][1]
    assert body["idempotency_key"] == _stable_idem("t1", "+31612345678", "hello")
    # stable: same input, same key
    assert _stable_idem("t1", "+31612345678", "hello") == _stable_idem("t1", "+31612345678", "hello")


def test_intake_failure_does_not_break_message_flow():
    core = _FakeCore(fail_intake=True)
    msg = WebhookMessage(tenant_id="t1", contact="+31612345678",
                         text="safety issue", external_ref="wamid.9")
    out = _run(msg, core)
    # agent reply still delivered, intake reported as unavailable
    assert out["agent"]["reply"] == "On it."
    assert out["intake"] == {"opened": False, "reason": "intake unavailable"}
