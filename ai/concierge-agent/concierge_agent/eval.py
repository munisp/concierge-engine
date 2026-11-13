"""Eval harness (AI spec 7): golden conversation sets, CI-gated.

A golden case replays an inbound turn against the orchestrator with stubbed
tools and asserts: state transitions, escalation triggers, reply pipeline
compliance. LLM-judge panels and weekly human review operate on the same
trace records exported from /v1/traces.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from .models import ToolResult
from .orchestrator import Orchestrator, TurnContext


@dataclass
class GoldenCase:
    name: str
    text: str
    expect_escalated: bool
    expect_trigger: str = ""
    expect_reply_contains: str = ""


class StubTools:
    """Tool boundary stub: no network, deterministic verdicts."""

    def __init__(self) -> None:
        self.calls: list[dict[str, Any]] = []
        self.traces: list[dict[str, Any]] = []

    async def call(self, tenant_id: str, conversation_id: str, tool: str,
                   args: dict[str, Any], state: str, turn: int) -> ToolResult:
        self.calls.append({"tool": tool, "args": args})
        if tool == "resolve_client":
            return ToolResult(ok=True, data={"client_id": "c1", "confidence": 0.99, "is_new": False})
        if tool == "escalate_to_human":
            return ToolResult(ok=True, data={"handoff_id": "h1"})
        if tool == "send_message":
            return ToolResult(ok=True, data={"message_id": "m1", "delivered": True})
        return ToolResult(ok=True)


async def run_golden_cases(cases: list[GoldenCase]) -> dict[str, Any]:
    results = []
    for case in cases:
        stub = StubTools()
        orch = Orchestrator(tools=stub)
        ctx = TurnContext(tenant_id="t1", conversation_id=f"golden-{case.name}", state="CAPTURING")
        res = await orch.handle_turn(ctx, case.text)
        ok = res.escalated == case.expect_escalated
        if ok and case.expect_reply_contains:
            ok = case.expect_reply_contains.lower() in res.reply.lower()
        results.append({"name": case.name, "pass": ok,
                        "escalated": res.escalated, "reply": res.reply})
    passed = sum(1 for r in results if r["pass"])
    return {"total": len(results), "passed": passed, "pass_rate": passed / max(len(results), 1),
            "results": results}


def load_golden_set(path: str | Path) -> list[GoldenCase]:
    data = json.loads(Path(path).read_text())
    return [GoldenCase(**c) for c in data]
