"""Payout saga workflow tests on Temporal's time-skipping test server.

Activities are replaced by in-memory fakes (same names, no network); the
time-skipping server fast-forwards the 17-minute callback TTL, so the abort
path runs in milliseconds of wall time.
"""

from __future__ import annotations

import uuid

import pytest
from temporalio import activity
from temporalio.testing import WorkflowEnvironment
from temporalio.worker import Worker

from temporal_worker.workflows import (PayoutSagaInput, PayoutSagaResult,
                                       PayoutSagaWorkflow)

TASK_QUEUE = "payouts-test"


class FakeCore:
    """Scriptable core-api double: records calls, serves programmed states."""

    def __init__(self, states: list[str], fail_quote=False, fail_execute=False):
        self.states = list(states)
        self.fail_quote = fail_quote
        self.fail_execute = fail_execute
        self.compensations: list[str] = []
        self.executions: list[str] = []
        self.quotes: list[str] = []

    def activities(self):
        outer = self

        @activity.defn(name="quote_payout")
        async def quote(tenant_id: str, payout_id: str) -> dict:
            outer.quotes.append(payout_id)
            if outer.fail_quote:
                from temporal_worker.activities import CoreAPIError
                raise CoreAPIError("POST quote: 422 validation")
            return {"state": "QUOTED"}

        @activity.defn(name="execute_payout")
        async def execute(tenant_id: str, payout_id: str) -> dict:
            outer.executions.append(payout_id)
            if outer.fail_execute:
                raise RuntimeError("adapter unreachable")
            return {"state": "IN_FLIGHT"}

        @activity.defn(name="compensate_payout")
        async def compensate(tenant_id: str, payout_id: str, reason: str) -> dict:
            outer.compensations.append(reason)
            return {"state": "COMPENSATED"}

        @activity.defn(name="get_payout")
        async def get(tenant_id: str, payout_id: str) -> dict:
            state = outer.states[0] if outer.states else "IN_FLIGHT"
            if len(outer.states) > 1:
                outer.states.pop(0)
            return {"state": state}

        return [quote, execute, compensate, get]


async def _run(env: WorkflowEnvironment, fake: FakeCore,
               signal_kind: str | None = None) -> PayoutSagaResult:
    async with Worker(env.client, task_queue=TASK_QUEUE,
                      workflows=[PayoutSagaWorkflow],
                      activities=fake.activities()):
        handle = await env.client.start_workflow(
            PayoutSagaWorkflow.run,
            PayoutSagaInput(tenant_id="t-1", payout_id="po-1"),
            id=f"payout-test-{uuid.uuid4()}", task_queue=TASK_QUEUE)
        if signal_kind:
            # let quote+execute complete, then deliver the scheme callback
            import asyncio
            await asyncio.sleep(1)
            await handle.signal(PayoutSagaWorkflow.transfer_callback, signal_kind)
        return await handle.result()


@pytest.mark.asyncio
async def test_happy_path_signal_settles():
    fake = FakeCore(states=["IN_FLIGHT", "SETTLED"])
    async with await WorkflowEnvironment.start_time_skipping() as env:
        result = await _run(env, fake, signal_kind="transfer.fulfilled")
    assert result.state == "SETTLED"
    assert fake.quotes == ["po-1"] and fake.executions == ["po-1"]
    assert fake.compensations == []


@pytest.mark.asyncio
async def test_ttl_expiry_compensates():
    fake = FakeCore(states=["IN_FLIGHT"])  # never settles
    async with await WorkflowEnvironment.start_time_skipping() as env:
        result = await _run(env, fake)
    assert result.state == "COMPENSATED"
    assert fake.compensations and "TTL" in fake.compensations[0]


@pytest.mark.asyncio
async def test_execute_failure_compensates():
    fake = FakeCore(states=[], fail_execute=True)
    async with await WorkflowEnvironment.start_time_skipping() as env:
        result = await _run(env, fake)
    assert result.state == "COMPENSATED"
    assert fake.compensations and fake.compensations[0].startswith("execute")


@pytest.mark.asyncio
async def test_quote_domain_rejection_fails_without_compensation():
    fake = FakeCore(states=[], fail_quote=True)
    async with await WorkflowEnvironment.start_time_skipping() as env:
        result = await _run(env, fake)
    assert result.state == "FAILED"
    assert fake.compensations == []  # nothing reserved — nothing to reverse
