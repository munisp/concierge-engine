"""Judged evaluation (AI spec 7): golden sets are CI-gated; on top of the
deterministic assertions, a judge scores reply quality. The HeuristicJudge is
the offline, zero-dependency stand-in for the LLM-judge panel — same
interface, same score shape; swapping in a model judge is config, not code.

V1 bar (AI spec): >=60% zero-touch conversations, 100% policy compliance.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import Any, Protocol

from .eval import GoldenCase, run_golden_cases
from .reply_pipeline import FORBIDDEN_JARGON

_NUMERIC = re.compile(r"(\d[\d.,]*\s?(?:EUR|USD|GBP|AED|km|%|:\d{2})?)")


@dataclass
class Judgment:
    score: float  # 0..1
    reasons: list[str] = field(default_factory=list)


class Judge(Protocol):
    def score(self, case: GoldenCase, reply: str, escalated: bool,
              actions: list[dict[str, Any]]) -> Judgment: ...


class HeuristicJudge:
    """Deterministic rubric: tone, leakage, escalation correctness, brevity."""

    def score(self, case: GoldenCase, reply: str, escalated: bool,
              actions: list[dict[str, Any]]) -> Judgment:
        reasons: list[str] = []
        score = 1.0
        if not case.expect_escalated and not reply.strip():
            score -= 0.5
            reasons.append("empty reply on a non-escalated turn")
        lowered = reply.lower()
        for jargon in FORBIDDEN_JARGON:
            if jargon in lowered:
                score -= 0.3
                reasons.append(f"internal jargon: {jargon}")
        if '{"tool"' in reply or '"args"' in reply:
            score -= 0.3
            reasons.append("tool JSON leaked")
        emoji = sum(1 for c in reply if ord(c) > 0x1F000)
        if emoji > 0:
            score -= 0.1
            reasons.append("emoji-slop")
        if len(reply) > 2000:
            score -= 0.2
            reasons.append("over-long reply")
        if escalated != case.expect_escalated:
            score -= 0.4
            reasons.append("escalation decision mismatch")
        return Judgment(score=max(0.0, round(score, 2)), reasons=reasons)


async def run_judged(cases: list[GoldenCase], judge: Judge | None = None) -> dict[str, Any]:
    judge = judge or HeuristicJudge()
    base = await run_golden_cases(cases)
    judged = []
    for case, result in zip(cases, base["results"]):
        # Replay actions are not returned by run_golden_cases; judged on
        # reply + escalation outcome, which the golden run captured.
        j = judge.score(case, result["reply"], result["escalated"], [])
        judged.append({**result, "judge_score": j.score, "judge_reasons": j.reasons})
    avg = sum(r["judge_score"] for r in judged) / max(len(judged), 1)
    zero_touch = sum(1 for r in judged if not r["escalated"]) / max(len(judged), 1)
    return {
        "total": len(judged),
        "passed": base["passed"],
        "pass_rate": base["pass_rate"],
        "avg_judge_score": round(avg, 3),
        "zero_touch_rate": round(zero_touch, 3),
        "bars": {
            "policy_compliance": base["pass_rate"] == 1.0,
            "zero_touch_ge_60pct": zero_touch >= 0.6,
        },
        "results": judged,
    }
