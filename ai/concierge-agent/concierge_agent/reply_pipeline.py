"""Reply guardrail pipeline (AI spec 4.3): every outbound message passes
schema -> policy -> tone -> factuality -> privacy. Nothing unreviewed leaves
the system at TIER-2/3 risk. Failures: regenerate -> degrade -> escalate.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import Any

_NUMERIC = re.compile(r"(\d[\d.,]*\s?(?:EUR|USD|GBP|AED|km|%|:\d{2})?)")


@dataclass
class PipelineResult:
    verdict: str  # PASS | REGENERATE | DEGRADE | ESCALATE
    body: str
    reasons: list[str] = field(default_factory=list)


FORBIDDEN_JARGON = ("margin", "take rate", "tier-", "idempotency", "internal")


def _schema_check(body: str, reasons: list[str]) -> None:
    stripped = body.strip()
    if stripped.startswith("{") and ("\"tool\"" in stripped or "\"args\"" in stripped):
        reasons.append("schema: tool JSON leaked into reply")
    for jargon in FORBIDDEN_JARGON:
        if jargon in body.lower():
            reasons.append(f"schema: internal jargon leaked: {jargon}")


def _policy_check(body: str, reasons: list[str]) -> None:
    lowered = body.lower()
    if "i guarantee availability" in lowered or "guaranteed available" in lowered:
        reasons.append("policy: availability guarantee beyond tenant confirmation")
    if "full refund" in lowered and "policy" not in lowered:
        reasons.append("policy: refund promise requires human confirmation")


def _tone_check(body: str, tenant_voice: dict[str, Any], reasons: list[str]) -> None:
    # Luxury register: short, composed, no emoji-slop (WhatsApp spec 5.2).
    emoji_count = sum(1 for c in body if ord(c) > 0x1F000)
    max_emoji = int(tenant_voice.get("max_emoji", 0))
    if emoji_count > max_emoji:
        reasons.append("tone: emoji-slop exceeds luxury register")
    if len(body) > 2000:
        reasons.append("tone: reply too long for concierge register")


def _factuality_check(body: str, allowed_values: list[str], reasons: list[str]) -> None:
    """Every numeric claim (prices, times) must match tool outputs verbatim."""
    if not allowed_values:
        return
    claims = [c.strip() for c in _NUMERIC.findall(body) if any(ch.isdigit() for ch in c)]
    for claim in claims:
        if len(claim) < 2:
            continue
        if not any(claim in v or v in claim for v in allowed_values):
            reasons.append(f"factuality: numeric claim '{claim}' not in tool outputs")


def _privacy_check(body: str, conversation_id: str, reasons: list[str]) -> None:
    # Cross-client leakage guard: replies are bound to (tenant, conversation).
    if "conv:" in body and f"conv:{conversation_id}" not in body:
        reasons.append("privacy: foreign conversation reference")


def run_pipeline(body: str, *, conversation_id: str,
                 allowed_values: list[str] | None = None,
                 tenant_voice: dict[str, Any] | None = None) -> PipelineResult:
    reasons: list[str] = []
    _schema_check(body, reasons)
    _policy_check(body, reasons)
    _tone_check(body, tenant_voice or {}, reasons)
    _factuality_check(body, allowed_values or [], reasons)
    _privacy_check(body, conversation_id, reasons)

    if not reasons:
        return PipelineResult(verdict="PASS", body=body)
    if any(r.startswith("privacy") for r in reasons):
        return PipelineResult(verdict="ESCALATE", body=body, reasons=reasons)
    if any(r.startswith("factuality") for r in reasons):
        return PipelineResult(verdict="REGENERATE", body=body, reasons=reasons)
    return PipelineResult(verdict="DEGRADE", body=body, reasons=reasons)
