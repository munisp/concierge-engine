"""Pipecat pipeline assembly — lazy-imported, config-gated, honest.

The pipeline shape (when Pipecat is installed and providers configured):

    LiveKit transport (room audio in/out, Silero VAD turn detection)
      → STT (Deepgram, or OpenAI-compatible transcription)
      → AgentTurnProcessor  ← the platform's concierge-agent IS the LLM stage
      → TTS (ElevenLabs or Cartesia)
      → LiveKit transport out

Why the agent service sits where the LLM normally would: policy gating,
tool tiers, per-conversation budget caps and shadow mode already live in
concierge-agent. Wiring a raw LLM here would bypass all of it; instead the
pipeline turns each final transcript into a /v1/turns call and speaks the
reply. Barge-in and turn-taking stay Pipecat's job.

Pipecat and the LiveKit SDK are OPTIONAL runtime dependencies — this module
imports cleanly without them so the session API, webhook receiver and tests
run hermetic. build_pipeline() refuses loudly with the exact missing piece.

Self-hosted note: the Phase 8 whisper stack is BATCH transcription (right
for voice notes); live calls need a streaming STT provider — tracked in
docs/deferred-register.md.
"""

from __future__ import annotations

import importlib.util
from typing import Awaitable, Callable, Optional

from . import config
from .bridge import CoreBridge, handle_user_turn


class VoiceUnavailable(Exception):
    """Raised when the live pipeline cannot be assembled; the message lists
    every missing piece (never a bare 'unavailable')."""


def pipecat_available() -> bool:
    return importlib.util.find_spec("pipecat") is not None


def pipeline_readiness() -> dict:
    missing = []
    if not pipecat_available():
        missing.append("pipecat-ai (pip install 'pipecat-ai[livekit]' on the voice worker)")
    missing.extend(config.readiness()["missing"])
    return {"pipecat": pipecat_available(), "ready": not missing, "missing": missing}


def bot_token(session) -> str:
    """The concierge joins the room as a named participant with the same
    token contract as any client — no backdoor media path."""
    from .tokens import mint_access_token
    return mint_access_token(config.LIVEKIT_API_KEY, config.LIVEKIT_API_SECRET,
                             identity="concierge-ai", room=session.room,
                             ttl_minutes=config.MAX_SESSION_MINUTES,
                             name="Concierge")


def build_pipeline(session, bridge: CoreBridge,
                   on_client_left: Optional[Callable[[], Awaitable[None]]] = None):
    """Assemble the realtime PipelineTask for a session. Runs only on the
    voice worker with full deps installed; every failure names its cause."""
    if not pipecat_available():
        raise VoiceUnavailable(
            "pipecat-ai not installed — pip install 'pipecat-ai[livekit]' "
            "on the voice worker")
    ready = pipeline_readiness()
    if not ready["ready"]:
        raise VoiceUnavailable("voice pipeline not configured: "
                               + "; ".join(ready["missing"]))

    # Lazy imports — this module must import cleanly without pipecat.
    from pipecat.audio.vad.silero import SileroVADAnalyzer
    from pipecat.frames.frames import (Frame, TranscriptionFrame,
                                       UserStartedSpeakingFrame)
    from pipecat.pipeline.pipeline import Pipeline
    from pipecat.pipeline.task import PipelineParams, PipelineTask
    from pipecat.processors.frame_processor import FrameDirection, FrameProcessor
    from pipecat.transports.livekit.transport import (LiveKitParams,
                                                      LiveKitTransport)

    class AgentTurnProcessor(FrameProcessor):
        """Final transcription in → agent turn → speakable reply frames out.
        The transcript is consumed here (it never flows to a raw LLM
        service); the concierge-agent's reply is what gets spoken."""

        async def process_frame(self, frame: Frame, direction: FrameDirection):
            await super().process_frame(frame, direction)
            if isinstance(frame, TranscriptionFrame) and frame.text.strip():
                reply = await handle_user_turn(bridge, session, frame.text.strip())
                if reply:
                    from pipecat.frames.frames import TTSSpeakFrame
                    await self.push_frame(TTSSpeakFrame(reply))
                return
            if isinstance(frame, UserStartedSpeakingFrame):
                # Barge-in provenance: the timeline shows the caller cut the
                # agent off — context a human concierge needs on takeover.
                await bridge.post_message(session.tenant_id,
                                          session.conversation_id, "SYSTEM",
                                          "[interrupted — caller barged in]")
                await self.push_frame(frame, direction)
                return
            await self.push_frame(frame, direction)

    if config.VOICE_STT == "deepgram":
        from pipecat.services.deepgram.stt import DeepgramSTTService
        stt = DeepgramSTTService(api_key=config.DEEPGRAM_API_KEY)
    else:  # openai-compatible transcription endpoint
        from pipecat.services.openai.stt import OpenAISTTService
        stt = OpenAISTTService(api_key=config.OPENAI_API_KEY)

    if config.VOICE_TTS == "elevenlabs":
        from pipecat.services.elevenlabs.tts import ElevenLabsTTSService
        tts = ElevenLabsTTSService(api_key=config.ELEVENLABS_API_KEY,
                                   voice_id=config.ELEVENLABS_VOICE_ID)
    else:
        from pipecat.services.cartesia.tts import CartesiaTTSService
        tts = CartesiaTTSService(api_key=config.CARTESIA_API_KEY,
                                 voice_id=config.CARTESIA_VOICE_ID)

    transport = LiveKitTransport(
        url=config.LIVEKIT_URL,
        token=bot_token(session),
        room_name=session.room,
        params=LiveKitParams(audio_in_enabled=True, audio_out_enabled=True,
                             vad_analyzer=SileroVADAnalyzer()))

    pipeline = Pipeline([
        transport.input(),
        stt,
        AgentTurnProcessor(),
        tts,
        transport.output(),
    ])
    task = PipelineTask(pipeline, params=PipelineParams(
        allow_interruptions=True, enable_metrics=True))

    @transport.event_handler("on_participant_left")
    async def _on_participant_left(transport, participant, *args):
        identity = getattr(participant, "identity", "") or ""
        if identity.startswith("client-") and on_client_left is not None:
            await on_client_left()
        await task.cancel()

    return task
