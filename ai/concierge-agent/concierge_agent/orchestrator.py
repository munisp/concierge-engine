"""Orchestrator (AI spec 2): one orchestrator agent per conversation.
Maintains intent state, decides tool calls, drafts replies in tenant voice,
escalates when judgment/value/sensitivity demands it.

Model access goes through the model gateway (per-task routing; swapping
providers is config, not code). Default provider is `mock` — a deterministic
rule-based responder used for dev/evals without external credentials.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import Any, Optional

from . import config
from .reply_pipeline import run_pipeline
from .tools import ToolExecutor

# Trigger taxonomy (AI spec 5.1)
TRIGGER_VALUE = "VALUE"
TRIGGER_CONFIDENCE = "CONFIDENCE"
TRIGGER_SENTIMENT = "SENTIMENT"
TRIGGER_POLICY = "POLICY"
TRIGGER_SLA = "SLA"

_NEGATIVE_SENTIMENT = ("terrible", "unacceptable", "complaint", "angry", "furious",
                       "disappointed", "refund", "cancel everything")

_INTENT_PATTERNS = [
    (("quote", "price", "cost", "how much", "book", "arrange", "need a car", "need a ride"),
     "booking_intent"),
    (("jet", "helicopter", "yacht", "charter"), "high_value_intent"),
    (("status", "where is", "eta", "driver"), "status_inquiry"),
    (("cancel",), "cancellation"),
    (("prefer", "always", "usually", "i like"), "preference_signal"),
]


@dataclass
class TurnContext:
    tenant_id: str
    conversation_id: str
    state: str = "NEW"
    turn: int = 0
    client_id: Optional[str] = None
    client_phone: str = ""
    vip_flagged: bool = False
    allowed_values: list[str] = field(default_factory=list)  # verbatim tool outputs
    spend_cents: int = 0


@dataclass
class OrchestratorResult:
    reply: str
    state: str
    escalated: bool = False
    handoff_id: Optional[str] = None
    actions: list[dict[str, Any]] = field(default_factory=list)


class ModelGateway:
    """Per-task model routing behind one gateway — swapping is config."""

    def __init__(self) -> None:
        self.provider = config.MODEL_PROVIDER

    async def draft_reply(self, ctx: TurnContext, intent: str,
                          details: dict[str, Any]) -> str:
        if self.provider == "mock":
            return self._mock_reply(ctx, intent, details)
        # openai-compatible providers: POST {base}/chat/completions.
        # Kept thin: production prompts are versioned per tenant in the
        # tenant system prompt (brand voice, service standards, market facts).
        raise NotImplementedError("configure CE_MODEL_BASE_URL for live inference")

    @staticmethod
    def _mock_reply(ctx: TurnContext, intent: str, details: dict[str, Any]) -> str:
        if intent == "booking_intent" and details.get("quote"):
            q = details["quote"]
            total = f"{q['total_minor'] / 100:.2f} {q['currency']}"
            return (f"Thank you. I can confirm availability and your fare is {total}, "
                    f"inclusive of meet and greet. Shall I send a secure payment link?")
        if intent == "high_value_intent":
            return ("Thank you — a senior concierge is reviewing the best options "
                    "for you and will reply shortly.")
        if intent == "status_inquiry":
            return "Your chauffeur is on the way; I will share live updates here."
        if intent == "preference_signal":
            return "Noted — I have saved this to your preferences for every future journey."
        return "Thank you for your message. How may I assist with your travel arrangements?"


class Orchestrator:
    def __init__(self, tools: Optional[ToolExecutor] = None,
                 gateway: Optional[ModelGateway] = None) -> None:
        self.tools = tools or ToolExecutor()
        self.gateway = gateway or ModelGateway()

    @staticmethod
    def _classify(text: str) -> str:
        lowered = text.lower()
        for needles, intent in _INTENT_PATTERNS:
            if any(n in lowered for n in needles):
                return intent
        return "general"

    async def handle_turn(self, ctx: TurnContext, inbound_text: str) -> OrchestratorResult:
        ctx.turn += 1
        actions: list[dict[str, Any]] = []

        # Rate & budget caps: anomalies degrade to human-only (AI spec 4.1).
        if ctx.spend_cents > config.CONVERSATION_BUDGET_CENTS:
            return await self._escalate(ctx, TRIGGER_POLICY, "conversation budget exceeded", actions)

        lowered = inbound_text.lower()

        # Sentiment/sensitivity trigger — immediate + priority paging.
        if any(w in lowered for w in _NEGATIVE_SENTIMENT):
            return await self._escalate(ctx, TRIGGER_SENTIMENT,
                                        "negative sentiment detected", actions)

        intent = self._classify(inbound_text)

        # High-value verticals route to a human owner — AI drafts, human owns.
        if intent == "high_value_intent" or ctx.vip_flagged and intent == "booking_intent":
            result = await self._escalate(ctx, TRIGGER_VALUE,
                                          "high-value vertical or VIP-flagged request", actions)
            result.reply = await self.gateway.draft_reply(ctx, intent, {})
            return result

        if intent == "preference_signal" and ctx.client_id:
            res = await self.tools.call(ctx.tenant_id, ctx.conversation_id,
                                        "save_preference",
                                        {"client_id": ctx.client_id,
                                         "key": "stated", "value": inbound_text[:200],
                                         "source_quote": inbound_text[:200]},
                                        ctx.state, ctx.turn)
            actions.append({"tool": "save_preference", "ok": res.ok})

        if intent == "booking_intent" and ctx.state in {"CAPTURING", "QUOTING"}:
            # Resolve identity, create the request, price it — all tool calls.
            res = await self.tools.call(ctx.tenant_id, ctx.conversation_id,
                                        "resolve_client", {"phone": ctx.client_phone},
                                        ctx.state, ctx.turn)
            actions.append({"tool": "resolve_client", "ok": res.ok})
            if res.ok and res.data.get("confidence", 0) >= 0.5:
                ctx.client_id = res.data["client_id"]

        reply = await self.gateway.draft_reply(ctx, intent, actions[-1] if actions else {})

        # Reply guardrail pipeline before anything leaves (AI spec 4.3).
        pipeline = run_pipeline(reply, conversation_id=ctx.conversation_id,
                                allowed_values=ctx.allowed_values)
        if pipeline.verdict == "ESCALATE":
            return await self._escalate(ctx, TRIGGER_POLICY,
                                        "; ".join(pipeline.reasons), actions)
        if pipeline.verdict == "REGENERATE":
            reply = await self.gateway.draft_reply(ctx, intent, {"retry": True})
            pipeline = run_pipeline(reply, conversation_id=ctx.conversation_id,
                                    allowed_values=ctx.allowed_values)
            if pipeline.verdict != "PASS":
                return await self._escalate(ctx, TRIGGER_POLICY,
                                            "; ".join(pipeline.reasons), actions)

        sent = await self.tools.call(ctx.tenant_id, ctx.conversation_id, "send_message",
                                     {"body": pipeline.body}, ctx.state, ctx.turn)
        actions.append({"tool": "send_message", "ok": sent.ok})

        return OrchestratorResult(reply=pipeline.body, state=ctx.state, actions=actions)

    async def _escalate(self, ctx: TurnContext, trigger: str, reason: str,
                        actions: list[dict[str, Any]]) -> OrchestratorResult:
        res = await self.tools.call(ctx.tenant_id, ctx.conversation_id,
                                    "escalate_to_human",
                                    {"trigger": trigger, "reason": reason},
                                    ctx.state, ctx.turn)
        actions.append({"tool": "escalate_to_human", "ok": res.ok, "trigger": trigger})
        ctx.state = "ESCALATED"
        return OrchestratorResult(
            reply="", state=ctx.state, escalated=True,
            handoff_id=res.data.get("handoff_id") if res.ok else None,
            actions=actions)


# Utility used by the WhatsApp adapter for time extraction heuristics.
def extract_pickup_hint(text: str) -> Optional[str]:
    m = re.search(r"\b(\d{1,2}[:.]\d{2})\b", text)
    return m.group(1) if m else None
