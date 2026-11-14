FROM python:3.12-slim
WORKDIR /app
COPY ai/voice-adapter/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
# Live calls additionally need: pip install 'pipecat-ai[livekit,deepgram,cartesia,elevenlabs,silero]'
COPY ai/voice-adapter/ .
EXPOSE 8130
CMD ["uvicorn", "voice_adapter.main:app", "--host", "0.0.0.0", "--port", "8130"]
