# Phase 23 — Voice concierge channel (Pipecat × LiveKit)

Adds the third client channel: real-time voice. WhatsApp stays async-first;
voice is for the VIP who expects to *call* their concierge and be answered
by name with full context.

## Stack decision (and why both projects)

- **LiveKit = media plane.** Open-source (Apache-2.0) WebRTC SFU, genuinely
  self-hostable, with native SIP ingress so a tenant can publish a real
  phone number, plus web/mobile SDKs for the console and PWA. Rooms give
  participant lifecycle (join/leave), reconnection and multi-party
  (client + AI + human concierge takeover) for free.
- **Pipecat = agent pipeline.** Open-source (BSD-2-Clause) frame-processor
  pipeline: VAD/turn detection (Smart Turn), barge-in, STT → LLM → TTS with
  swappable providers — running over its **LiveKit transport**, so the two
  compose rather than compete. Pipecat keeps every stage inspectable, which
  matters for a platform that labels provenance on everything.
- **The platform's concierge-agent stays the brain.** The pipeline's LLM
  stage is `AgentTurnProcessor`: each final transcript becomes a
  `/v1/turns` call to the existing agent service. Policy-engine gating,
  tool tiers, per-conversation budget caps, shadow mode, and the
  deterministic rule floor apply to voice exactly as to WhatsApp — no
  parallel unguarded LLM path.

## What ships (`ai/voice-adapter`)

- **Session API** — `POST /v1/voice/sessions` (phone → resolved client →
  room → LiveKit join token), `GET/DELETE /v1/voice/sessions/{id}`
  (tenant-scoped; DELETE = hangup). Sessions are ephemeral call state; the
  durable record is the conversation thread.
- **LiveKit tokens** — stdlib HS256 JWT minting with the `video` grant
  (`roomJoin`, room, publish/subscribe), TTL-gated; the bot joins as
  participant `concierge-ai` with the same token contract — no media
  backdoor. Webhook receiver verifies the bearer JWT **and** its `sha256`
  body-binding claim; tampered bodies with valid signatures are rejected.
- **Transcript bridge** — every caller turn: CLIENT transcript line on the
  client's **VOICE** conversation (Phase 22 get-or-create threading: repeat
  calls continue one thread, the funnel counts calls once), advisory
  per-turn case intake idempotent at `voice:{session}:{turn}`, agent reply
  recorded as AI line and spoken. Barge-ins land as SYSTEM provenance
  markers (`[interrupted — caller barged in]`). Session end flushes a final
  intake exactly once (`voice:{session}:final`, idempotent across webhook
  redelivery).
- **Failure honesty** — identity resolution / conversation open are hard
  (502 with cause; no anchor, no call). Intake, transcript posting and
  agent failure are advisory: agent failure speaks a holding phrase
  ("…human concierge…") and records nothing fake.
- **Honest gating** — `CE_VOICE_ENABLED` default off; `/health` reports
  config and pipeline readiness verbatim (`missing: [...]`); session
  creation refuses 503 with the exact missing pieces until LiveKit creds,
  streaming STT (Deepgram/OpenAI-compatible) and TTS (ElevenLabs/Cartesia)
  are configured. Note: the Phase 8 whisper stack is *batch* (right for
  voice notes); live calls need streaming STT — cutover steps in
  `docs/deferred-register.md`.

## Verification

33 tests, all hermetic (no pipecat/livekit/network):

- **tokens (8)**: mint/verify roundtrip, grant shape, TTL, wrong-secret and
  tampered-payload rejection, expiry, webhook body-binding accept/reject.
- **sessions (7)**: lifecycle, no-show, terminal-state refusal, failure
  path, tenant isolation, active filtering.
- **bridge (7)**: full turn contract (CLIENT → intake → turn → AI, exact
  order and payloads), per-turn idempotency keys, agent-failure holding
  phrase with no fake AI line, advisory intake, hard resolve/conversation
  failures, VOICE-thread shape.
- **webhooks (6)**: client join flips LIVE, bot join doesn't, room_finished
  ends + single final flush across redelivery, zero-turn skip, unknown
  events ignored, idempotent end.
- **main (5)**: degraded-truth health, 503 with missing pieces, session
  creation end-to-end (token verifiable with configured secret), tenant
  scoping, hangup, webhook 401/200.

CI: voice-adapter added to the python job. Full suite re-run: adapter 55,
agent 26, analytics 16+1, voice 33, Go + Rust + contracts + web green.
