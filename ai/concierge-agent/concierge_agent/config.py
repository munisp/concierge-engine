import os

CORE_API_URL = os.getenv("CE_CORE_API_URL", "http://localhost:8000")
POLICY_ENGINE_URL = os.getenv("CE_POLICY_ENGINE_URL", "http://localhost:8200")
MODEL_PROVIDER = os.getenv("CE_MODEL_PROVIDER", "mock")  # mock | openai-compatible
MODEL_BASE_URL = os.getenv("CE_MODEL_BASE_URL", "")
MODEL_NAME = os.getenv("CE_MODEL_NAME", "concierge-frontier")
MODEL_API_KEY = os.getenv("CE_MODEL_API_KEY", "")
# Self-hosted inference (Phase 4): Ollama (llama.cpp) serving Qwen locally.
# Unreachable daemon -> deterministic rule fallback, never an outage.
OLLAMA_BASE_URL = os.getenv("CE_OLLAMA_BASE_URL", "http://localhost:11434")
OLLAMA_MODEL = os.getenv("CE_OLLAMA_MODEL", "qwen3:8b")
OLLAMA_EMBED_MODEL = os.getenv("CE_OLLAMA_EMBED_MODEL", "nomic-embed-text")
# Slow CPU-only hosts need headroom: a 4b model on 2 cores can take 45s+.
OLLAMA_TIMEOUT = float(os.getenv("CE_OLLAMA_TIMEOUT", "30"))
# Per-conversation LLM spend cap (USD cents) - AI spec 4.1 rate & budget caps.
CONVERSATION_BUDGET_CENTS = int(os.getenv("CE_CONVERSATION_BUDGET_CENTS", "200"))
# Shadow mode (AI spec 7): new tenant configs run draft-only — no mutating
# tool calls, no outbound sends — until eval evidence promotes them.
SHADOW_MODE = os.getenv("CE_SHADOW_MODE", "false").lower() == "true"
