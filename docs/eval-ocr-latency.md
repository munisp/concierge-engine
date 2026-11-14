# OCR latency eval — WhatsApp media intake (Phase 7a gate)

Question: can media OCR join the synchronous WhatsApp intake loop without
blowing the response-time budget, and which engine carries which
environment?

## Measured: tesseract 5.3.0, sandbox CPU (2 cores, shared)

| Input | Median latency | Extraction |
|---|---|---|
| single-line PNG 900x220 | 0.14 s | exact |
| half-page PNG 1240x900 (5 lines) | 0.39 s | exact |
| full-page PNG 2480x1754 (10 lines, A4-ish) | 0.74 s | exact |

Verdict: tesseract sits comfortably inside the webhook budget (the
adapter's agent-turn timeout alone is 30 s). It is the dev/pilot default
engine — zero network, zero cost, honest labels.

## MiniCPM-OCR 1.3B — not sandbox-hostable, interface ready

Same resource reality as the qwen3:8b eval (`docs/eval-qwen3-ladder.md`):
a 1.3B vision model wants ~2–4 GB of weights+KV on CPU, and this sandbox
has 3.9 GB total where the 4B Qwen already triggered an OOM recycle.
Running MiniCPM-OCR here would measure the sandbox, not the model.

What shipped instead (`whatsapp_adapter/ocr.py`):

- `CE_OCR_URL` provider chain — point it at any MiniCPM-OCR HTTP
  deployment (or compatible service) and the adapter prefers it, labeled
  `minicpm-ocr`; any failure falls through to local tesseract, labeled
  `tesseract`; total failure degrades to `none` and the message flow
  continues advisory.
- Real extraction test in CI (`test_tesseract_extracts_text_real_image`)
  so the local engine can never rot silently.
- Latency bound asserted at 30 s smoke; measured values above.

Activation gate (unchanged from the Phase 6 assessment): provision the
OCR service on target hardware, run this same battery against it, and if
p95 lands inside the webhook budget, set `CE_OCR_URL` per-tenant. No code
change needed — it is config.
