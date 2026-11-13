"""PII redaction before inference (AI spec: prompt-injection hardening +
PII redaction before any model call) — mirrors crates/ingestion-parser.

Everything the model sees passes through redact_pii(); the mapping lets the
orchestrator rehydrate tool arguments from the ORIGINAL inbound text (tools
call core-api with real values — only inference is redacted).
"""

from __future__ import annotations

import re

_EMAIL = re.compile(r"[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}")
_LONG_NUMBER = re.compile(r"\b\d{9,}\b")
_CARD_CANDIDATE = re.compile(r"\b(?:\d[ -]?){13,19}\b")

UNTRUSTED_OPEN = "<<<UNTRUSTED_EXTERNAL_CONTENT>>>"
UNTRUSTED_CLOSE = "<<<END_UNTRUSTED_EXTERNAL_CONTENT>>>"


def luhn_ok(digits: str) -> bool:
    if not digits.isdigit() or not (13 <= len(digits) <= 19):
        return False
    total = 0
    for i, ch in enumerate(reversed(digits)):
        d = int(ch)
        if i % 2 == 1:
            d *= 2
            if d > 9:
                d -= 9
        total += d
    return total % 10 == 0


def redact_pii(text: str) -> tuple[str, list[dict[str, str]]]:
    """Return (redacted_text, mapping). Mapping entries: {token, kind}."""
    mapping: list[dict[str, str]] = []

    def card_sub(m: re.Match[str]) -> str:
        digits = re.sub(r"\D", "", m.group(0))
        if luhn_ok(digits):
            mapping.append({"token": "[CARD_REDACTED]", "kind": "card"})
            return "[CARD_REDACTED]"
        return m.group(0)

    def email_sub(m: re.Match[str]) -> str:
        mapping.append({"token": "[EMAIL_REDACTED]", "kind": "email"})
        return "[EMAIL_REDACTED]"

    def number_sub(m: re.Match[str]) -> str:
        mapping.append({"token": "[NUMBER_REDACTED]", "kind": "number"})
        return "[NUMBER_REDACTED]"

    out = _CARD_CANDIDATE.sub(card_sub, text)
    out = _EMAIL.sub(email_sub, out)
    out = _LONG_NUMBER.sub(number_sub, out)
    return out, mapping


def wrap_untrusted(text: str) -> str:
    """Delimit external content so injection attempts can't impersonate the
    operator or the system prompt."""
    return f"{UNTRUSTED_OPEN}\n{text}\n{UNTRUSTED_CLOSE}"


def prepare_for_inference(text: str) -> tuple[str, list[dict[str, str]]]:
    """Redact + delimit: the only path inbound text takes into a model."""
    redacted, mapping = redact_pii(text)
    return wrap_untrusted(redacted), mapping
