"""Payout saga workflow (Master Spec §11.3) — Temporal binding.

The domain layer owns the money truth (idempotent postings, absorbing terminal
states); Temporal owns durability of the orchestration: retries, the
callback-wait timeout, and compensation on failure.

Saga steps:
    1. quote     — FX quote via the Mojaloop rail
    2. execute   — reserve (PARTNER_PAYABLE -> IN_FLIGHT_SETTLEMENT) + transfer
    3. await     — scheme callback. Two wake-up paths: a Temporal signal (when
                   a signaler is wired) and a 30s state poll as the backstop,
                   bounded by the transfer TTL.
    4. settle    — core-api has already applied the fulfil posting
                   (IN_FLIGHT_SETTLEMENT -> PARTNER_CLEARING + FX remainder)
    abort path   — aborted/timeout: compensate (IN_FLIGHT_SETTLEMENT ->
                   PARTNER_PAYABLE) + HITL ops page
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import timedelta

from temporalio import workflow
from temporalio.common import RetryPolicy

with workflow.unsafe.imports_passed_through():
    from .activities import (compensate_payout, execute_payout, get_payout,
                             quote_payout)

# The Mojaloop adapter sweeps stale transfers at 15 min by default
# (MOJALOOP_TRANSFER_TTL_SECS); the wait outlives it by one sweep interval.
CALLBACK_WAIT_TIMEOUT = timedelta(minutes=17)
POLL_INTERVAL = timedelta(seconds=30)
TERMINAL_STATES = {"SETTLED", "COMPENSATED", "FAILED"}

FAST_RETRY = RetryPolicy(initial_interval=timedelta(seconds=2),
                         backoff_coefficient=2.0, maximum_attempts=5,
                         non_retryable_error_types=["CoreAPIError"])
COMPENSATE_RETRY = RetryPolicy(initial_interval=timedelta(seconds=5),
                               backoff_coefficient=2.0, maximum_attempts=10)


@dataclass
class PayoutSagaInput:
    tenant_id: str
    payout_id: str


@dataclass
class PayoutSagaResult:
    state: str  # SETTLED | COMPENSATED | FAILED
    payout_id: str
    detail: str = ""


@workflow.defn
class PayoutSagaWorkflow:
    def __init__(self) -> None:
        self._callback_kind: str | None = None

    @workflow.signal
    def transfer_callback(self, kind: str) -> None:
        """Optional early wake-up from a signaler observing the core-api
        Mojaloop callback: transfer.fulfilled | transfer.aborted |
        transfer.timeout. The poll backstop makes this optional."""
        self._callback_kind = kind

    @workflow.run
    async def run(self, inp: PayoutSagaInput) -> PayoutSagaResult:
        # Step 1: quote. A domain 4xx (CoreAPIError) is not retried.
        try:
            await workflow.execute_activity(
                quote_payout, args=[inp.tenant_id, inp.payout_id],
                start_to_close_timeout=timedelta(seconds=30),
                retry_policy=FAST_RETRY)
        except Exception as e:
            workflow.logger.warning("payout %s failed at quote: %s",
                                    inp.payout_id, e)
            return PayoutSagaResult(state="FAILED", payout_id=inp.payout_id,
                                    detail=f"quote: {str(e)[:300]}")

        # Step 2: execute (reserve + dispatch). Compensate on failure.
        try:
            await workflow.execute_activity(
                execute_payout, args=[inp.tenant_id, inp.payout_id],
                start_to_close_timeout=timedelta(seconds=30),
                retry_policy=FAST_RETRY)
        except Exception as e:
            await self._compensate(inp, f"execute: {e}")
            return PayoutSagaResult(state="COMPENSATED", payout_id=inp.payout_id,
                                    detail=f"execute failed: {str(e)[:300]}")

        # Step 3: await the terminal state — signal fast path, poll backstop.
        deadline = workflow.now() + CALLBACK_WAIT_TIMEOUT
        while workflow.now() < deadline:
            try:
                await workflow.wait_condition(
                    lambda: self._callback_kind is not None,
                    timeout=POLL_INTERVAL)
            except TimeoutError:
                pass  # poll tick
            payout = await workflow.execute_activity(
                get_payout, args=[inp.tenant_id, inp.payout_id],
                start_to_close_timeout=timedelta(seconds=15),
                retry_policy=FAST_RETRY)
            state = payout.get("state", "")
            if state in TERMINAL_STATES:
                return PayoutSagaResult(state=state, payout_id=inp.payout_id,
                                        detail=self._callback_kind or "")

        # TTL expired with the payout still in flight: compensate ourselves —
        # the adapter sweep may have missed it. The domain absorb-guard makes
        # this safe even if the sweep landed first.
        await self._compensate(inp, "callback wait TTL expired")
        return PayoutSagaResult(state="COMPENSATED", payout_id=inp.payout_id,
                                detail="no terminal state within TTL")

    async def _compensate(self, inp: PayoutSagaInput, reason: str) -> None:
        # Compensation must not vanish under deadline pressure: generous
        # retries, and the domain layer dedupes replays.
        await workflow.execute_activity(
            compensate_payout, args=[inp.tenant_id, inp.payout_id, reason],
            start_to_close_timeout=timedelta(seconds=30),
            retry_policy=COMPENSATE_RETRY)


__all__ = ["PayoutSagaInput", "PayoutSagaResult", "PayoutSagaWorkflow",
           "CALLBACK_WAIT_TIMEOUT", "POLL_INTERVAL"]
