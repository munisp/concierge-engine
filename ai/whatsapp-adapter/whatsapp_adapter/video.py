"""Video-note intake: WhatsApp video -> keyframes -> OCR -> text.

Video notes carry their information visually (a boarding pass held to the
camera, a quick pan of a document). Strategy: ffmpeg samples frames at
1 fps (capped by CE_VIDEO_MAX_FRAMES), each frame goes through the Phase 7
OCR provider chain, lines are deduped across frames, and the result joins
the conversation/agent/intake flow with engine provenance.

Honest degrade, as everywhere: no ffmpeg -> engine "none" with the reason;
non-video mimes are explicitly unsupported; OCR failure on every frame
yields empty text, never an exception.
"""

from __future__ import annotations

import os
import shutil
import subprocess
import tempfile
from typing import Any

from .ocr import ocr_image

MAX_FRAMES = int(os.getenv("CE_VIDEO_MAX_FRAMES", "4"))

_MIME_EXT = {
    "video/mp4": ".mp4",
    "video/quicktime": ".mov",
    "video/3gpp": ".3gp",
    "video/webm": ".webm",
}


def video_to_text(data: bytes, mime: str = "") -> dict[str, Any]:
    """Extract text from video bytes via keyframe OCR. Never raises."""
    if not data:
        return {"text": "", "engine": "none", "detail": "empty media"}
    if mime and not mime.startswith("video/"):
        return {"text": "", "engine": "none", "detail": f"unsupported media type {mime}"}
    if not shutil.which("ffmpeg"):
        return {"text": "", "engine": "none", "detail": "ffmpeg not installed"}

    ext = _MIME_EXT.get(mime.split(";")[0].strip(), ".mp4")
    video_path, frame_dir = "", ""
    try:
        with tempfile.NamedTemporaryFile(suffix=ext, delete=False, dir="/tmp") as fh:
            fh.write(data)
            video_path = fh.name
        frame_dir = tempfile.mkdtemp(prefix="ce-frames-", dir="/tmp")
        proc = subprocess.run(
            ["ffmpeg", "-y", "-v", "error", "-i", video_path,
             "-vf", "fps=1", "-frames:v", str(MAX_FRAMES),
             os.path.join(frame_dir, "f%02d.png")],
            capture_output=True, timeout=60, check=False)
        if proc.returncode != 0:
            return {"text": "", "engine": "none",
                    "detail": "ffmpeg frame extraction failed: "
                              + proc.stderr.decode(errors="replace")[:200]}

        lines: list[str] = []
        engine = ""
        for name in sorted(os.listdir(frame_dir)):
            if not name.endswith(".png"):
                continue
            with open(os.path.join(frame_dir, name), "rb") as fh:
                frame = fh.read()
            out = ocr_image(frame, "image/png")
            if out["engine"] != "none":
                engine = out["engine"]
            for line in out["text"].splitlines():
                line = line.strip()
                if line and line not in lines:  # same text persists across frames
                    lines.append(line)
        if not lines:
            return {"text": "", "engine": "none", "detail": "no readable text in sampled frames"}
        return {"text": "\n".join(lines),
                "engine": f"video-keyframes+{engine or 'unknown'}",
                "detail": f"{min(len(os.listdir(frame_dir)), MAX_FRAMES)} frames sampled"}
    except Exception as exc:
        return {"text": "", "engine": "none",
                "detail": f"video pipeline failed: {exc.__class__.__name__}"}
    finally:
        for p in (video_path,):
            if p:
                try:
                    os.unlink(p)
                except OSError:
                    pass
        if frame_dir:
            shutil.rmtree(frame_dir, ignore_errors=True)
