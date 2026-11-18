# Phase 51 — Pluggable OCR engine chain

## Decision

The Phase 7 OCR provider chain (`CE_OCR_URL` HTTP service → tesseract →
honest none) is generalized into a pluggable engine registry in
`ai/whatsapp-adapter/whatsapp_adapter/ocr.py`.

## Chain

`CE_OCR_CHAIN` (default `vlm,docling,paddleocr,rusto,tesseract`) selects
and orders engines. First success wins; every attempt is recorded in the
result `detail` for provenance; the result `engine` labels what actually
extracted the text. The chain never raises — final degrade is
`engine: "none"` with the full attempt log, so media flow stays advisory.

| Engine | What | Invocation | Optional dep |
|---|---|---|---|
| `vlm` | External OCR/VLM HTTP service (production target: MiniCPM-OCR) | `CE_OCR_URL` POST contract, engine name from response | none |
| `docling` | IBM Docling document converter — strongest on PDFs/structured docs | lazy `docling.document_converter` import, cached converter | `docling` |
| `paddleocr` | PaddleOCR general image/PDF OCR | lazy `paddleocr` import, cached engine; 2.x and 3.x result shapes normalized | `paddleocr` + `paddlepaddle` |
| `rusto` | RustO! pure-Rust OCR (github.com/byrizki/rusto-rs), PP-OCRv6 weights (Apache-2.0) | `rusto` Python binding if importable, else `rusto` CLI (`CE_OCR_RUSTO_BIN`); CLI score suffixes stripped | `rusto` wheel or `cargo install rusto-rs` |
| `tesseract` | Local baseline, image mimes only | subprocess | system binary |

Unsupported media raises `UnsupportedMedia` inside an engine and is skipped
without marking the chain as "supported but failed" — a media type no
enabled engine handles degrades to `unsupported media type <mime>` exactly
as before. Uninstalled/unconfigured engines raise `EngineUnavailable` and
are likewise skipped, so CI (which installs none of the heavy extras) keeps
exercising the real tesseract extraction gate unchanged.

## Packaging discipline

Heavy engines live in `ai/whatsapp-adapter/requirements-ocr.txt`,
deliberately **outside** the CI install set and the security job's
pip-audit loop (paddlepaddle/docling pull hundreds of MB and their own dep
trees). Deployments audit the extras file at install time
(`pip-audit -r requirements-ocr.txt`) in the environment that enables them.

## Tests

Hermetic chain tests cover: env-driven ordering, unknown-engine skip,
fallback provenance (both attempts named in detail), all-unavailable →
honest none, unsupported-media labeling, rusto CLI parsing with score
suffix stripping, paddleocr via a fake module with 2.x-shaped results,
docling via a fake module, and `_paddle_texts` normalization across
PaddleOCR 2.x/3.x shapes. The pre-existing real-tesseract,
HTTP-provider-preference, and end-to-end intake tests pass unmodified.

## Activation notes

- MiniCPM-OCR remains a hardware task: serve it over HTTP, set
  `CE_OCR_URL`; it is the `vlm` chain head.
- rusto default model `ppocrv6-tiny` is Apache-2.0. The optional
  `indraocr` weights are modified OpenRAIL-M (free under $20M
  funding/revenue, no competing OCR API) — check the license before
  `rusto download --model indraocr` in commercial deployments.
