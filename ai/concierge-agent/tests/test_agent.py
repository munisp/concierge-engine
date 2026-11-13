"""Unit tests for the reply pipeline, tool tiering and golden-set harness."""

from concierge_agent.eval import GoldenCase, StubTools, run_golden_cases
from concierge_agent.orchestrator import Orchestrator, TurnContext
from concierge_agent.reply_pipeline import run_pipeline
from concierge_agent.tools import idempotency_key, tool_tier


def test_tool_tiers():
    assert tool_tier("get_preferences") == 0
    assert tool_tier("save_preference") == 1
    assert tool_tier("create_payment_link") == 2
    assert tool_tier("refund") == 3
    assert tool_tier("never_heard_of_it") == 3  # fail closed


def test_idempotency_key_stable():
    a = idempotency_key("conv1", 3, "create_payment_link")
    b = idempotency_key("conv1", 3, "create_payment_link")
    c = idempotency_key("conv1", 4, "create_payment_link")
    assert a == b and a != c


def test_reply_pipeline_blocks_tool_json():
    res = run_pipeline('{"tool": "refund", "args": {}}', conversation_id="c1")
    assert res.verdict != "PASS"


def test_reply_pipeline_factuality():
    # 250.00 is in tool outputs; 260.00 is not.
    ok = run_pipeline("Your fare is 250.00 EUR.", conversation_id="c1",
                      allowed_values=["250.00 EUR"])
    assert ok.verdict == "PASS"
    bad = run_pipeline("Your fare is 260.00 EUR.", conversation_id="c1",
                       allowed_values=["250.00 EUR"])
    assert bad.verdict == "REGENERATE"


def test_reply_pipeline_emoji_slop():
    res = run_pipeline("Great news! 🎉🎉🎉", conversation_id="c1")
    assert res.verdict != "PASS"


def test_golden_cases_pass():
    import asyncio

    cases = [
        GoldenCase(name="jet", text="I need the jet to Geneva", expect_escalated=True),
        GoldenCase(name="complaint", text="this is unacceptable", expect_escalated=True),
        GoldenCase(name="quote", text="how much for a car to the airport",
                   expect_escalated=False),
    ]
    report = asyncio.run(run_golden_cases(cases))
    assert report["pass_rate"] == 1.0, report


def test_escalation_sets_state():
    import asyncio

    class _Stub(StubTools):
        pass

    orch = Orchestrator(tools=_Stub())
    ctx = TurnContext(tenant_id="t", conversation_id="c", state="CAPTURING")
    res = asyncio.run(orch.handle_turn(ctx, "I am furious about the delay"))
    assert res.escalated and res.state == "ESCALATED" and res.handoff_id == "h1"
