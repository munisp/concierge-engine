"""Voice-note transcription: WhatsApp audio -> text for the intake loop.

Provider chain (live-first, honest degrade, engine labeled on every call):

1. CE_TRANSCRIBE_URL — external transcription HTTP service. Production
   target is a whisper.cpp server (or any whisper-compatible endpoint):
   POST the audio bytes, get back {"text": ...}. Any failure falls through.
2. Local faster-whisper (optional dependency — install
   `faster-whisper` + `av==13.*`; av 14+ dropped the `metadata_errors`
   kwarg faster-whisper passes). Model from CE_WHISPER_MODEL, default
   "tiny" — measured 0.6–0.7 s for an 11 s voice note on a 2-core CPU,
   including ogg/opus (the WhatsApp voice-note format).
3. engine "none" with empty text — callers stay advisory and the message
   flow is never broken by transcription.

Media download reuses ocr.fetch_meta_media (the Meta two-step is identical
for audio).
"""

from __future__ import annotations

import os
import tempfile
from typing import Any, Optional

import httpx

TRANSCRIBE_URL = os.getenv("CE_TRANSCRIBE_URL", "")
TRANSCRIBE_API_KEY = os.getenv("CE_TRANSCRIBE_API_KEY", "")
TRANSCRIBE_TIMEOUT = float(os.getenv("CE_TRANSCRIBE_TIMEOUT", "30"))
WHISPER_MODEL = os.getenv("CE_WHISPER_MODEL", "tiny")

_MIME_EXT = {
    "audio/ogg": ".ogg",
    "audio/opus": ".ogg",
    "audio/mpeg": ".mp3",
    "audio/mp4": ".m4a",
    "audio/aac": ".aac",
    "audio/wav": ".wav",
    "audio/x-wav": ".wav",
    "audio/flac": ".flac",
}

_model: Optional[Any] = None

_PERSISTED = "/mnt/agents/persist/models/faster-whisper-tiny"


def _model_source() -> str:
    """CE_WHISPER_MODEL wins; otherwise the persisted snapshot survives
    sandbox wipes and needs no network; otherwise the hub name."""
    if os.getenv("CE_WHISPER_MODEL"):
        return WHISPER_MODEL
    if os.path.isdir(_PERSISTED):
        return _PERSISTED
    return WHISPER_MODEL


def transcribe_audio(data: bytes, mime: str = "") -> dict[str, Any]:
    """Transcribe audio bytes. Never raises — advisory by design."""
    if not data:
        return {"text": "", "engine": "none", "detail": "empty media"}
    if TRANSCRIBE_URL:
        try:
            return _transcribe_http(data, mime)
        except Exception as exc:  # fall through to local engine
            http_detail = f"transcription service unavailable: {exc.__class__.__name__}"
        else:  # pragma: no cover - _transcribe_http always returns or raises
            http_detail = "transcription service unavailable"
    else:
        http_detail = "CE_TRANSCRIBE_URL not configured"
    try:
        return _transcribe_local(data, mime)
    except Exception as exc:
        return {"text": "", "engine": "none",
                "detail": f"{http_detail}; local whisper failed: {exc.__class__.__name__}"}


def _transcribe_http(data: bytes, mime: str) -> dict[str, Any]:
    """External whisper-compatible service (whisper.cpp server et al.)."""
    headers = {"Authorization": f"Bearer {TRANSCRIBE_API_KEY}"} if TRANSCRIBE_API_KEY else {}
    with httpx.Client(timeout=TRANSCRIBE_TIMEOUT) as client:
        r = client.post(
            TRANSCRIBE_URL,
            files={"file": (f"voice{_MIME_EXT.get(mime, '.bin')}", data, mime)},
            headers=headers or None,
        )
        r.raise_for_status()
        body = r.json()
    return {"text": body.get("text", "").strip(),
            "engine": body.get("engine", "whisper-http"),
            "detail": "http"}


def _transcribe_local(data: bytes, mime: str) -> dict[str, Any]:
    """Local faster-whisper. Audio mimes only — anything else is honestly
    unsupported."""
    if mime and not mime.startswith("audio/"):
        return {"text": "", "engine": "none", "detail": f"unsupported media type {mime}"}
    global _model
    if _model is None:
        from faster_whisper import WhisperModel  # noqa: PLC0415 — optional dep

        _model = WhisperModel(_model_source(), device="cpu", compute_type="int8")
    ext = _MIME_EXT.get(mime, ".audio")
    path = ""
    try:
        with tempfile.NamedTemporaryFile(suffix=ext, delete=False, dir="/tmp") as fh:
            fh.write(data)
            path = fh.name
        segments, _info = _model.transcribe(path)
        text = " ".join(s.text for s in segments).strip()
        return {"text": text, "engine": f"faster-whisper/{WHISPER_MODEL}",
                "detail": "local"}
    finally:
        if path:
            try:
                os.unlink(path)
            except OSError:
                pass
