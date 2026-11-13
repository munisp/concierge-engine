"""Self-hosted inference via Ollama (llama.cpp) running Qwen (Phase 4).

Un-defers the deferred-register item "self-hosted open-weight models":
tenant data-locality mandates (UAE residency) and inference-spend economics
both point at owning the model runtime. Ollama exposes an OpenAI-compatible
endpoint (/v1/chat/completions) over llama.cpp; the default model is Qwen
(qwen3:8b) — strong multilingual + instruction following at laptop scale.

Design rules (unchanged from hosted providers):
  * EVERY model call passes through prepare_for_inference() — PII redaction
    and untrusted-content delimiting happen before any token leaves.
  * The router degrades to deterministic rules when Ollama is unreachable —
    CI and dev never need a GPU or a pulled model.
  * Per-task routing is config (CE_OLLAMA_*), not code.
"""

from __future__ import annotations

import json
import time
import urllib.error
import urllib.request
from typing import Any, Optional

from . import config
from .redact import prepare_for_inference


class OllamaProvider:
    """OpenAI-compatible chat completions against a local Ollama daemon."""

    def __init__(self, base: Optional[str] = None, model: Optional[str] = None,
                 timeout: float = 0) -> None:
        self.base = (base or config.OLLAMA_BASE_URL).rstrip("/")
        self.model = model or config.OLLAMA_MODEL
        self.timeout = timeout or config.OLLAMA_TIMEOUT
        self._healthy_until = 0.0
        self._healthy = False

    def healthy(self) -> bool:
        """Cached health probe (10s) — /api/tags lists pulled models."""
        now = time.monotonic()
        if now < self._healthy_until:
            return self._healthy
        try:
            with urllib.request.urlopen(f"{self.base}/api/tags", timeout=3) as resp:
                self._healthy = resp.status == 200
        except OSError:
            self._healthy = False
        self._healthy_until = now + 10
        return self._healthy

    def chat(self, system: str, user: str, *, temperature: float = 0.2,
             max_tokens: int = 512) -> str:
        """One round-trip. Caller owns redaction; this refuses nothing but
        raises on transport/API errors so the router can fall back."""
        body = json.dumps({
            "model": self.model,
            "messages": [
                {"role": "system", "content": system},
                {"role": "user", "content": user},
            ],
            "stream": False,
            # Structured tasks (triage/next-action) must not burn the token
            # budget on qwen3 thinking traces; ignored by models without it.
            "think": False,
            "options": {"temperature": temperature, "num_predict": max_tokens},
        }).encode()
        req = urllib.request.Request(
            f"{self.base}/v1/chat/completions", data=body,
            headers={"Content-Type": "application/json"}, method="POST")
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                data = json.loads(resp.read())
        except urllib.error.HTTPError as e:
            raise InferenceError(f"ollama {e.code}") from e
        except OSError as e:
            raise InferenceError(f"ollama unreachable: {e}") from e
        choices = data.get("choices") or []
        if not choices:
            raise InferenceError("ollama returned no choices")
        return str(choices[0].get("message", {}).get("content", "")).strip()

    def embed(self, text: str, model: Optional[str] = None) -> list[float]:
        """OpenAI-compatible embeddings (CE_OLLAMA_EMBED_MODEL, default
        nomic-embed-text). Raises InferenceError on any failure — semantic
        memory is advisory, callers degrade silently."""
        body = json.dumps({
            "model": model or config.OLLAMA_EMBED_MODEL,
            "input": text,
        }).encode()
        req = urllib.request.Request(
            f"{self.base}/v1/embeddings", data=body,
            headers={"Content-Type": "application/json"}, method="POST")
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                data = json.loads(resp.read())
        except urllib.error.HTTPError as e:
            raise InferenceError(f"ollama embed {e.code}") from e
        except OSError as e:
            raise InferenceError(f"ollama unreachable: {e}") from e
        rows = data.get("data") or []
        if not rows or not rows[0].get("embedding"):
            raise InferenceError("ollama returned no embedding")
        return [float(x) for x in rows[0]["embedding"]]


class InferenceError(RuntimeError):
    pass


# ------------------------------------------------------------- task router

TRIAGE_SYSTEM = (
    "You are the triage engine of a VIP concierge platform. Classify the "
    "inbound request. Reply with STRICT JSON only, no prose: "
    '{"kind": one of "booking|incident|inquiry|vip_request", '
    '"priority": one of "P1|P2|P3|P4", '
    '"summary": one sentence, '
    '"reason": short}. '
    "P1 = safety/legal/celebrity exposure; P2 = time-critical travel (<4h) "
    "or VIP; P3 = standard booking/inquiry; P4 = FYI/feedback."
)

SUMMARIZE_SYSTEM = (
    "You summarize concierge case timelines for busy operators. 3 bullets "
    "max: current state, blockers, next step. No invented facts — only what "
    "the timeline states."
)

NEXT_ACTION_SYSTEM = (
    "You advise concierge operators on the single best next action for a "
    "case. Reply with STRICT JSON only: "
    '{"action": short imperative, "rationale": one sentence}.'
)


class ModelRouter:
    """Per-task routing: local Qwen first (data-locality + cost), hosted
    frontier when configured for complex drafting, deterministic rules as the
    always-available floor. Everything flows through PII redaction."""

    def __init__(self, ollama: Optional[OllamaProvider] = None) -> None:
        self.ollama = ollama or OllamaProvider()

    # ---- public task APIs (used by case management + the orchestrator)

    def triage(self, text: str) -> dict[str, Any]:
        """Classify kind/priority/summary. LLM first, rules on any failure.
        Deterministic escalation: when the rules detect a safety/legal or
        service-recovery signal (incident at P1/P2), their kind wins and
        priority can only escalate — small models are observed to mis-kind
        complaints as bookings (see docs/eval-qwen3-ladder.md)."""
        safe, mapping = prepare_for_inference(text)
        rule = _rule_triage(text)
        if self.ollama.healthy():
            try:
                raw = self.ollama.chat(TRIAGE_SYSTEM, safe, temperature=0.0, max_tokens=200)
                parsed = _parse_json_object(raw)
                if parsed and parsed.get("kind") in {"booking", "incident", "inquiry", "vip_request"} \
                        and parsed.get("priority") in {"P1", "P2", "P3", "P4"}:
                    if rule["kind"] == "incident" and rule["priority"] in ("P1", "P2"):
                        if parsed["kind"] != "incident" or parsed["priority"] > rule["priority"]:
                            parsed["kind"] = "incident"
                            parsed["priority"] = min(parsed["priority"], rule["priority"])
                            parsed["reason"] = (parsed.get("reason", "") +
                                                " [rules-override: safety/recovery signal]").strip()
                    parsed["engine"] = f"ollama/{self.ollama.model}"
                    parsed["redacted_kinds"] = sorted({m["kind"] for m in mapping})
                    return parsed
            except (InferenceError, ValueError):
                pass  # fall through to rules
        out = rule
        out["engine"] = "rules"
        out["redacted_kinds"] = sorted({m["kind"] for m in mapping})
        return out

    def summarize(self, timeline: list[str]) -> dict[str, str]:
        safe, _ = prepare_for_inference("\n".join(timeline))
        if self.ollama.healthy():
            try:
                out = self.ollama.chat(SUMMARIZE_SYSTEM, safe, max_tokens=256)
                if out:
                    return {"summary": out, "engine": f"ollama/{self.ollama.model}"}
            except InferenceError:
                pass
        return {"summary": _rule_summary(timeline), "engine": "rules"}

    def next_action(self, case_state: str, summary: str) -> dict[str, str]:
        safe, _ = prepare_for_inference(f"state: {case_state}\n{summary}")
        if self.ollama.healthy():
            try:
                parsed = _parse_json_object(
                    self.ollama.chat(NEXT_ACTION_SYSTEM, safe, temperature=0.0, max_tokens=120))
                if parsed and parsed.get("action"):
                    parsed["engine"] = f"ollama/{self.ollama.model}"
                    return parsed
            except (InferenceError, ValueError):
                pass
        return {"action": _rule_next_action(case_state),
                "rationale": "deterministic fallback", "engine": "rules"}

    def embed(self, text: str) -> tuple[list[float], str]:
        """Semantic vector for case recall. Redaction still applies — the
        embed model is inference too. Raises when the daemon is down so
        callers can skip semantic memory rather than store junk."""
        safe, _ = prepare_for_inference(text)
        vec = self.ollama.embed(safe)
        return vec, f"ollama/{config.OLLAMA_EMBED_MODEL}"

    async def draft_reply(self, intent: str, text: str) -> str:
        """Orchestrator drafting path: hosted > local Qwen > mock rules."""
        safe, _ = prepare_for_inference(text)
        if config.MODEL_PROVIDER == "openai-compatible" and config.MODEL_BASE_URL:
            # Hosted frontier path (existing behavior — config, not code).
            raise NotImplementedError("hosted drafting stays on the tenant gateway")
        if self.ollama.healthy():
            try:
                return self.ollama.chat(
                    "You are a VIP concierge. Draft a brief, warm, precise reply. "
                    "Never invent prices or availability.", safe, max_tokens=300)
            except InferenceError:
                pass
        return ""  # caller falls back to its deterministic templates


# ------------------------------------------------------------- deterministic floor

def _rule_triage(text: str) -> dict[str, str]:
    t = text.lower()
    if any(w in t for w in ("safety", "threat", "legal", "lawyer", "press", "leak")):
        return {"kind": "incident", "priority": "P1",
                "summary": text[:140], "reason": "safety/legal keyword"}
    if any(w in t for w in ("jet", "helicopter", "yacht", "vip", "celebrity", "royal")):
        return {"kind": "vip_request", "priority": "P2",
                "summary": text[:140], "reason": "high-value vertical"}
    if any(w in t for w in ("urgent", "asap", "immediately", "tonight", "within the hour")):
        return {"kind": "booking", "priority": "P2",
                "summary": text[:140], "reason": "time-critical wording"}
    if any(w in t for w in ("book", "quote", "price", "ride", "car", "transfer", "arrange")):
        return {"kind": "booking", "priority": "P3",
                "summary": text[:140], "reason": "booking vocabulary"}
    if any(w in t for w in ("complaint", "broken", "wrong", "late", "refund")):
        return {"kind": "incident", "priority": "P2",
                "summary": text[:140], "reason": "service-recovery signal"}
    return {"kind": "inquiry", "priority": "P3",
            "summary": text[:140], "reason": "default"}


def _rule_summary(timeline: list[str]) -> str:
    if not timeline:
        return "No activity yet."
    recent = timeline[-3:]
    return " | ".join(t[:120] for t in recent)


def _rule_next_action(case_state: str) -> str:
    return {
        "OPEN": "Triage and assign an owner",
        "TRIAGED": "Assign an owner and acknowledge the client",
        "IN_PROGRESS": "Check SLA clock and push the oldest open task",
        "WAITING_CLIENT": "Send a polite nudge; prepare options for their reply",
        "WAITING_PARTNER": "Chase the partner; line up a fallback supplier",
        "RESOLVED": "Confirm client satisfaction and close",
        "CLOSED": "No action — case closed",
    }.get(case_state, "Review the case")


def _parse_json_object(raw: str) -> Optional[dict[str, Any]]:
    """Models wrap JSON in prose or fences — extract the first object."""
    start = raw.find("{")
    end = raw.rfind("}")
    if start == -1 or end <= start:
        return None
    try:
        obj = json.loads(raw[start:end + 1])
    except json.JSONDecodeError:
        return None
    return obj if isinstance(obj, dict) else None
