# Phase 50 — Security hardening wave

Context: the 2025–26 attack cycle (Shai-Hulud npm worm, TanStack
release-pipeline hijack with valid SLSA-3 provenance, Megalodon CI-bot
commit floods, Salesloft/Drift OAuth cascade, record L7 DDoS volumes)
targeted exactly the surfaces a payments-adjacent platform exposes: build
pipelines, dependency graphs, public endpoints, and stored credentials.
Phase 50 closes the platform-side gaps with a defense-in-depth pass.
(Full assessment: security-posture.md, delivered 2026-11-18.)

## What shipped

### 1. CI/CD supply-chain hardening (`.github/workflows/ci.yml`)
- Every action pinned to a full commit SHA (tags are mutable; the TanStack
  hijack proved provenance alone does not save a poisoned pipeline).
- Top-level `permissions: contents: read` — default-deny GITHUB_TOKEN.
- `persist-credentials: false` on every checkout (no token leakage into
  artifacts or later steps).
- Console install is `npm ci --ignore-scripts` — the Shai-Hulud family
  executes via lifecycle scripts; the build needs none (esbuild ships
  platform binaries via optionalDependencies).

### 2. New `security` CI job — every push is gated on:
| Tool | Stack | Pin |
|---|---|---|
| gitleaks | repo history + tree | binary v8.30.1, sha256-verified |
| govulncheck | Go | v1.1.4 |
| cargo-audit | Rust | 0.23.0 (`--locked`) |
| pip-audit | Python (5 requirement files) | 2.10.1 |
| npm audit (`--omit=dev --audit-level=high`) | console production deps | node 20 toolchain |
| zizmor | GitHub Actions themselves | 1.32.0 |

`npm audit --omit=dev` is deliberate: vite/esbuild/picomatch advisories are
build-time tooling — the console ships as a static bundle and no dev-server
or glob matching reaches production. Production deps are held clean.

Findings fixed this phase: react-router-dom → 7.18.5 (open redirect +
SSR constructor-injection advisories; only runtime dep flagged).
RUSTSEC-2023-0071 (rsa via opendal→reqsign Azure path) is documented and
ignored in `crates/.cargo/audit.toml` — no fix exists upstream, and the
advisory's decryption-oracle model does not match request signing.
Re-review on every opendal upgrade.

### 3. Edge security headers (server/securityHeaders middleware)
CSP, HSTS, X-Content-Type-Options, Referrer-Policy, Permissions-Policy on
every response; `X-Frame-Options: SAMEORIGIN` + `frame-ancestors 'self'`
everywhere EXCEPT `/embed/{slug}` — the booking widget is designed to be
iframed onto tenant sites. Handlers keep the last word (the media endpoint's
stricter CSP overrides the baseline).

### 4. Tiered rate limiting (server/rateLimit middleware)
- Anonymous traffic: per source-IP bucket (default 30 rps / 60 burst).
- Identified traffic: per-tenant bucket (default 200/400) keyed on
  X-Tenant-ID or a hash of the bearer key — PLUS a per-IP ceiling at tenant
  rates so rotating fake tenant headers cannot mint unlimited buckets.
- Runs OUTSIDE tenantctx so auth-rejected floods still burn budget.
- `/healthz` exempt (load-balancer probes). 429 carries `Retry-After: 1`
  and the standard error envelope (`rate_limited`).
- Env: `CE_RATE_PUBLIC_RPS/BURST`, `CE_RATE_TENANT_RPS/BURST`,
  `CE_RATE_LIMIT=off` (load tests/dev).
- Honest limit: in-process buckets. Multi-replica fleets need the CDN/WAF
  (Cloudflare) or a Redis backend for global ceilings — the middleware is
  the canonical hook.

### 5. SSRF egress guard (httpx.SafeTransport / NewSafeClient)
Webhook delivery targets are tenant-controlled URLs — the platform's prime
SSRF surface. The guarded dialer resolves the host and refuses loopback,
RFC-1918, link-local (incl. 169.254.169.254 cloud metadata), CGNAT,
multicast, unspecified and reserved ranges, then dials the resolved IP
directly (no DNS-rebinding TOCTOU gap). Redirects re-enter the same guard;
chains capped at 5. WebhookService defaults to the guarded client; tests
use the explicit `AllowPrivateTargets` escape hatch (never set in prod).
Operator-configured service URLs (adapters, PSPs) are exempt by design —
deployment config is not request input.

### 6. Strict decoders — verified existing
`httpx.Decode` already sets `DisallowUnknownFields()` on every JSON write
handler (mass-assignment resistance); external webhook ingress (Stripe,
Infobip) parses raw bodies separately and is unaffected.

## Tests
- httpx: SSRF blocklist (loopback, RFC-1918, metadata, CGNAT, v4-mapped
  smuggle, scheme abuse), guarded vs allowPrivate client end-to-end.
- domain: webhook delivery to a loopback receiver is blocked by default
  with `ssrf` in the delivery error; the escape hatch delivers on replay.
- server: header battery on API + public surfaces, `/embed/` framing
  exemption, public/tenant bucket independence, `/healthz` exemption,
  429 envelope shape.

## Deferred (waves 2–3 of the posture doc)
CDN/WAF fronting + Turnstile on anonymous writes; Trivy image scanning;
WAL-G backups + restore drills; pgaudit; k8s NetworkPolicy/securityContext;
Schemathesis API fuzzing; prompt-injection screening at the agent edge;
TOTP MFA for console operators.
