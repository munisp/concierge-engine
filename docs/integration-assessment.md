# Integration assessment — oxirs, terrio, MiniCPM-OCR (2026-11-14)

Evaluated against concierge-engine's actual needs: multi-tenant VIP
concierge with case management, WhatsApp intake, gold-layer analytics,
data-locality mandates (UAE/EU), self-hosted AI.

## oxirs (github.com/cool-japan/oxirs) — Rust semantic-web / knowledge-graph platform

What it is: a Rust-native SPARQL 1.1/1.2 + GraphQL knowledge graph with
TDB2 storage, GeoSPARQL, vector search, GraphRAG, W3C DID/verifiable
credentials, and ODRL data-sovereignty policies. v0.4.1, actively
developed, very large test suite.

**Real value for this platform:**

1. **Verifiable credentials (oxirs-did)** — the strongest fit. Partner and
   chauffeur credentialing (licenses, insurance, airport permits) is a
   trust bottleneck for VIP operations; VCs give tamper-proof, revocable
   proof without us holding the underlying documents. Candidate for the
   partner-vetting flow (`Status: PENDING -> VETTED`).
2. **ODRL/IDS data-sovereignty policies** — machine-checkable residency
   rules would strengthen the UAE/EU tenancy split beyond documentation.
3. **Knowledge graph + GraphRAG** — client preference graphs ("client's
   usual companion prefers...") would deepen personalization beyond the
   flat preference store; GraphRAG could enrich case context recall.

**Why we are not adopting it now:** it is a full second data platform
(RDF store, SPARQL endpoint, clustering) next to Postgres + the
lakehouse. The problems it solves best for us (credential verification,
policy expression) do not require standing up a SPARQL cluster — a narrow
VC-verification library would do. The Postgres + embeddings stack already
covers recall. **Verdict: DEFER** — trigger: first government/regulated
tenant demanding verifiable partner credentials or machine-enforced
residency policy, at which point oxirs-did (narrow) before the full stack.

## terrio (github.com/opengeos/terrio) — pure-Rust geospatial I/O

What it is: pure-Rust (no GDAL/C deps) reading/writing/converting/
reprojecting raster, vector and point-cloud formats; builds to WASM.
Early development — API may change before 1.0.

**Real value for this platform:**

1. **Tenant geodata ingestion** — airports, city authorities and large
   partners publish service-area/coverage data as Shapefile/GeoPackage/
   GeoTIFF. terrio converts those into the GeoJSON polygons our
   `service_areas` (Phase 6, PostGIS) consumes — without a GDAL C
   toolchain in deploy images.
2. **WASM edge use** — could run format conversion in the browser for the
   console's area-editor workflow.

**Why we are not adopting it now:** our current need is GeoJSON polygons
hand-drawn or pasted by operators — trivially parsed with stdlib, zero
deps. terrio earns its place the day a tenant hands us a Shapefile or
GeoPackage. **Verdict: DEFER** — trigger: first service-area import that
is not GeoJSON. Note the pre-1.0 API risk: when adopted, wrap it behind
our GeoService interface (as we did for GDS/Amadeus) so churn stays
contained.

## MiniCPM-OCR (1.3B OCR-as-systems model)

What it is: a 1.3B-parameter model treating OCR as a systems problem —
competitive document understanding at a size runnable on commodity
hardware, self-hostable.

**Real value for this platform:**

1. **WhatsApp media intake** — the adapter currently drops non-text
   messages (`media: next iteration`). VIP clients send photos: hotel
   folios, receipts, itineraries, tickets. A 1.3B OCR model on the same
   self-hosted stack (data stays in-tenant, matching the residency story)
   turns those into case-ready text.
2. **Invoice/receipt reconciliation** — OCR'd folios matched against
   ledger postings for corporate accounts.
3. **ID capture** — airport meet-and-greet and aviation bookings need
   passenger document data; on-device-class OCR avoids sending passports
   to a cloud OCR API.

**Verdict: PICK-UP CANDIDATE (Phase 7)** — concrete integration path:
adapter accepts image messages → OCR service (MiniCPM-OCR via Ollama-style
serving) → extracted text flows into the existing intake loop
(`POST /v1/cases/intake`). Gate: CPU latency eval on the sandbox ladder
(same method as eval-qwen3-ladder.md) before wiring it into the live path.
