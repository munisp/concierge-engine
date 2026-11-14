# Phase 8 — WhatsApp voice-note transcription intake

User-directed wave: the next media type after Phase 7's images — audio
voice notes — through the same provenance-labeled, advisory, idempotent
pipeline. (The other two candidates — MiniCPM-OCR model activation and
Sedona cluster — remain hardware/volume-gated; nothing to execute.)

## WhatsApp audio → transcription → intake loop

- New module `whatsapp_adapter/transcribe.py`, mirroring the OCR design:
  - Provider chain, engine-labeled, never raises: `CE_TRANSCRIBE_URL`
    (whisper.cpp server or any whisper-compatible endpoint) → local
    faster-whisper → honest `none`. Non-audio mimes are explicitly
    unsupported.
  - Local engine: faster-whisper `tiny`, int8, CPU. Model source is
    `CE_WHISPER_MODEL`, else the persisted snapshot at
    `/mnt/agents/persist/models/faster-whisper-tiny` (survives sandbox
    wipes, no hub download), else the hub name.
  - Media download reuses the Phase 7 Meta two-step.
- `inbound()` media block now routes by mime: `audio/*` → transcription,
  everything else → OCR. Extracted text enters conversation + agent turn +
  case intake inline, labeled `[voice note via faster-whisper/tiny]: ...`.
  Idempotency unchanged (`wa:<message-id>`).
- Meta webhook routes `audio` messages (voice notes have no caption —
  the transcript IS the message).
- `scripts/bootstrap-toolchain.sh` restores the optional local engine
  (`faster-whisper` + `av==13.*` — av 14+ breaks faster-whisper).

## Latency eval — `docs/eval-voice-latency.md`

faster-whisper tiny on 2-core CPU: 11 s of speech transcribed in 0.6–0.7 s
(~15x real-time), exact on the standard JFK fixture, ogg/opus (the
WhatsApp voice-note wire format) validated. whisper.cpp / larger models
activate by config (`CE_TRANSCRIBE_URL` / `CE_WHISPER_MODEL`) once bigger
hardware exists — same gate pattern as MiniCPM-OCR and qwen3:8b.

## Tests — 6 new (`tests/test_audio.py`), suite 26/26

- Real transcription of the JFK fixture (opt-in where faster-whisper is
  installed; sandbox-validated including ogg/opus).
- HTTP provider preference + engine labeling.
- Non-audio mime → honest unsupported.
- Total failure → `engine: "none"`, empty text, no raise.
- End-to-end voice note → intake with provenance + idempotency assertions.
- Meta webhook audio routing.

CI note: the model tests are opt-in (skip-gated like the Sedona parity
test); CI runs the provider-chain/routing/honesty tests plus the Phase 7
tesseract real-extraction gate.

## Verification

- whatsapp-adapter: 26/26 (6 new)
- concierge-agent 26/26, analytics 13+1 opt-in, Go suites, console:
  untouched this wave, last verified green
