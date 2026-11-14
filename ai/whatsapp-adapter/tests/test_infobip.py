"""Infobip inbound parity tests: the BSP webhook normalizes text, media
(direct URL download with App-key auth), and location into the same
WebhookMessage pipeline as the Meta path."""

import asyncio
from unittest.mock import patch

import pytest

from whatsapp_adapter import ocr
from whatsapp_adapter.main import WebhookMessage, infobip_inbound


class _Req:
    def __init__(self, payload):
        self._payload = payload

    async def json(self):
        return self._payload


def _run_infobip(results, env=None):
    seen = []

    async def fake_inbound(msg):
        seen.append(msg)
        return {"ok": True}

    merged = {"CE_INFOBIP_DEFAULT_TENANT": "t1"}
    merged.update(env or {})
    with patch("whatsapp_adapter.main.inbound", fake_inbound), \
            patch.dict("os.environ", merged):
        out = asyncio.run(infobip_inbound(_Req({"results": results})))
    return out, seen


def test_infobip_text_normalized():
    out, seen = _run_infobip([{
        "from": "31612345678", "to": "31687654321", "messageId": "ib-1",
        "message": {"type": "TEXT", "text": "need a car at 6am"},
    }])
    assert out["processed"] == 1
    assert seen[0].text == "need a car at 6am"
    assert seen[0].external_ref == "ib-1"  # same wa: idempotency downstream
    assert seen[0].tenant_id == "t1"


def test_infobip_media_uses_direct_url():
    out, seen = _run_infobip([{
        "from": "31612345678", "to": "31687654321", "messageId": "ib-2",
        "message": {"type": "IMAGE",
                    "url": "https://api.infobip.com/media/xyz",
                    "caption": "booking confirmation"},
    }])
    assert out["processed"] == 1
    assert seen[0].media_id == "https://api.infobip.com/media/xyz"
    assert seen[0].text == "booking confirmation"


def test_infobip_location_normalized():
    out, seen = _run_infobip([{
        "from": "31612345678", "to": "31687654321", "messageId": "ib-3",
        "message": {"type": "LOCATION", "latitude": 6.4281, "longitude": 3.4219,
                    "name": "Eko Hotel"},
    }])
    assert out["processed"] == 1
    assert seen[0].latitude == 6.4281
    assert seen[0].location_name == "Eko Hotel"


def test_infobip_tenant_map_by_receiver():
    out, seen = _run_infobip(
        [{"from": "31612345678", "to": "31687654321", "messageId": "ib-4",
          "message": {"type": "TEXT", "text": "hi"}}],
        env={"CE_INFOBIP_TENANT_MAP": "31687654321:t2", "CE_INFOBIP_DEFAULT_TENANT": "t1"})
    assert seen[0].tenant_id == "t2"


def test_infobip_unknown_type_skipped_honestly():
    out, seen = _run_infobip([{
        "from": "31612345678", "to": "x", "messageId": "ib-5",
        "message": {"type": "STICKER", "url": "https://x/y"},
    }])
    assert out["processed"] == 0
    assert seen == []


def test_infobip_no_tenant_skipped():
    with patch.dict("os.environ", {"CE_INFOBIP_DEFAULT_TENANT": "", "CE_INFOBIP_TENANT_MAP": ""}):
        seen = []

        async def fake_inbound(msg):
            seen.append(msg)
            return {"ok": True}

        with patch("whatsapp_adapter.main.inbound", fake_inbound):
            out = asyncio.run(infobip_inbound(_Req({"results": [{
                "from": "31612345678", "to": "31687654321", "messageId": "ib-6",
                "message": {"type": "TEXT", "text": "hi"}}]})))
    assert out["processed"] == 0


# --- direct-URL media fetch ---------------------------------------------------

def test_fetch_url_media_sends_app_key_and_reads_mime(monkeypatch):
    monkeypatch.setattr(ocr, "INFOBIP_API_KEY", "k-123")
    captured = {}

    class _R:
        content = b"img-bytes"
        headers = {"content-type": "image/png; charset=binary"}

        def raise_for_status(self):
            pass

    class _FakeHTTP:
        def __init__(self, *a, **kw):
            pass

        async def __aenter__(self):
            return self

        async def __aexit__(self, *a):
            return False

        async def get(self, url, headers=None):
            captured["url"] = url
            captured["headers"] = headers
            return _R()

    with patch("httpx.AsyncClient", _FakeHTTP):
        data, mime = asyncio.run(ocr.fetch_media("https://api.infobip.com/media/xyz"))
    assert data == b"img-bytes"
    assert mime == "image/png"
    assert captured["headers"] == {"Authorization": "App k-123"}


def test_fetch_media_dispatches_meta_ids_to_two_step(monkeypatch):
    called = {}

    async def fake_meta(media_id, token=None, graph_base=""):
        called["id"] = media_id
        return b"meta-bytes", "image/jpeg"

    monkeypatch.setattr(ocr, "fetch_meta_media", fake_meta)
    data, mime = asyncio.run(ocr.fetch_media("wamid-media-9"))
    assert called["id"] == "wamid-media-9"
    assert mime == "image/jpeg"
