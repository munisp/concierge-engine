"""Voice adapter configuration — every live-audio dependency is env-gated and
honestly reported. Nothing here invents capability: if a piece is missing,
/health says so and session creation refuses with the exact reason."""

import os

# Master switch. Default off: a voice channel that half-works is worse than
# no voice channel.
VOICE_ENABLED = os.getenv("CE_VOICE_ENABLED", "false").lower() == "true"

# LiveKit media plane (self-hosted OSS server or LiveKit Cloud).
LIVEKIT_URL = os.getenv("CE_LIVEKIT_URL", "")            # wss://livekit.example:7880
LIVEKIT_API_KEY = os.getenv("CE_LIVEKIT_API_KEY", "")
LIVEKIT_API_SECRET = os.getenv("CE_LIVEKIT_API_SECRET", "")

# Platform services.
CORE_API_URL = os.getenv("CE_CORE_API_URL", "http://localhost:8000")
AGENT_URL = os.getenv("CE_AGENT_URL", "http://localhost:8100")

# Streaming speech providers. The Phase 8 whisper stack is batch (right for
# voice notes); live calls need streaming STT — Deepgram or an
# OpenAI-compatible transcription endpoint. TTS: ElevenLabs or Cartesia.
VOICE_STT = os.getenv("CE_VOICE_STT", "deepgram")        # deepgram | openai
DEEPGRAM_API_KEY = os.getenv("CE_DEEPGRAM_API_KEY", "")
OPENAI_API_KEY = os.getenv("CE_OPENAI_API_KEY", "")
VOICE_TTS = os.getenv("CE_VOICE_TTS", "elevenlabs")      # elevenlabs | cartesia
ELEVENLABS_API_KEY = os.getenv("CE_ELEVENLABS_API_KEY", "")
ELEVENLABS_VOICE_ID = os.getenv("CE_ELEVENLABS_VOICE_ID", "21m00Tcm4TlvDq8ikWAM")
CARTESIA_API_KEY = os.getenv("CE_CARTESIA_API_KEY", "")
CARTESIA_VOICE_ID = os.getenv("CE_CARTESIA_VOICE_ID", "")

ROOM_PREFIX = os.getenv("CE_VOICE_ROOM_PREFIX", "ce-voice")
TOKEN_TTL_MINUTES = int(os.getenv("CE_VOICE_TOKEN_TTL_MINUTES", "15"))
# Hard ceiling on a call; the pipeline ends the session at the limit so a
# stuck room never burns STT/TTS spend indefinitely.
MAX_SESSION_MINUTES = int(os.getenv("CE_VOICE_MAX_SESSION_MINUTES", "30"))


def readiness() -> dict:
    """What works right now, what is missing, and why. /health serves this
    verbatim — an operator must be able to see the degraded truth."""
    missing = []
    if not VOICE_ENABLED:
        missing.append("CE_VOICE_ENABLED=false")
    if not (LIVEKIT_URL and LIVEKIT_API_KEY and LIVEKIT_API_SECRET):
        missing.append("livekit credentials (CE_LIVEKIT_URL/_API_KEY/_API_SECRET)")
    if VOICE_STT == "deepgram" and not DEEPGRAM_API_KEY:
        missing.append("CE_DEEPGRAM_API_KEY (streaming STT)")
    if VOICE_STT == "openai" and not OPENAI_API_KEY:
        missing.append("CE_OPENAI_API_KEY (streaming STT)")
    if VOICE_TTS == "elevenlabs" and not ELEVENLABS_API_KEY:
        missing.append("CE_ELEVENLABS_API_KEY (TTS)")
    if VOICE_TTS == "cartesia" and not CARTESIA_API_KEY:
        missing.append("CE_CARTESIA_API_KEY (TTS)")
    return {
        "enabled": VOICE_ENABLED,
        "livekit_configured": bool(LIVEKIT_URL and LIVEKIT_API_KEY and LIVEKIT_API_SECRET),
        "stt": VOICE_STT, "tts": VOICE_TTS,
        "ready": not missing,
        "missing": missing,
    }
