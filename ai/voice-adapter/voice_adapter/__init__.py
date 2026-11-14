"""Voice adapter service — real-time voice concierge channel (Phase 23).

Architecture: LiveKit carries the media (WebRTC rooms, SIP/PSTN ingress,
client SDKs); Pipecat carries the realtime agent pipeline (VAD/turn-taking/
barge-in, STT → agent turn → TTS); the platform's own concierge-agent stays
the brain — every voice turn is a /v1/turns call, so policy gating, tool
tiers, budget caps and shadow mode apply to voice exactly as they do to
WhatsApp. Transcripts land on the client's VOICE conversation thread
(Phase 22 get-or-create threading), one client across channels.
"""
