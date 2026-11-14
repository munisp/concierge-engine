"""Message reactions (Phase 21): emoji reactions arrive as their own webhook
message type on both providers. They are conversation-timeline signal —
recorded with bracketed provenance — and explicitly skip case intake and
the agent turn (a thumbs-up is an acknowledgement, not a request)."""

import asyncio
from unittest.mock import patch


def _run_meta(payload: dict):
    from whatsapp_adapter import main as adapter_main

    seen = []

    async def fake_inbound(msg):
        seen.append(msg)
        return {"ok": True}

    class _Req:
        async def body(self):
            import json as _json
            return _json.dumps(payload).encode()

        headers = {"x-hub-signature-256": "sha256=ok"}

    with patch.object(adapter_main, "META_APP_SECRET", "s"), \
            patch("whatsapp_adapter.meta.verify_meta_signature", return_value=True), \
            patch.object(adapter_main, "inbound", fake_inbound), \
            patch.dict("os.environ", {"CE_META_TENANT_MAP": "pn-1:t1"}):
        out = asyncio.run(adapter_main.meta_inbound(_Req()))
    return out, seen


def _envelope(message: dict) -> dict:
    return {"entry": [{"changes": [{"value": {
        "metadata": {"phone_number_id": "pn-1"},
        "contacts": [{"wa_id": "31612345678", "profile": {"name": "Ada"}}],
        "messages": [message],
    }}]}]}


def test_meta_reaction_becomes_provenance_line():
    out, seen = _run_meta(_envelope({
        "from": "31612345678", "id": "wamid.r1", "type": "reaction",
        "reaction": {"message_id": "wamid.orig", "emoji": "\U0001F44D"},
    }))
    assert out["processed"] == 1
    assert seen[0].text == "[reaction: \U0001F44D]"
    assert seen[0].external_ref == "wamid.r1"
    assert seen[0].reaction is True  # intake + agent turn skipped downstream


def test_meta_reaction_removed():
    out, seen = _run_meta(_envelope({
        "from": "31612345678", "id": "wamid.r2", "type": "reaction",
        "reaction": {"message_id": "wamid.orig", "emoji": ""},
    }))
    assert out["processed"] == 1
    assert seen[0].text == "[reaction removed]"
    assert seen[0].reaction is True


def _run_infobip(results):
    from whatsapp_adapter import main as adapter_main

    seen = []

    async def fake_inbound(msg):
        seen.append(msg)
        return {"ok": True}

    class _Req:
        def __init__(self, payload):
            self._payload = payload

        async def json(self):
            return self._payload

    with patch.object(adapter_main, "inbound", fake_inbound), \
            patch.dict("os.environ", {"CE_INFOBIP_DEFAULT_TENANT": "t1"}):
        out = asyncio.run(adapter_main.infobip_inbound(_Req({"results": results})))
    return out, seen


def test_infobip_reaction_normalized():
    out, seen = _run_infobip([{
        "from": "31612345678", "to": "31687654321", "messageId": "ib-r1",
        "message": {"type": "REACTION",
                    "reaction": {"action": "REACT", "emoji": "\u2764\uFE0F",
                                 "messageId": "ib-orig"}},
    }])
    assert out["processed"] == 1
    assert seen[0].text == "[reaction: \u2764\uFE0F]"
    assert seen[0].reaction is True


def test_infobip_reaction_unreact():
    out, seen = _run_infobip([{
        "from": "31612345678", "to": "31687654321", "messageId": "ib-r2",
        "message": {"type": "REACTION",
                    "reaction": {"action": "UNREACT", "messageId": "ib-orig"}},
    }])
    assert out["processed"] == 1
    assert seen[0].text == "[reaction removed]"


def test_reaction_skips_intake_and_agent():
    """The inbound() fast-path: a reaction appends to the conversation but
    never posts to /v1/cases/intake or the agent /v1/turns."""
    from whatsapp_adapter import main as adapter_main

    calls = []

    class FakeResponse:
        def __init__(self, payload):
            self._payload = payload

        def raise_for_status(self):
            pass

        def json(self):
            return self._payload

    class FakeClient:
        def __init__(self, *a, **kw):
            self._headers = kw.get("headers", {})

        async def __aenter__(self):
            return self

        async def __aexit__(self, *a):
            return False

        async def post(self, path, json=None, **kw):
            calls.append(path)
            if path == "/v1/clients/resolve":
                return FakeResponse({"client": {"id": "c1"}})
            if path == "/v1/conversations":
                return FakeResponse({"id": "conv1", "state": "CAPTURING"})
            return FakeResponse({})

        async def get(self, path, **kw):
            calls.append(path)
            return FakeResponse({})

    with patch("httpx.AsyncClient", FakeClient):
        out = asyncio.run(adapter_main.inbound(adapter_main.WebhookMessage(
            tenant_id="t1", contact="31612345678", text="[reaction: \U0001F44D]",
            external_ref="wamid.r9", reaction=True)))

    assert out["reaction"] is True
    assert out["status"] == "recorded"
    assert "/v1/clients/resolve" in calls
    assert "/v1/conversations" in calls
    assert "/v1/conversations/conv1/messages" in calls
    assert not any("intake" in c for c in calls)   # no junk cases
    assert not any("turns" in c for c in calls)    # no agent reply to an emoji
