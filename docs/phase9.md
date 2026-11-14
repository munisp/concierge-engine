# Phase 9 — video-note OCR intake, interactive reply routing

User-directed wave: the remaining WhatsApp message types the webhook was
skipping — video notes and interactive replies.

## (a) Video notes → keyframes → OCR → intake

A video note usually carries its payload visually (a boarding pass held to
the camera, a pan across a document). New module `whatsapp_adapter/video.py`:

- `video_to_text` — ffmpeg samples frames at 1 fps (capped by
  `CE_VIDEO_MAX_FRAMES`, default 4), each frame goes through the Phase 7
  OCR provider chain, lines deduped across frames. Engine label:
  `video-keyframes+<frame engine>`.
- Honest degrade: no ffmpeg → `engine: "none"` with the reason; non-video
  mimes explicitly unsupported; unreadable frames → empty text; never
  raises, never breaks the message flow.
- `inbound()` now routes three ways by mime: `audio/*` → transcription
  (Phase 8), `video/*` → keyframe OCR, everything else → OCR (Phase 7).
  Same provenance (`[video note OCR via ...]: ...`), same `wa:<id>`
  idempotency, same advisory semantics.
- Meta webhook routes `video` messages (caption + media handle).

Tests: real pipeline gate — ffmpeg renders a 3-second text card
("FLIGHT EK001 CONFIRMED" — the boarding-pass fixture), keyframes are
extracted and OCR'd, text asserted. ffmpeg + tesseract + dejavu are all
present on CI runners, so the real gate runs in CI. Plus honesty tests
(missing ffmpeg, wrong mime), end-to-end intake with provenance and
idempotency assertions, and webhook video routing.

## (b) Interactive replies → text intake

Taps on our outbound list/button options were being dropped at the
webhook. Now:

- `interactive` messages (`button_reply` / `list_reply`) become the
  selected title with the option id kept for provenance —
  `Mercedes S-Class [option: veh-s-class]` — and enter the same
  conversation/agent/intake pipeline as typed text.
- Legacy quick-reply `button` messages same treatment.
- Replies without a title are skipped honestly (processed count excludes
  them).

This closes the loop the console's interactive outbound
(`send_interactive_list`) opened in Phase 3: structured option →
structured tap → typed-equivalent intake.

## Verification

- whatsapp-adapter: 35/35 (9 new: 5 video, 4 interactive)
- concierge-agent 26/26, analytics 13+1 opt-in, Go, console: untouched,
  last verified green
