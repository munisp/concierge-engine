"""Video-note tests: real ffmpeg keyframe extraction + OCR (doubles as the
pipeline smoke for docs), honest degrade paths, end-to-end video intake,
and Meta webhook video routing."""

import asyncio
import shutil
import subprocess
from pathlib import Path
from unittest.mock import patch

import pytest

from whatsapp_adapter import video
from whatsapp_adapter.main import WebhookMessage, inbound

FONT = "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"

needs_ffmpeg = pytest.mark.skipif(
    shutil.which("ffmpeg") is None or shutil.which("tesseract") is None
    or not Path(FONT).exists(),
    reason="ffmpeg/tesseract/dejavu not installed (CI installs them)")


def _make_video(text: str = "FLIGHT EK001 CONFIRMED") -> bytes:
    """3-second white card with bold text — the 'boarding pass held to the
    camera' fixture."""
    out = "/tmp/ce-test-note.mp4"
    subprocess.run(
        ["ffmpeg", "-y", "-v", "error", "-f", "lavfi",
         "-i", f"color=c=white:s=900x300:d=3,drawtext=fontfile={FONT}:"
               f"text='{text}':fontsize=56:fontcolor=black:x=40:y=120",
         "-c:v", "libx264", "-pix_fmt", "yuv420p", out],
        check=True, capture_output=True)
    return Path(out).read_bytes()


@needs_ffmpeg
def test_video_keyframe_ocr_real():
    out = video.video_to_text(_make_video(), "video/mp4")
    assert out["engine"].startswith("video-keyframes+")
    assert "FLIGHT" in out["text"] and "CONFIRMED" in out["text"]


def test_video_non_video_mime_honest_unsupported():
    out = video.video_to_text(b"\x89PNG-fake", "image/png")
    assert out["engine"] == "none"
    assert "unsupported media type" in out["detail"]


def test_video_ffmpeg_missing_honest():
    with patch("shutil.which", return_value=None):
        out = video.video_to_text(b"fake-video", "video/mp4")
    assert out["engine"] == "none"
    assert "ffmpeg not installed" in out["detail"]


# --- end-to-end video intake flow --------------------------------------------

from test_intake import _Ctx, _FakeAgent, _FakeCore  # noqa: E402


@needs_ffmpeg
def test_inbound_video_feeds_intake():
    core = _FakeCore()
    agent = _FakeAgent()
    calls = {"n": 0}

    def pick(**kw):
        calls["n"] += 1
        return _Ctx(core if calls["n"] == 1 else agent)

    async def fake_fetch(media_id, token=None, graph_base=""):
        return _make_video(), "video/mp4"

    msg = WebhookMessage(tenant_id="t1", contact="+31612345678",
                         text="here is my ticket", external_ref="wamid.vid1",
                         media_id="media-vid1", media_mime="video/mp4")
    with patch("httpx.AsyncClient", side_effect=pick), \
            patch("whatsapp_adapter.ocr.fetch_meta_media", fake_fetch):
        out = asyncio.run(inbound(msg))
    intake_body = [c for c in core.calls if c[0].endswith("/cases/intake")][0][1]
    assert "[video note OCR via video-keyframes+" in intake_body["text"]
    assert "FLIGHT" in intake_body["text"]
    assert intake_body["idempotency_key"] == "wa:wamid.vid1"
    assert out["media"]["kind"] == "video note OCR"
    assert out["agent"]["reply"] == "On it."


def test_meta_inbound_routes_video_messages():
    from whatsapp_adapter import main as adapter_main

    payload = {"entry": [{"changes": [{"value": {
        "metadata": {"phone_number_id": "pn-1"},
        "contacts": [{"wa_id": "31612345678", "profile": {"name": "Ada"}}],
        "messages": [{"from": "31612345678", "id": "wamid.vid9", "type": "video",
                      "video": {"id": "media-v9", "mime_type": "video/mp4",
                                "caption": "my pass"}}],
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
    assert seen[0].media_id == "media-v9"
    assert seen[0].media_mime == "video/mp4"
    assert seen[0].text == "my pass"
