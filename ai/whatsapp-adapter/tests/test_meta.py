"""Meta Cloud API adapter tests: payload shapes, signature verification
(the webhook ingress must reject forgeries in constant time), the challenge
handshake, and tenant routing from phone_number_id."""

import asyncio
import hashlib
import hmac
import json

import httpx
import pytest

from whatsapp_adapter.channel_port import BSPConfig, adapter_for
from whatsapp_adapter.meta import (GRAPH_BASE, MetaCloudAdapter, verify_challenge,
                                   verify_meta_signature)


def cfg() -> BSPConfig:
    return BSPConfig(provider="meta_direct", waba_id="waba-1",
                     phone_id="123456789", api_key="EAAG-token")


def sign(secret: str, body: bytes) -> str:
    return "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()


def test_signature_roundtrip_and_tamper_rejection():
    secret, body = "app-secret-1", b'{"entry":[]}'
    good = sign(secret, body)
    assert verify_meta_signature(secret, body, good)
    assert not verify_meta_signature(secret, b'{"entry":[{"x":1}]}', good)
    assert not verify_meta_signature("other-secret", body, good)
    assert not verify_meta_signature(secret, body, "sha1=" + good[7:])
    assert not verify_meta_signature("", body, good)   # missing secret never passes
    assert not verify_meta_signature(secret, body, "")  # missing header never passes


def test_challenge_handshake():
    assert verify_challenge("subscribe", "tok", "abc123", "tok") == "abc123"
    assert verify_challenge("subscribe", "wrong", "abc123", "tok") is None
    assert verify_challenge("unsubscribe", "tok", "abc123", "tok") is None
    assert verify_challenge("subscribe", "tok", "abc123", "") is None


def test_adapter_registered():
    assert isinstance(adapter_for(cfg()), MetaCloudAdapter)


class FakeTransport(httpx.AsyncBaseTransport):
    """Records requests and answers like the Graph API."""

    def __init__(self):
        self.requests = []

    async def handle_async_request(self, request):
        self.requests.append(request)
        return httpx.Response(200, json={"messages": [{"id": "wamid.1"}]})


def _post_with_transport(adapter, transport):
    original = httpx.AsyncClient

    class Client(httpx.AsyncClient):
        def __init__(self, *a, **kw):
            kw["transport"] = transport
            super().__init__(*a, **kw)

    httpx.AsyncClient = Client
    try:
        return asyncio.run(adapter.send_freeform(cfg(), "+31612345678", "hello"))
    finally:
        httpx.AsyncClient = original


def test_freeform_payload_shape():
    t = FakeTransport()
    out = _post_with_transport(MetaCloudAdapter(), t)
    assert t.requests, "no request sent"
    req = t.requests[0]
    assert req.url.path == "/v21.0/123456789/messages"
    assert req.headers["authorization"] == "Bearer EAAG-token"
    payload = json.loads(req.content)
    assert payload["messaging_product"] == "whatsapp"
    assert payload["type"] == "text"
    assert payload["text"]["body"] == "hello"
    assert payload["to"] == "+31612345678"
    assert out["ok"] and out["status"] == 200


def test_template_payload_components():
    t = FakeTransport()
    adapter = MetaCloudAdapter()
    original = httpx.AsyncClient

    class Client(httpx.AsyncClient):
        def __init__(self, *a, **kw):
            kw["transport"] = t
            super().__init__(*a, **kw)

    httpx.AsyncClient = Client
    try:
        asyncio.run(adapter.send_template(cfg(), "+31612345678",
                                          "booking_confirmed", ["BK-9"]))
    finally:
        httpx.AsyncClient = original
    payload = json.loads(t.requests[0].content)
    assert payload["type"] == "template"
    assert payload["template"]["name"] == "booking_confirmed"
    params = payload["template"]["components"][0]["parameters"]
    assert params == [{"type": "text", "text": "BK-9"}]


def test_graph_base_override():
    a = MetaCloudAdapter(graph_base="https://graph.example.test/v99/")
    c = cfg()
    assert a._url(c) == "https://graph.example.test/v99/123456789/messages"
    assert MetaCloudAdapter()._url(c).startswith(GRAPH_BASE + "/")
