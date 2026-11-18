"""Media OCR: WhatsApp image/document -> text for the intake loop.

Pluggable engine chain (live-first, honest degrade, engine labeled on every
call). Chain order is env-configurable:

    CE_OCR_CHAIN=vlm,docling,paddleocr,rusto,tesseract   (default)

Engines:

1. vlm — external OCR/VLM HTTP service at CE_OCR_URL. Production target is
   a MiniCPM-OCR deployment (see docs/integration-assessment.md): POST the
   media bytes, get back {"text": ..., "engine": ...}. Any VLM/ocr service
   with that contract slots in here.
2. docling — IBM Docling document converter (pip install docling). Strongest
   on PDFs and structured documents; also handles images. Lazy-imported,
   optional dependency.
3. paddleocr — PaddleOCR (pip install paddleocr + paddlepaddle). General
   image/PDF OCR. Lazy-imported, optional dependency. CE_OCR_PADDLE_LANG
   selects the recognition language (default "en").
4. rusto — RustO! pure-Rust OCR (github.com/byrizki/rusto-rs). Prefers the
   `rusto` Python binding (pip install rusto); falls back to the `rusto`
   CLI binary (cargo install rusto-rs) via CE_OCR_RUSTO_BIN. PP-OCRv6
   weights, Apache-2.0, CPU-friendly.
5. tesseract — local tesseract binary. Dev/pilot default, no network,
   CPU-only, image mimes only.
6. engine "none" with empty text — callers stay advisory and the message
   flow is never broken by OCR.

An engine is skipped when it is not installed/available, when it does not
support the media type, or when it errors — the next engine in the chain
takes over and the attempt is recorded in the result detail for provenance.

Media download for the direct Meta path is the documented two-step:
GET /{media-id} returns a temporary URL, GET that URL with the same
Bearer token returns the bytes.
"""

from __future__ import annotations

import os
import re
import shutil
import subprocess
import tempfile
from typing import Any, Callable, Optional

import httpx

OCR_URL = os.getenv("CE_OCR_URL", "")
OCR_API_KEY = os.getenv("CE_OCR_API_KEY", "")
OCR_TIMEOUT = float(os.getenv("CE_OCR_TIMEOUT", "20"))
META_ACCESS_TOKEN = os.getenv("CE_META_ACCESS_TOKEN", "")
INFOBIP_API_KEY = os.getenv("CE_INFOBIP_API_KEY", "")

DEFAULT_CHAIN = "vlm,docling,paddleocr,rusto,tesseract"

_MIME_EXT = {
    "image/jpeg": ".jpg",
    "image/png": ".png",
    "image/webp": ".webp",
    "image/tiff": ".tif",
    "image/bmp": ".bmp",
    "application/pdf": ".pdf",
}

_IMAGE_OR_PDF = frozenset({"application/pdf"})


class UnsupportedMedia(Exception):
    """Engine cannot handle this media type — try the next engine."""


class EngineUnavailable(Exception):
    """Engine not configured/installed on this host — skip silently."""


def _chain() -> list[str]:
    raw = os.getenv("CE_OCR_CHAIN", DEFAULT_CHAIN)
    return [e.strip() for e in raw.split(",") if e.strip()]


def _supports(mime: str, allow_pdf: bool) -> bool:
    if not mime:
        return True
    return mime.startswith("image/") or (allow_pdf and mime == "application/pdf")


def _tmp_file(data: bytes, mime: str) -> str:
    ext = _MIME_EXT.get(mime, ".img")
    fh = tempfile.NamedTemporaryFile(suffix=ext, delete=False, dir="/tmp")
    with fh:
        fh.write(data)
    return fh.name


def _cleanup(path: str) -> None:
    if path:
        try:
            os.unlink(path)
        except OSError:
            pass


# --- engine: vlm (external HTTP service, e.g. MiniCPM-OCR) ---------------------

def _ocr_http(data: bytes, mime: str) -> dict[str, Any]:
    """External OCR/VLM service (MiniCPM-OCR or compatible)."""
    if not OCR_URL:
        raise EngineUnavailable("CE_OCR_URL not configured")
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


# --- engine: docling (optional, lazy import) -----------------------------------

_docling_converter: Any = None


def _ocr_docling(data: bytes, mime: str) -> dict[str, Any]:
    if not _supports(mime, allow_pdf=True):
        raise UnsupportedMedia(f"unsupported media type {mime}")
    global _docling_converter
    try:
        from docling.document_converter import DocumentConverter
    except ImportError:
        raise EngineUnavailable("docling not installed")
    if _docling_converter is None:
        _docling_converter = DocumentConverter()
    path = ""
    try:
        path = _tmp_file(data, mime)
        result = _docling_converter.convert(path)
        text = result.document.export_to_markdown().strip()
        return {"text": text, "engine": "docling", "detail": "local"}
    finally:
        _cleanup(path)


# --- engine: paddleocr (optional, lazy import) ---------------------------------

_paddle_engine: Any = None


def _paddle_texts(result: Any) -> list[str]:
    """Normalize PaddleOCR 2.x (nested lists) and 3.x (rec_texts) outputs."""
    if isinstance(result, dict) and "rec_texts" in result:
        return [str(t) for t in result["rec_texts"]]
    texts: list[str] = []
    for page in result or []:
        if isinstance(page, dict) and "rec_texts" in page:
            texts.extend(str(t) for t in page["rec_texts"])
            continue
        for line in page or []:
            if isinstance(line, dict) and "rec_texts" in line:
                texts.extend(str(t) for t in line["rec_texts"])
                continue
            try:
                texts.append(str(line[1][0]))
            except (TypeError, IndexError, KeyError):
                continue
    return texts


def _ocr_paddle(data: bytes, mime: str) -> dict[str, Any]:
    if not _supports(mime, allow_pdf=True):
        raise UnsupportedMedia(f"unsupported media type {mime}")
    global _paddle_engine
    try:
        from paddleocr import PaddleOCR
    except ImportError:
        raise EngineUnavailable("paddleocr not installed")
    if _paddle_engine is None:
        lang = os.getenv("CE_OCR_PADDLE_LANG", "en")
        try:
            _paddle_engine = PaddleOCR(lang=lang)
        except Exception:
            _paddle_engine = PaddleOCR()  # older/newer signatures
    path = ""
    try:
        path = _tmp_file(data, mime)
        result = _paddle_engine.ocr(path)
        text = "\n".join(t for t in _paddle_texts(result) if t).strip()
        return {"text": text, "engine": "paddleocr", "detail": "local"}
    finally:
        _cleanup(path)


# --- engine: rusto (RustO! — python binding or CLI) -----------------------------

_RUSTO_LINE_SCORE = re.compile(r"\s*\(\d+(?:\.\d+)?%\)\s*$")


def _ocr_rusto(data: bytes, mime: str) -> dict[str, Any]:
    if not _supports(mime, allow_pdf=True):
        raise UnsupportedMedia(f"unsupported media type {mime}")
    try:
        import rusto  # type: ignore
    except ImportError:
        rusto = None
    path = ""
    try:
        path = _tmp_file(data, mime)
        if rusto is not None:
            with rusto.RustO.initialize() as engine:
                result = engine.detect_text(path).as_lines().run()
                items = getattr(result, "results", None) or getattr(result, "items", [])
                text = "\n".join(str(getattr(i, "text", i)) for i in items).strip()
            return {"text": text, "engine": "rusto", "detail": "local-binding"}
        binary = os.getenv("CE_OCR_RUSTO_BIN", "rusto")
        if shutil.which(binary) is None:
            raise EngineUnavailable("rusto not installed (no python binding, no CLI)")
        proc = subprocess.run(
            [binary, path], capture_output=True, timeout=OCR_TIMEOUT, check=False)
        if proc.returncode != 0:
            raise RuntimeError(proc.stderr.decode(errors="replace")[:200] or "rusto error")
        lines = [_RUSTO_LINE_SCORE.sub("", ln).strip()
                 for ln in proc.stdout.decode(errors="replace").splitlines()]
        return {"text": "\n".join(ln for ln in lines if ln).strip(),
                "engine": "rusto", "detail": "local-cli"}
    finally:
        _cleanup(path)


# --- engine: tesseract (always-available baseline) ------------------------------

def _ocr_tesseract(data: bytes, mime: str) -> dict[str, Any]:
    """Local tesseract. Image mimes only — anything else is honestly
    unsupported rather than silently mis-OCR'd."""
    if not _supports(mime, allow_pdf=False):
        raise UnsupportedMedia(f"unsupported media type {mime}")
    path = ""
    try:
        path = _tmp_file(data, mime)
        proc = subprocess.run(
            ["tesseract", path, "stdout"],
            capture_output=True, timeout=OCR_TIMEOUT, check=False)
        if proc.returncode != 0:
            raise RuntimeError(proc.stderr.decode(errors="replace")[:200] or "tesseract error")
        return {"text": proc.stdout.decode(errors="replace").strip(),
                "engine": "tesseract", "detail": "local"}
    finally:
        _cleanup(path)


# --- chain registry --------------------------------------------------------------

ENGINES: dict[str, Callable[[bytes, str], dict[str, Any]]] = {
    "vlm": _ocr_http,
    "docling": _ocr_docling,
    "paddleocr": _ocr_paddle,
    "rusto": _ocr_rusto,
    "tesseract": _ocr_tesseract,
}


def ocr_image(data: bytes, mime: str = "") -> dict[str, Any]:
    """Extract text from media bytes. Never raises — advisory by design.

    Walks CE_OCR_CHAIN in order; the first engine that succeeds wins and its
    name is recorded in the result. Failed/unsupported attempts accumulate in
    the detail string so operators can see the full degrade path."""
    if not data:
        return {"text": "", "engine": "none", "detail": "empty media"}
    attempts: list[str] = []
    supported_any = False
    for name in _chain():
        fn = ENGINES.get(name)
        if fn is None:
            attempts.append(f"{name}: unknown engine")
            continue
        try:
            return fn(data, mime)
        except UnsupportedMedia as exc:
            attempts.append(f"{name}: {exc}")
        except EngineUnavailable as exc:
            attempts.append(f"{name}: {exc}")
        except Exception as exc:
            supported_any = True  # engine could have handled it, but failed
            attempts.append(f"{name} failed: {exc.__class__.__name__}")
    detail = "; ".join(attempts)
    if not supported_any and mime:
        detail = f"unsupported media type {mime} ({detail})" if detail \
            else f"unsupported media type {mime}"
    return {"text": "", "engine": "none", "detail": detail}


# --- media fetch (Meta / Infobip) -----------------------------------------------

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


async def fetch_media(ref: str, mime_hint: str = "") -> tuple[bytes, str]:
    """Provider-agnostic media fetch: Infobip hands us a download URL,
    Meta hands us a media id for the two-step flow."""
    if ref.startswith("http://") or ref.startswith("https://"):
        return await fetch_url_media(ref, mime_hint)
    return await fetch_meta_media(ref)


async def fetch_url_media(url: str, mime_hint: str = "") -> tuple[bytes, str]:
    """Direct media URL download (Infobip content.url). The mime comes from
    the response Content-Type; the hint only applies when the server is
    silent."""
    headers = {"Authorization": f"App {INFOBIP_API_KEY}"} if INFOBIP_API_KEY else None
    async with httpx.AsyncClient(timeout=20.0) as client:
        r = await client.get(url, headers=headers)
        r.raise_for_status()
        mime = r.headers.get("content-type", "").split(";")[0].strip() or mime_hint
        return r.content, mime
