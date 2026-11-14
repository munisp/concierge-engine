"""Media OCR tests: provider chain (HTTP service -> local tesseract -> honest
none), advisory failure semantics, end-to-end media intake flow, and a real
tesseract extraction that doubles as the CPU latency smoke for
docs/eval-ocr-latency.md."""

import asyncio
import io
import time
from unittest.mock import patch

import pytest

from whatsapp_adapter import ocr
from whatsapp_adapter.main import WebhookMessage, inbound

FONT = "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"


def _receipt_image(text: str = "REFUND REQUEST 1400 AED") -> bytes:
    from PIL import Image, ImageDraw, ImageFont

    img = Image.new("RGB", (900, 220), "white")
    draw = ImageDraw.Draw(img)
    try:
        font = ImageFont.truetype(FONT, 64)
    except OSError:
        font = ImageFont.load_default()
    draw.text((30, 70), text, fill="black", font=font)
    buf = io.BytesIO()
    img.save(buf, format="PNG")
    return buf.getvalue()


@pytest.fixture(autouse=True)
def _no_http_provider(monkeypatch):
    """Default: no external OCR service unless a test wires one."""
    monkeypatch.setattr(ocr, "OCR_URL", "")
    monkeypatch.setattr(ocr, "OCR_API_KEY", "")


def test_tesseract_extracts_text_real_image():
    t0 = time.perf_counter()
    out = ocr.ocr_image(_receipt_image(), "image/png")
    elapsed = time.perf_counter() - t0
    print(f"\ntesseract CPU latency: {elapsed:.2f}s for 900x220 PNG")
    assert out["engine"] == "tesseract"
    assert "REFUND" in out["text"].upper()
    assert elapsed < 30  # smoke bound; measured value goes to the eval doc


def test_http_provider_preferred_and_labeled(monkeypatch):
    monkeypatch.setattr(ocr, "OCR_URL", "http://ocr.local/ocr")

    class _FakeHTTP:
        def __init__(self, *a, **kw):
            pass

        def __enter__(self):
            return self

        def __exit__(self, *a):
            return False

        def post(self, url, files=None, headers=None):
            class _R:
                def raise_for_status(self):
                    pass

                def json(self):
                    return {"text": "PNR ABC123 confirmed", "engine": "minicpm-ocr"}
            return _R()

    with patch("httpx.Client", _FakeHTTP):
        out = ocr.ocr_image(b"\x89PNG-fake", "image/png")
    assert out["engine"] == "minicpm-ocr"
    assert out["text"] == "PNR ABC123 confirmed"


def test_http_failure_falls_back_to_tesseract(monkeypatch):
    monkeypatch.setattr(ocr, "OCR_URL", "http://ocr.local/ocr")

    class _DownHTTP:
        def __init__(self, *a, **kw):
            pass

        def __enter__(self):
            return self

        def __exit__(self, *a):
            return False

        def post(self, *a, **kw):
            raise RuntimeError("connection refused")

    with patch("httpx.Client", _DownHTTP):
        out = ocr.ocr_image(_receipt_image(), "image/png")
    assert out["engine"] == "tesseract"
    assert "REFUND" in out["text"].upper()


def test_non_image_mime_honest_unsupported():
    out = ocr.ocr_image(b"%PDF-1.4 fake", "application/pdf")
    assert out["engine"] == "none"
    assert "unsupported media type" in out["detail"]


# --- end-to-end media intake flow --------------------------------------------

from test_intake import _Ctx, _FakeAgent, _FakeCore  # noqa: E402


def _run_media(msg, core, media_bytes=b"", fetch_fails=False):
    agent = _FakeAgent()
    calls = {"n": 0}

    def pick(**kw):
        calls["n"] += 1
        return _Ctx(core if calls["n"] == 1 else agent)

    async def fake_fetch(media_id, token=None, graph_base=""):
        if fetch_fails:
            raise RuntimeError("media expired")
        return media_bytes, "image/png"

    with patch("httpx.AsyncClient", side_effect=pick), \
            patch("whatsapp_adapter.main.fetch_meta_media", fake_fetch, create=True):
        # fetch_meta_media is imported inside inbound(); patch at source module
        with patch("whatsapp_adapter.ocr.fetch_meta_media", fake_fetch):
            return asyncio.run(inbound(msg))


def test_inbound_media_ocr_feeds_intake():
    core = _FakeCore()
    msg = WebhookMessage(tenant_id="t1", contact="+31612345678",
                         text="see attached", external_ref="wamid.m1",
                         media_id="media-1", media_mime="image/png")
    out = _run_media(msg, core, media_bytes=_receipt_image())
    intake_body = [c for c in core.calls if c[0].endswith("/cases/intake")][0][1]
    assert "[media OCR via tesseract]" in intake_body["text"]
    assert "REFUND" in intake_body["text"].upper()
    assert intake_body["idempotency_key"] == "wa:wamid.m1"  # same idem discipline
    assert out["media"]["extracted"] is True
    assert out["agent"]["reply"] == "On it."


def test_inbound_media_failure_is_advisory():
    core = _FakeCore()
    msg = WebhookMessage(tenant_id="t1", contact="+31612345678",
                         text="", external_ref="wamid.m2",
                         media_id="media-2", media_mime="image/png")
    out = _run_media(msg, core, fetch_fails=True)
    # flow unbroken: placeholder text reaches intake, media detail recorded
    intake_body = [c for c in core.calls if c[0].endswith("/cases/intake")][0][1]
    assert intake_body["text"] == "[media received — no text extracted]"
    assert out["media"]["ocr_engine"] == "none"
    assert "media unavailable" in out["media"]["detail"]
    assert out["agent"]["reply"] == "On it."


def test_meta_inbound_routes_image_messages():
    from whatsapp_adapter import main as adapter_main

    payload = {"entry": [{"changes": [{"value": {
        "metadata": {"phone_number_id": "pn-1"},
        "contacts": [{"wa_id": "31612345678", "profile": {"name": "Ada"}}],
        "messages": [{"from": "31612345678", "id": "wamid.img1", "type": "image",
                      "image": {"id": "media-9", "mime_type": "image/jpeg",
                                "caption": "my booking confirmation"}}],
    }}]}]}
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
        from starlette.testclient import TestClient  # noqa: F401 — ensures app import ok
        out = asyncio.run(adapter_main.meta_inbound(_Req()))

    assert out["processed"] == 1
    assert seen[0].media_id == "media-9"
    assert seen[0].media_mime == "image/jpeg"
    assert seen[0].text == "my booking confirmation"
    assert seen[0].tenant_id == "t1"
