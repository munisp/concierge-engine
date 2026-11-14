# Voice transcription latency eval — WhatsApp voice-note intake (Phase 8 gate)

Question: can voice-note transcription join the synchronous WhatsApp
intake loop, and which engine carries which environment?

## Measured: faster-whisper tiny, int8, sandbox CPU (2 cores)

| Input | Latency | Result |
|---|---|---|
| JFK speech fixture, 11.0 s FLAC | 0.7 s (+1.2 s one-time model load) | exact transcript |
| Same clip as WhatsApp voice note (ogg/opus 32k) | 0.6 s | exact transcript |
| Model load from persisted snapshot (`/mnt/agents/persist`, cold) | 26.3 s incl. page-cache cold read | then normal speed |

Verdict: ~15x real-time on CPU for the tiny model — comfortably inside the
webhook budget. Transcription quality on the standard JFK fixture is exact.

## Environment notes (honest)

- huggingface.co is not reachable from this sandbox; the model was pulled
  via `HF_ENDPOINT=https://hf-mirror.com` and persisted to
  `/mnt/agents/persist/models/faster-whisper-tiny` (75 MB). The adapter
  prefers this snapshot when `CE_WHISPER_MODEL` is unset, so wipes cost
  nothing.
- `av` must stay on 13.x: av 14+ removed the `metadata_errors` kwarg that
  faster-whisper passes to `av.open`. Pinned in
  `scripts/bootstrap-toolchain.sh`.
- The JFK fixture (`tests/fixtures/jfk.flac`) is the same file
  openai/whisper uses in its own test suite, fetched via the GitHub API
  because raw.githubusercontent.com is not reachable from the sandbox.

## whisper.cpp / larger models — activation by config

Production target is a whisper.cpp server (or any whisper-compatible HTTP
endpoint) behind `CE_TRANSCRIBE_URL` — the adapter prefers it, labeled
`whisper-http`/server-reported engine, and falls back to the local model
on any failure. Move up from `tiny` by setting `CE_WHISPER_MODEL`
(`base`/`small` once a GPU or bigger CPU box exists — same hardware gate
pattern as MiniCPM-OCR and qwen3:8b).

Tests: real-extraction gate runs wherever faster-whisper + the fixture
exist (sandbox: green); CI exercises the provider-chain, honesty, and
routing tests and skips the model tests — same opt-in pattern as Sedona.
