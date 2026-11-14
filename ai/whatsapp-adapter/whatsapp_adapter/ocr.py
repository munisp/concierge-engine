"""Media OCR: WhatsApp image/document -> text for the intake loop.

Provider chain (live-first, honest degrade, engine labeled on every call):

1. CE_OCR_URL — external OCR HTTP service. Production target is a
   MiniCPM-OCR deployment (see docs/integration-assessment.md): POST the
   media bytes, get back {"text": ...}. Any failure falls through.
2. Local tesseract — dev/pilot default, no network, CPU-only.
3. engine "none" with empty text — callers stay advisory and the message
   flow is never broken by OCR.

Media download for the direct Meta path is the documented two-step:
GET /{media-id} returns a temporary URL, GET that URL with the same
Bearer token returns the bytes.
"""

from __future__ import annotations

import os
import subprocess
import tempfile
from typing import Any, Optional

import httpx

OCR_URL = os.getenv("CE_OCR_URL", "")
OCR_API_KEY = os.getenv("CE_OCR_API_KEY", "")
OCR_TIMEOUT = float(os.getenv("CE_OCR_TIMEOUT", "20"))
META_ACCESS_TOKEN = os.getenv("CE_META_ACCESS_TOKEN", "")

_MIME_EXT = {
    "image/jpeg": ".jpg",
    "image/png": ".png",
    "image/webp": ".webp",
    "image/tiff": ".tif",
    "image/bmp": ".bmp",
    "application/pdf": ".pdf",
}


def ocr_image(data: bytes, mime: str = "") -> dict[str, Any]:
    """Extract text from media bytes. Never raises — advisory by design."""
    if not data:
        return {"text": "", "engine": "none", "detail": "empty media"}
    if OCR_URL:
        try:
            return _ocr_http(data, mime)
        except Exception as exc:  # fall through to local engine
            http_detail = f"ocr service unavailable: {exc.__class__.__name__}"
        else:  # pragma: no cover - _ocr_http always returns or raises
            http_detail = "ocr service unavailable"
    else:
        http_detail = "CE_OCR_URL not configured"
    try:
        return _ocr_tesseract(data, mime)
    except Exception as exc:
        return {"text": "", "engine": "none",
                "detail": f"{http_detail}; tesseract failed: {exc.__class__.__name__}"}


def _ocr_http(data: bytes, mime: str) -> dict[str, Any]:
    """External OCR service (MiniCPM-OCR or compatible)."""
    headers = {"Authorization": f"Bearer {OCR_API_KEY}"} if OCR_API_KEY else {}
    with httpx.Client(timeout=OCR_TIMEOUT) as client:
        r = client.post(
            OCR_URL,
            files={"file": (f"media{_MIME_EXT.get(mime, '.bin')}", data, mime)},
            headers=headers or None,
        )
        r.raise_for_status()
        body = r.json()
    return {"text": body.get("text", "").strip(),
            "engine": body.get("engine", "minicpm-ocr"),
            "detail": "http"}


def _ocr_tesseract(data: bytes, mime: str) -> dict[str, Any]:
    """Local tesseract. Image mimes only — anything else is honestly
    unsupported rather than silently mis-OCR'd."""
    if mime and not mime.startswith("image/"):
        return {"text": "", "engine": "none", "detail": f"unsupported media type {mime}"}
    ext = _MIME_EXT.get(mime, ".img")
    path = ""
    try:
        with tempfile.NamedTemporaryFile(suffix=ext, delete=False, dir="/tmp") as fh:
            fh.write(data)
            path = fh.name
        proc = subprocess.run(
            ["tesseract", path, "stdout"],
            capture_output=True, timeout=OCR_TIMEOUT, check=False)
        if proc.returncode != 0:
            raise RuntimeError(proc.stderr.decode(errors="replace")[:200] or "tesseract error")
        return {"text": proc.stdout.decode(errors="replace").strip(),
                "engine": "tesseract", "detail": "local"}
    finally:
        if path:
            try:
                os.unlink(path)
            except OSError:
                pass


async def fetch_meta_media(media_id: str, token: Optional[str] = None,
                           graph_base: str = "https://graph.facebook.com/v21.0"
                           ) -> tuple[bytes, str]:
    """Meta two-step media download: id -> temporary URL -> bytes.
    Returns (data, mime_type)."""
    token = token if token is not None else META_ACCESS_TOKEN
    if not token:
        raise RuntimeError("no Meta access token configured")
    auth = {"Authorization": f"Bearer {token}"}
    async with httpx.AsyncClient(timeout=20.0) as client:
        node = await client.get(f"{graph_base.rstrip('/')}/{media_id}", headers=auth)
        node.raise_for_status()
        info = node.json()
        url, mime = info.get("url", ""), info.get("mime_type", "")
        if not url:
            raise RuntimeError("media node returned no url")
        blob = await client.get(url, headers=auth)
        blob.raise_for_status()
        return blob.content, mime
