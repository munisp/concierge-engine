"""Voice-note transcription tests: provider chain (HTTP service -> local
faster-whisper -> honest none), advisory failure semantics, end-to-end
audio intake flow, and a real whisper-tiny transcription of the JFK speech
fixture (the same fixture openai/whisper uses in its own test suite) that
doubles as the CPU latency smoke for docs/eval-voice-latency.md."""

import asyncio
import importlib.util
import time
from pathlib import Path
from unittest.mock import patch

import pytest

from whatsapp_adapter import transcribe
from whatsapp_adapter.main import WebhookMessage, inbound

FIXTURE = Path(__file__).parent / "fixtures" / "jfk.flac"

needs_whisper = pytest.mark.skipif(
    importlib.util.find_spec("faster_whisper") is None or not FIXTURE.exists(),
    reason="faster-whisper or the JFK fixture not installed "
           "(validated engine config; opt-in like Sedona)")


@pytest.fixture(autouse=True)
def _no_http_provider(monkeypatch):
    """Default: no external transcription service unless a test wires one."""
    monkeypatch.setattr(transcribe, "TRANSCRIBE_URL", "")
    monkeypatch.setattr(transcribe, "TRANSCRIBE_API_KEY", "")


@needs_whisper
def test_whisper_tiny_transcribes_real_speech():
    data = FIXTURE.read_bytes()
    t0 = time.perf_counter()
    out = transcribe.transcribe_audio(data, "audio/flac")
    elapsed = time.perf_counter() - t0
    print(f"\nfaster-whisper/tiny CPU latency: {elapsed:.2f}s for 11s of speech")
    assert out["engine"] == "faster-whisper/tiny"
    assert "ask not what your country can do for you" in out["text"].lower()
    assert elapsed < 60  # smoke bound; measured value goes to the eval doc


def test_http_provider_preferred_and_labeled(monkeypatch):
    monkeypatch.setattr(transcribe, "TRANSCRIBE_URL", "http://whisper.local/inference")

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
                    return {"text": "please confirm pickup at 6am", "engine": "whisper.cpp"}
            return _R()

    with patch("httpx.Client", _FakeHTTP):
        out = transcribe.transcribe_audio(b"OGGS-fake", "audio/ogg")
    assert out["engine"] == "whisper.cpp"
    assert out["text"] == "please confirm pickup at 6am"


def test_non_audio_mime_honest_unsupported():
    out = transcribe.transcribe_audio(b"\x89PNG-fake", "image/png")
    assert out["engine"] == "none"
    assert "unsupported media type" in out["detail"]


def test_total_failure_is_honest_none(monkeypatch):
    """No HTTP service, no local model: engine 'none', empty text, no raise."""
    monkeypatch.setattr(transcribe, "_model", None)
    with patch.dict("sys.modules", {"faster_whisper": None}):
        out = transcribe.transcribe_audio(b"RIFF-fake", "audio/wav")
    assert out["engine"] == "none"
    assert out["text"] == ""
    assert "local whisper failed" in out["detail"]


# --- end-to-end voice-note intake flow ---------------------------------------

from test_intake import _Ctx, _FakeAgent, _FakeCore  # noqa: E402


def _run_media(msg, core, media_bytes=b"", mime="audio/ogg", fetch_fails=False):
    agent = _FakeAgent()
    calls = {"n": 0}

    def pick(**kw):
        calls["n"] += 1
        return _Ctx(core if calls["n"] == 1 else agent)

    async def fake_fetch(media_id, token=None, graph_base=""):
        if fetch_fails:
            raise RuntimeError("media expired")
        return media_bytes, mime

    with patch("httpx.AsyncClient", side_effect=pick), \
            patch("whatsapp_adapter.ocr.fetch_meta_media", fake_fetch):
        return asyncio.run(inbound(msg))


@needs_whisper
def test_inbound_voice_note_feeds_intake():
    core = _FakeCore()
    msg = WebhookMessage(tenant_id="t1", contact="+31612345678",
                         text="", external_ref="wamid.v1",
                         media_id="media-v1", media_mime="audio/ogg")
    out = _run_media(msg, core, media_bytes=FIXTURE.read_bytes(), mime="audio/ogg")
    intake_body = [c for c in core.calls if c[0].endswith("/cases/intake")][0][1]
    assert "[voice note via faster-whisper/tiny]" in intake_body["text"]
    assert "ask not what your country" in intake_body["text"].lower()
    assert intake_body["idempotency_key"] == "wa:wamid.v1"
    assert out["media"]["extracted"] is True
    assert out["media"]["kind"] == "voice note"
    assert out["agent"]["reply"] == "On it."


def test_meta_inbound_routes_audio_messages():
    from whatsapp_adapter import main as adapter_main

    payload = {"entry": [{"changes": [{"value": {
        "metadata": {"phone_number_id": "pn-1"},
        "contacts": [{"wa_id": "31612345678", "profile": {"name": "Ada"}}],
        "messages": [{"from": "31612345678", "id": "wamid.au1", "type": "audio",
                      "audio": {"id": "media-a9", "mime_type": "audio/ogg; codecs=opus",
                                "voice": True}}],
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
        out = asyncio.run(adapter_main.meta_inbound(_Req()))

    assert out["processed"] == 1
    assert seen[0].media_id == "media-a9"
    assert seen[0].media_mime == "audio/ogg; codecs=opus"
    assert seen[0].tenant_id == "t1"
